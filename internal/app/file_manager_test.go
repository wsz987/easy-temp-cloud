package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easy-temp-cloud/internal/config"
)

// newTestService builds a local-storage service rooted in a temp dir with two
// committed objects, returning the service and the two record ids.
func newTestService(t *testing.T) (*service, string, string) {
	t.Helper()
	dir := t.TempDir()
	svc, err := NewService(config.Config{
		DataDir:         dir,
		MaxUploadBytes:  1024,
		MaxStorageBytes: 1024,
		Retention:       time.Hour,
		Driver:          "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeObject := func(id, content string) string {
		path := filepath.Join(dir, "objects", id)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		svc.records[id] = record{ID: id, ObjectKey: id, Filename: content + ".txt", ContentType: "text/plain", Size: int64(len(content)), Created: time.Now().UTC(), Expires: time.Now().Add(time.Hour).UTC()}
		return id
	}
	idA := writeObject(strings.Repeat("a", 64), "hello")
	idB := writeObject(strings.Repeat("b", 64), "world!")
	return svc, idA, idB
}

func TestListFilesRequiresSession(t *testing.T) {
	svc, _, _ := newTestService(t)
	router := NewRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/files", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestListFilesReturnsServerFiles(t *testing.T) {
	svc, _, _ := newTestService(t)
	router := NewRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/files", nil)
	req.AddCookie(svc.auth.NewSessionCookie(time.Now()))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload struct {
		Files []fileEntry `json:"files"`
		Count int         `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 2 || len(payload.Files) != 2 {
		t.Fatalf("count = %d, files = %d, want 2", payload.Count, len(payload.Files))
	}
	for _, f := range payload.Files {
		if f.Filename == "" {
			t.Errorf("file %s has empty filename", f.ID)
		}
		if f.DownloadURL == "" {
			t.Errorf("file %s has empty downloadUrl", f.ID)
		}
	}
}

func TestFileManagerSingleDownloadUsesHiddenLink(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "src", "web", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "window.open(file.downloadUrl") {
		t.Fatal("single-file download must not open a new browser window")
	}
	if !strings.Contains(string(script), "triggerDownload(file.downloadUrl)") {
		t.Fatal("single-file download must use the hidden-link download helper")
	}
}

func TestBatchDeleteRemovesMultiple(t *testing.T) {
	svc, idA, idB := newTestService(t)
	router := NewRouter(svc)

	body := bytes.NewBufferString(`{"ids":["` + idA + `","` + idB + `"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/files/delete", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(svc.auth.NewSessionCookie(time.Now()))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var result struct {
		Removed int `json:"removed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Removed != 2 {
		t.Fatalf("removed = %d, want 2", result.Removed)
	}
	if _, ok := svc.records[idA]; ok {
		t.Error("record A still present")
	}
	if _, ok := svc.records[idB]; ok {
		t.Error("record B still present")
	}
	if _, err := os.Stat(filepath.Join(svc.config.DataDir, "objects", idA)); !os.IsNotExist(err) {
		t.Errorf("object A still on disk: %v", err)
	}
}

func TestBatchDeleteRejectsEmptyBody(t *testing.T) {
	svc, _, _ := newTestService(t)
	router := NewRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/files/delete", bytes.NewBufferString(`{}`))
	req.AddCookie(svc.auth.NewSessionCookie(time.Now()))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestArchiveStreamsZip(t *testing.T) {
	svc, idA, idB := newTestService(t)
	router := NewRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/files/archive?ids="+idA+","+idB, nil)
	req.AddCookie(svc.auth.NewSessionCookie(time.Now()))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type = %q, want application/zip", ct)
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("zip entries = %d, want 2", len(zr.File))
	}
	names := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(rc)
		rc.Close()
		names[f.Name] = string(content)
	}
	// Filenames were derived from the record Filename field ("hello.txt"/"world!.txt").
	if names["hello.txt"] != "hello" {
		t.Errorf("entry hello.txt content = %q", names["hello.txt"])
	}
	if names["world!.txt"] != "world!" {
		t.Errorf("entry world!.txt content = %q", names["world!.txt"])
	}
}

func TestParseArchiveIDsValidation(t *testing.T) {
	if _, err := parseArchiveIDs(""); err == nil {
		t.Error("empty string should error")
	}
	if _, err := parseArchiveIDs("not-an-id"); err == nil {
		t.Error("invalid id should error")
	}
	// valid single id
	ids, err := parseArchiveIDs(strings.Repeat("a", 64))
	if err != nil || len(ids) != 1 {
		t.Fatalf("expected 1 id, got %v %v", ids, err)
	}
	// duplicates allowed but...
	if _, err := parseArchiveIDs(strings.Repeat("a", 64) + "," + strings.Repeat("a", 64)); err != nil {
		t.Errorf("duplicate ids parse error: %v", err)
	}
}
