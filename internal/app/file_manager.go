package app

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// maxArchiveFiles caps how many objects a single batch zip may contain, to
// keep the streaming download bounded and predictable.
const maxArchiveFiles = 500

// fileEntry is one item in the file-manager listing.
type fileEntry struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	Created     int64  `json:"created"` // unix seconds
	Expires     int64  `json:"expires"` // unix seconds, 0 if unset
	DownloadURL string `json:"downloadUrl,omitempty"`
}

// listFiles returns every stored object the authenticated session may manage.
// It is the data source for the file manager view. Records are sorted newest
// first so the most recently uploaded files appear at the top.
func (s *service) listFiles(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	s.mu.RLock()
	entries := make([]fileEntry, 0, len(s.records))
	for _, rec := range s.records {
		entries = append(entries, s.toFileEntry(rec, r, now))
	}
	s.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Created > entries[j].Created
	})

	totalBytes := int64(0)
	for _, e := range entries {
		totalBytes += e.Size
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"files":      entries,
		"count":      len(entries),
		"totalBytes": totalBytes,
		"retention":  formatRetention(s.config.Retention),
	})
}

// toFileEntry maps a record to its API representation. The download URL is the
// same signed link served after upload; for local storage it carries the
// per-file key so the browser can download without a session.
func (s *service) toFileEntry(rec record, r *http.Request, now time.Time) fileEntry {
	var expires int64
	if !rec.Expires.IsZero() {
		expires = rec.Expires.Unix()
	} else {
		expires = rec.Created.Add(s.config.Retention).Unix()
	}
	downloadURL := ""
	if s.config.Driver == "oss" {
		if u, err := s.store.SignURL(rec.ObjectKey, s.config.Retention); err == nil {
			downloadURL = u
		}
	} else {
		base := s.config.PublicBaseURL
		if base == "" {
			scheme := "http"
			if r != nil && r.TLS != nil {
				scheme = "https"
			}
			host := ""
			if r != nil {
				host = r.Host
			}
			base = scheme + "://" + host
		}
		downloadURL = base + "/files/" + rec.ID + "?key=" + s.auth.FileKey(rec.ID)
	}
	return fileEntry{
		ID:          rec.ID,
		Filename:    rec.Filename,
		ContentType: rec.ContentType,
		Size:        rec.Size,
		Created:     rec.Created.Unix(),
		Expires:     expires,
		DownloadURL: downloadURL,
	}
}

// batchDeleteRequest is the JSON body for POST /api/files/delete.
type batchDeleteRequest struct {
	IDs []string `json:"ids"`
}

