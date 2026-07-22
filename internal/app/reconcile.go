package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"easy-temp-cloud/internal/config"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// reconcile removes files left behind if the process stopped between writing an
// object and committing its metadata. It only runs before accepting requests.
func (s *service) reconcile(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tempEntries, err := os.ReadDir(filepath.Join(s.config.DataDir, "tmp"))
	if err != nil {
		return err
	}
	for _, entry := range tempEntries {
		// Drop interrupted single-shot uploads ("upload-*"). Resumable tus
		// uploads keep their own metadata under data/tus and are reconciled by
		// initTus so they can be resumed after a process restart.
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "upload-") {
			if err := os.Remove(filepath.Join(s.config.DataDir, "tmp", entry.Name())); err != nil {
				return err
			}
		}
	}

	changed := false
	switch store := s.store.(type) {
	case localStore:
		entries, err := os.ReadDir(store.root)
		if err != nil {
			return err
		}
		for id, item := range s.records {
			path, err := store.path(item.ObjectKey)
			if err != nil || !config.FileExists(path) {
				delete(s.records, id)
				changed = true
			}
		}
		for _, entry := range entries {
			if entry.IsDir() || !sha256Pattern.MatchString(entry.Name()) {
				continue
			}
			if _, indexed := s.records[entry.Name()]; !indexed {
				if err := os.Remove(filepath.Join(store.root, entry.Name())); err != nil {
					return err
				}
			}
		}
	case ossStore:
		objects := make(map[string]struct{})
		marker := ""
		for {
			result, err := store.bucket.ListObjects(oss.Prefix(ossObjectPrefix), oss.Marker(marker))
			if err != nil {
				return fmt.Errorf("list OSS objects: %w", err)
			}
			for _, object := range result.Objects {
				objects[object.Key] = struct{}{}
			}
			if !result.IsTruncated {
				break
			}
			marker = result.NextMarker
		}
		indexedObjects := make(map[string]struct{}, len(s.records))
		for _, item := range s.records {
			indexedObjects[item.ObjectKey] = struct{}{}
		}
		for id, item := range s.records {
			_, exists := objects[item.ObjectKey]
			if !exists && !strings.HasPrefix(item.ObjectKey, ossObjectPrefix) {
				var err error
				exists, err = store.bucket.IsObjectExist(item.ObjectKey)
				if err != nil {
					return fmt.Errorf("check OSS object %s: %w", item.ObjectKey, err)
				}
			}
			if !exists {
				delete(s.records, id)
				changed = true
			}
		}
		for key := range objects {
			if _, indexed := indexedObjects[key]; !indexed {
				if err := store.Delete(context.Background(), key); err != nil {
					return fmt.Errorf("delete orphaned OSS object %s: %w", key, err)
				}
			}
		}
	}
	if changed {
		return s.saveIndexLocked()
	}
	return nil
}
