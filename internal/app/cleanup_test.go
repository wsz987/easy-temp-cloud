package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easy-temp-cloud/internal/config"
)

type deleteFailStore struct{ blobStore }

func (deleteFailStore) Delete(context.Context, string) error {
	return errors.New("delete object")
}

func TestCleanupFindsExpiredRecordsFromSQLite(t *testing.T) {
	cfg := config.Config{
		DataDir:         t.TempDir(),
		AuthPassword:    "test-password",
		MaxUploadBytes:  1024,
		MaxStorageBytes: 1024,
		Retention:       time.Hour,
		Driver:          "local",
		AllowedTypes:    "all",
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.metadata.Close() })

	item := record{
		ID:          strings.Repeat("d", 64),
		ObjectKey:   strings.Repeat("d", 64),
		ContentType: "image/png",
		Size:        1,
		Created:     time.Now().UTC().Add(-2 * time.Hour),
		Expires:     time.Now().UTC().Add(-time.Minute),
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "objects", item.ObjectKey), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.metadata.insert(item); err != nil {
		t.Fatal(err)
	}

	if err := svc.cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := svc.metadata.expiredBefore(time.Now().UTC(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expired records = %#v, want none", got)
	}
}

func TestCleanupRetainsMetadataWhenObjectDeleteFails(t *testing.T) {
	cfg := config.Config{
		DataDir:         t.TempDir(),
		AuthPassword:    "test-password",
		MaxUploadBytes:  1024,
		MaxStorageBytes: 1024,
		Retention:       time.Hour,
		Driver:          "local",
		AllowedTypes:    "all",
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.close() })
	item := record{
		ID:          strings.Repeat("e", 64),
		ObjectKey:   strings.Repeat("e", 64),
		ContentType: "image/png",
		Size:        1,
		Created:     time.Now().UTC().Add(-2 * time.Hour),
		Expires:     time.Now().UTC().Add(-time.Minute),
	}
	if err := svc.insertRecordLocked(item); err != nil {
		t.Fatal(err)
	}
	svc.store = deleteFailStore{blobStore: svc.store}

	if err := svc.cleanup(context.Background()); err == nil {
		t.Fatal("cleanup error = nil")
	}
	got, err := svc.metadata.expiredBefore(time.Now().UTC(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != item.ID {
		t.Fatalf("expired records = %#v, want %#v", got, item)
	}
	if _, ok := svc.records[item.ID]; !ok {
		t.Fatal("expired record was removed from cache")
	}
}
