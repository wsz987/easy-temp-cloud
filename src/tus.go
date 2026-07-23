//go:build ignore

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tus/tusd/v2/pkg/filestore"
	"github.com/tus/tusd/v2/pkg/handler"
)

const (
	tusStagingDir = "tus"
	tusSessionTTL = 2 * time.Hour
	maxTusResults = 1024
)

// initTus configures the standard tus protocol implementation. FileStore owns
// chunk writes and persisted resume metadata; service callbacks own only this
// application's capacity, validation, and final-storage rules.
func (s *service) initTus() error {
	s.tusDir = filepath.Join(s.config.DataDir, tusStagingDir)
	if err := os.MkdirAll(s.tusDir, 0o750); err != nil {
		return err
	}
	if err := s.reconcileTus(); err != nil {
		return err
	}
	store := filestore.New(s.tusDir)
	composer := handler.NewStoreComposer()
	store.UseIn(composer)
	tus, err := handler.NewHandler(handler.Config{
		BasePath:        "/api/uploads/",
		StoreComposer:   composer,
		MaxSize:         s.config.MaxUploadBytes,
		DisableDownload: true,
		// Respect X-Forwarded-Proto / X-Forwarded-Host / Forwarded so that
		// behind a TLS-terminating reverse proxy (e.g. nginx serving HTTPS and
		// proxying to this service over plain HTTP) the absolute upload URLs
		// in tus responses use the public https scheme instead of http, which
		// would otherwise trigger mixed-content blocking in the browser.
		RespectForwardedHeaders:          true,
		PreUploadCreateCallback:          s.beforeTusCreate,
		PreFinishResponseCallback:        s.finishTusUpload,
		PreUploadTerminateCallback:       s.beforeTusTerminate,
		GracefulRequestCompletionTimeout: 15 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("create tus handler: %w", err)
	}
	s.tus = tus
	return nil
}

func (s *service) beforeTusCreate(event handler.HookEvent) (handler.HTTPResponse, handler.FileInfoChanges, error) {
	info := event.Upload
	if info.SizeIsDeferred || info.Size <= 0 {
		return handler.HTTPResponse{}, handler.FileInfoChanges{}, handler.NewError("ERR_UPLOAD_LENGTH_REQUIRED", "Upload-Length must be positive", http.StatusBadRequest)
	}
	if err := s.reserve(event.Context, info.Size); err != nil {
		if errors.Is(err, errStorageFull) {
			return handler.HTTPResponse{}, handler.FileInfoChanges{}, handler.NewError("ERR_STORAGE_FULL", "storage capacity exceeded", http.StatusInsufficientStorage)
		}
		return handler.HTTPResponse{}, handler.FileInfoChanges{}, err
	}
	return handler.HTTPResponse{}, handler.FileInfoChanges{ID: newTusID()}, nil
}

func (s *service) beforeTusTerminate(event handler.HookEvent) (handler.HTTPResponse, error) {
	s.release(event.Upload.Size)
	return handler.HTTPResponse{}, nil
}

func (s *service) finishTusUpload(event handler.HookEvent) (handler.HTTPResponse, error) {
	path := event.Upload.Storage["Path"]
	if path == "" {
		return handler.HTTPResponse{}, s.failTusUpload(event.Upload, errors.New("tus upload has no file path"))
	}
	size, hash, err := hashFile(path)
	if err == nil && size != event.Upload.Size {
		err = fmt.Errorf("uploaded size %d does not match declared %d", size, event.Upload.Size)
	}
	if err == nil {
		contentType, detectErr := detectContentType(path)
		if detectErr != nil {
			err = detectErr
		} else if !s.policy.allows(contentType) {
			err = fmt.Errorf("content type %q is not allowed (allowed types: %s)", contentType, s.policy)
		} else {
			id := fmt.Sprintf("%x", hash)
			created, _, persistErr := s.persist(event.Context, id, path, contentType, size, event.Upload.Size)
			if persistErr != nil {
				err = persistErr
			} else {
				s.release(event.Upload.Size)
				os.Remove(event.Upload.Storage["InfoPath"])
				s.rememberTusResult(event.Upload.ID, created)
				return handler.HTTPResponse{}, nil
			}
		}
	}
	return handler.HTTPResponse{}, s.failTusUpload(event.Upload, err)
}

func (s *service) rememberTusResult(id string, item record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tusResults[id]; !exists && len(s.tusResults) >= maxTusResults {
		for oldest := range s.tusResults {
			delete(s.tusResults, oldest)
			break
		}
	}
	s.tusResults[id] = item
}

func (s *service) failTusUpload(info handler.FileInfo, err error) error {
	os.Remove(info.Storage["Path"])
	os.Remove(info.Storage["InfoPath"])
	s.release(info.Size)
	if errors.Is(err, errStorageFull) {
		return handler.NewError("ERR_STORAGE_FULL", "storage capacity exceeded", http.StatusInsufficientStorage)
	}
	return handler.NewError("ERR_UPLOAD_REJECTED", err.Error(), http.StatusBadRequest)
}

// reconcileTus restores reservations for resumable uploads that survived a
// process restart and removes idle staging files. FileStore persists FileInfo
// as JSON, while the binary file's modification time records last activity.
func (s *service) reconcileTus() error {
	entries, err := os.ReadDir(s.tusDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".info") {
			continue
		}
		info, err := s.readTusInfo(filepath.Join(s.tusDir, entry.Name()))
		if err != nil {
			return err
		}
		if s.tusExpired(info) {
			s.removeTusFiles(info)
			continue
		}
		s.restoreReservation(info.Size)
	}
	return nil
}

func (s *service) reapTusLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.reapTus(); err != nil {
				log.Printf("tus cleanup failed: %v", err)
			}
		}
	}
}

func (s *service) reapTus() error {
	entries, err := os.ReadDir(s.tusDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".info") {
			continue
		}
		info, err := s.readTusInfo(filepath.Join(s.tusDir, entry.Name()))
		if err != nil {
			return err
		}
		if s.tusExpired(info) {
			s.removeTusFiles(info)
			s.release(info.Size)
		}
	}
	return nil
}

func (s *service) readTusInfo(path string) (handler.FileInfo, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return handler.FileInfo{}, err
	}
	var info handler.FileInfo
	if err := json.Unmarshal(contents, &info); err != nil {
		return handler.FileInfo{}, fmt.Errorf("read tus metadata %s: %w", path, err)
	}
	return info, nil
}

func (s *service) tusExpired(info handler.FileInfo) bool {
	stat, err := os.Stat(info.Storage["Path"])
	return err != nil || stat.ModTime().Before(time.Now().Add(-tusSessionTTL))
}

func (s *service) removeTusFiles(info handler.FileInfo) {
	os.Remove(info.Storage["Path"])
	os.Remove(info.Storage["InfoPath"])
}

func newTusID() string {
	bytes := make([]byte, 16)
	if _, err := readRand(bytes); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

func hashFile(path string) (int64, []byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, nil, err
	}
	return size, hash.Sum(nil), nil
}
