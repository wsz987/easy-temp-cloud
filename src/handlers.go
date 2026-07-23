//go:build ignore

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// upload handles a single-shot multipart/form-data upload. The file is streamed
// to a temp file while its SHA-256 is computed, then persisted under that key.
func (s *service) upload(w http.ResponseWriter, r *http.Request) {
	maxRequestBytes := s.config.MaxUploadBytes + maxMultipartOverhead
	if r.ContentLength > maxRequestBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds maximum upload size")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	reservation := s.config.MaxUploadBytes
	if r.ContentLength >= 0 && r.ContentLength < reservation {
		reservation = r.ContentLength
	}
	if err := s.reserve(r.Context(), reservation); err != nil {
		writeStorageError(w, err)
		return
	}
	defer s.release(reservation)
	filePath, contentType, size, id, err := s.readUpload(r)
	if err != nil {
		writeUploadError(w, err)
		return
	}
	defer os.Remove(filePath)

	created, duplicate, err := s.persist(r.Context(), id, filePath, contentType, size, reservation)
	if err != nil {
		log.Printf("upload %s: %v", id, err)
		writeStorageError(w, err)
		return
	}
	status := http.StatusCreated
	if duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"url":     s.urlFor(r, created),
		"created": created.Created.UTC().Format(time.RFC3339),
	})
}

// readUpload extracts the "file" form part, streams it to disk, detects its
// content type from magic bytes, and computes the SHA-256 content key.
func (s *service) readUpload(r *http.Request) (string, string, int64, string, error) {
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		return "", "", 0, "", errors.New("Content-Type must be multipart/form-data")
	}
	reader := multipart.NewReader(r.Body, parameters["boundary"])
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", "", 0, "", fmt.Errorf("read multipart body: %w", err)
		}
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			continue
		}
		return s.writePart(part)
	}
	return "", "", 0, "", errors.New("multipart field file is required")
}

// writePart copies one multipart part into a temp file, hashing it along the
// way, then probes the first 512 bytes to detect the content type and enforce
// the configured type policy.
func (s *service) writePart(part *multipart.Part) (string, string, int64, string, error) {
	temp, err := os.CreateTemp(filepath.Join(s.config.DataDir, "tmp"), "upload-*")
	if err != nil {
		return "", "", 0, "", err
	}
	path := temp.Name()
	cleanup := func(err error) (string, string, int64, string, error) {
		temp.Close()
		os.Remove(path)
		return "", "", 0, "", err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(part, s.config.MaxUploadBytes+1))
	if err != nil {
		return cleanup(err)
	}
	if written == 0 {
		return cleanup(errors.New("file is empty"))
	}
	if written > s.config.MaxUploadBytes {
		return cleanup(errTooLarge)
	}
	if err := temp.Close(); err != nil {
		os.Remove(path)
		return "", "", 0, "", err
	}
	contentType, err := detectContentType(path)
	if err != nil {
		os.Remove(path)
		return "", "", 0, "", err
	}
	if !s.policy.allows(contentType) {
		os.Remove(path)
		return "", "", 0, "", fmt.Errorf("content type %q is not allowed (allowed types: %s)", contentType, s.policy)
	}
	return path, contentType, written, hex.EncodeToString(hash.Sum(nil)), nil
}

// file serves a stored object (local storage only). OSS objects are served from
// their public bucket URL directly.
func (s *service) file(w http.ResponseWriter, r *http.Request) {
	if s.config.Driver != "local" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	id := r.PathValue("id")
	if !sha256Pattern.MatchString(id) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !s.auth.validFileKey(id, r.URL.Query().Get("key")) {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	s.mu.RLock()
	item, ok := s.records[id]
	s.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	path, err := (localStore{root: filepath.Join(s.config.DataDir, "objects")}).path(item.ObjectKey)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", item.ContentType)
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeFile(w, r, path)
}

// writeJSON encodes value as JSON with the given status. It does not flush; the
// caller returns and the response is finished by the http server.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

// writeError writes a JSON error envelope.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeUploadError maps a readUpload error to the right HTTP status.
func writeUploadError(w http.ResponseWriter, err error) {
	if errors.As(err, new(*http.MaxBytesError)) || errors.Is(err, errTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds maximum upload size")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

// writeStorageError maps a persist/reserve error to the right HTTP status.
func writeStorageError(w http.ResponseWriter, err error) {
	if errors.Is(err, errStorageFull) {
		writeError(w, http.StatusInsufficientStorage, "storage capacity exceeded")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

// logging is the access-log middleware. It records method, path, and duration.
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

// detectContentType reads the first 512 bytes of path and returns the
// canonicalized MIME type. It closes the file in all paths.
func detectContentType(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	probe := make([]byte, 512)
	read, readErr := file.Read(probe)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", readErr
	}
	return normalizeContentType(http.DetectContentType(probe[:read])), nil
}
