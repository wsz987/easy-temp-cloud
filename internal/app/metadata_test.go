package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"easy-temp-cloud/internal/config"
)

func TestMetadataPersistsRecordsAndFindsExpired(t *testing.T) {
	store, err := openMetadata(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	expired := record{
		ID:          strings.Repeat("a", 64),
		ObjectKey:   "expired",
		ContentType: "image/png",
		Size:        4,
		Created:     time.Unix(100, 0).UTC(),
		Expires:     time.Unix(200, 0).UTC(),
	}
	active := record{
		ID:          strings.Repeat("b", 64),
		ObjectKey:   "active",
		ContentType: "image/png",
		Size:        8,
		Created:     time.Unix(300, 0).UTC(),
		Expires:     time.Unix(400, 0).UTC(),
	}
	if err := store.insert(expired); err != nil {
		t.Fatal(err)
	}
	if err := store.insert(active); err != nil {
		t.Fatal(err)
	}

	got, err := store.expiredBefore(time.Unix(250, 0).UTC(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []record{expired}) {
		t.Fatalf("expired records = %#v, want %#v", got, []record{expired})
	}
}

func TestNewServiceRestoresRecordsFromSQLite(t *testing.T) {
	cfg := config.Config{
		DataDir:         t.TempDir(),
		AuthPassword:    "test-password",
		MaxUploadBytes:  1024,
		MaxStorageBytes: 1024,
		Retention:       time.Hour,
		Driver:          "local",
		AllowedTypes:    "all",
	}
	first, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := record{
		ID:          strings.Repeat("c", 64),
		ObjectKey:   strings.Repeat("c", 64),
		ContentType: "image/png",
		Size:        1,
		Created:     time.Unix(100, 0).UTC(),
		Expires:     time.Unix(4000, 0).UTC(),
	}
	if err := first.metadata.insert(want); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "objects", want.ObjectKey), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := first.metadata.Close(); err != nil {
		t.Fatal(err)
	}

	restored, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.metadata.Close() })
	if got := restored.records[want.ID]; !reflect.DeepEqual(got, want) {
		t.Fatalf("record = %#v, want %#v", got, want)
	}
}

func TestPersistWritesSQLiteMetadataForServiceRestart(t *testing.T) {
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
	id := strings.Repeat("f", 64)
	source := filepath.Join(cfg.DataDir, "tmp", "upload")
	if err := os.WriteFile(source, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.reserve(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	item, duplicate, err := svc.persist(context.Background(), id, source, "image/png", "photo.png", 1, 1)
	svc.release(1)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate {
		t.Fatal("persist reported a duplicate")
	}
	if err := svc.close(); err != nil {
		t.Fatal(err)
	}

	restored, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.close() })
	if got := restored.records[item.ID]; !reflect.DeepEqual(got, item) {
		t.Fatalf("record = %#v, want %#v", got, item)
	}
}