// batchDelete removes many objects in one request. Unknown IDs are skipped
// (treated as already deleted); every deletion error aborts the remaining set
// and returns a 500 with how many were removed before the failure.
func (s *service) batchDelete(w http.ResponseWriter, r *http.Request) {
	var body batchDeleteRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids is required")
		return
	}
	if len(body.IDs) > maxArchiveFiles {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("at most %d files per request", maxArchiveFiles))
		return
	}

	removed, err := s.deleteRecords(r.Context(), body.IDs)
	if err != nil {
		log.Printf("batch delete failed after %d/%d: %v", removed, len(body.IDs), err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   "delete failed",
			"removed": removed,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

// deleteRecords removes multiple committed objects and commits the index once
// at the end. It is the batch counterpart of deleteRecord.
func (s *service) deleteRecords(ctx context.Context, ids []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	// Track objects deleted in this batch so that, if the final index commit
	// fails, we can mark them as orphans for the next reconcile pass instead of
	// leaving a stale index.json pointing at missing objects.
	deleted := make(map[string]int64)
	for _, id := range ids {
		if !sha256Pattern.MatchString(id) {
			continue
		}
		item, ok := s.records[id]
		if !ok {
			continue
		}
		if err := s.store.Delete(ctx, item.ObjectKey); err != nil {
			// Objects already deleted in this batch are at risk: the on-disk
			// index still lists them. Mark them as orphans before bailing out
			// so the next reconcile/cleanup reaps both the objects and entries.
			if removed > 0 {
				s.markOrphansLocked(deleted)
			}
			return removed, err
		}
		delete(s.records, id)
		delete(s.tusResults, id)
		deleted[item.ObjectKey] = item.Size
		removed++
	}
	if removed == 0 {
		return 0, nil
	}
	if err := s.saveIndexLocked(); err != nil {
		// The objects are gone but index.json may still reference them. Hand
		// the batch to the orphan ledger so cleanup/reconcile finishes the job.
		s.markOrphansLocked(deleted)
		return removed, err
	}
	return removed, nil
}

// markOrphansLocked records objects whose bytes were deleted but whose index
// entry could not be committed. The next reconcile pass drops the stale record
// and the next cleanup deletes any leftover bytes. Caller must hold s.mu.
func (s *service) markOrphansLocked(deleted map[string]int64) {
	for key, size := range deleted {
		s.orphans[key] = size
	}
}

// archiveFiles streams a zip archive of the requested objects. For local
// storage the objects are read straight from disk; for OSS they are downloaded
// on demand. The response is flushed per object so very large batches start
// downloading immediately instead of buffering the whole archive in memory.
func (s *service) archiveFiles(w http.ResponseWriter, r *http.Request) {
	ids, err := parseArchiveIDs(r.URL.Query().Get("ids"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.mu.RLock()
	items := make([]record, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if rec, ok := s.records[id]; ok {
			items = append(items, rec)
		}
	}
	s.mu.RUnlock()

	if len(items) == 0 {
		writeError(w, http.StatusNotFound, "no matching files")
		return
	}

	// De-duplicate filenames inside the archive (e.g. two uploads both named
	// "photo.png") by appending a counter suffix.
	used := make(map[string]int, len(items))
	for i, it := range items {
		base := it.Filename
		if base == "" {
			base = "file"
		}
		if n := used[base]; n > 0 {
			ext := ""
			stem := base
			if dot := strings.LastIndex(base, "."); dot > 0 {
				ext = base[dot:]
				stem = base[:dot]
			}
			items[i].Filename = fmt.Sprintf("%s (%d)%s", stem, n, ext)
			used[base] = n + 1
		} else {
			used[base] = 1
		}
	}

	archiveName := "easy-temp-cloud-" + time.Now().UTC().Format("20060102-150405") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+archiveName+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)

	zipWriter := zip.NewWriter(w)
	flusher, _ := w.(http.Flusher)
	ctx := r.Context()

	for _, it := range items {
		if err := s.streamObjectIntoZip(ctx, zipWriter, it); err != nil {
			// A mid-stream failure leaves the zip half-written. We cannot change
			// the status code now (headers already sent); log and stop so the
			// client receives a truncated but valid prefix of the archive.
			log.Printf("archive stream %s failed: %v", it.ID, err)
			break
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	zipWriter.Close()
}

// streamObjectIntoZip opens one object and copies it into a zip entry.
func (s *service) streamObjectIntoZip(ctx context.Context, zw *zip.Writer, it record) error {
	reader, err := s.store.Open(ctx, it.ObjectKey)
	if err != nil {
		return err
	}
	defer reader.Close()

	header := &zip.FileHeader{
		Name:   it.Filename,
		Method: zip.Store, // no recompression; contents are already encoded media
	}
	entry, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(entry, reader)
	return err
}

// parseArchiveIDs parses the comma-separated id list from the query string and
// enforces the batch cap.
func parseArchiveIDs(raw string) ([]string, error) {
	if raw == "" {
		return nil, errors.New("ids is required")
	}
	parts := strings.Split(raw, ",")
	ids := make([]string, 0, len(parts))
	for _, p := range parts {
		id := strings.TrimSpace(p)
		if id == "" {
			continue
		}
		if !sha256Pattern.MatchString(id) {
			return nil, fmt.Errorf("invalid file id: %s", id)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, errors.New("ids is required")
	}
	if len(ids) > maxArchiveFiles {
		return nil, fmt.Errorf("at most %d files per archive", maxArchiveFiles)
	}
	return ids, nil
}

