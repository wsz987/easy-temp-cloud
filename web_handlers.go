package main

import (
	"fmt"
	"io/fs"
	"net/http"
	"time"
)

// clientConfig exposes the runtime limits the browser needs to validate files
// before upload and to size chunks. Read-only and unauthenticated.
func (s *service) clientConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"maxFileSize":  s.config.MaxUploadBytes,
		"maxTotalSize": s.config.MaxStorageBytes,
		"chunkSize":    s.config.chunkSize(),
		"maxChunkSize": maxChunkSize,
		"allowedTypes": s.policy.String(),
		"retention":    formatRetention(s.config.Retention),
	})
}

func formatRetention(retention time.Duration) string {
	for _, unit := range []struct {
		duration time.Duration
		label    string
	}{
		{7 * 24 * time.Hour, "w"},
		{24 * time.Hour, "d"},
		{time.Hour, "h"},
		{time.Minute, "m"},
	} {
		if retention%unit.duration == 0 {
			return fmt.Sprintf("%d%s", retention/unit.duration, unit.label)
		}
	}
	return retention.String()
}

// index serves the embedded upload page. The page reads runtime limits from
// /api/config at load time, so the binary stays the single source of truth.
func (s *service) index(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(webSubtree(), "index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "upload page unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "index.html", zeroTime, bytesReader(data))
}

// tusResult returns the final public URL after tusd has synchronously validated
// and persisted the upload. It is intentionally short-lived in memory: the
// browser fetches it immediately after receiving the completed tus response.
func (s *service) tusResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.RLock()
	item, ok := s.tusResults[id]
	s.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "upload result not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":     s.urlFor(r, item),
		"created": item.Created.UTC().Format(time.RFC3339),
	})
}

// zeroTime disables Last-Modified so ServeContent always serves fresh bytes.
var zeroTime time.Time
