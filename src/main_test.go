package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var tinyPNG = []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 31, 21, 196, 137}

func TestStaticModuleIsServedAsJavaScript(t *testing.T) {
	service, err := newService(config{DataDir: t.TempDir(), MaxUploadBytes: 1024, MaxStorageBytes: 1024, Retention: 24 * time.Hour, Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/assets/vendor/uppy.min.mjs", nil)
	response := httptest.NewRecorder()
	newRouter(service).ServeHTTP(response, request)
	if got := response.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("module content type = %q, want JavaScript", got)
	}
}

func TestUploadDeduplicatesAndServesImage(t *testing.T) {
	directory := t.TempDir()
	service, err := newService(config{DataDir: directory, PublicBaseURL: "http://images.test", MaxUploadBytes: 1024, MaxStorageBytes: 1024, Retention: 24 * time.Hour, Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	first := postFile(t, service, tinyPNG)
	if first.Code != http.StatusCreated {
		t.Fatalf("first upload status = %d: %s", first.Code, first.Body.String())
	}
	second := postFile(t, service, tinyPNG)
	if second.Code != http.StatusOK {
		t.Fatalf("duplicate upload status = %d: %s", second.Code, second.Body.String())
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.URL == "" || payload.URL != decodeURL(t, second.Body.Bytes()) {
		t.Fatalf("deduplicated URL mismatch: %q", payload.URL)
	}
	request := httptest.NewRequest(http.MethodGet, "/files/"+payload.URL[len("http://images.test/files/"):], nil)
	response := httptest.NewRecorder()
	router := http.NewServeMux()
	router.HandleFunc("GET /files/{id}", service.file)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("served response = %d (%s)", response.Code, response.Header().Get("Content-Type"))
	}
	if got := response.Header().Get("Content-Disposition"); got != "attachment" {
		t.Fatalf("Content-Disposition = %q, want attachment", got)
	}
	if len(service.records) != 1 {
		t.Fatalf("records = %d, want 1", len(service.records))
	}
}

type signedURLStore struct{ url string }

func (s signedURLStore) Put(context.Context, string, string, string) error { return nil }
func (s signedURLStore) Delete(context.Context, string) error              { return nil }
func (s signedURLStore) SignURL(string, time.Duration) (string, error)     { return s.url, nil }

func TestOSSURLsUseStorageSignedURL(t *testing.T) {
	service, err := newService(config{DataDir: t.TempDir(), MaxUploadBytes: 1024, MaxStorageBytes: 1024, Retention: time.Hour, Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	service.config.Driver = "oss"
	service.store = signedURLStore{url: "https://private.example/signed"}

	if got := service.urlFor(httptest.NewRequest(http.MethodGet, "/", nil), record{ObjectKey: "image-host/object"}); got != "https://private.example/signed" {
		t.Fatalf("OSS URL = %q, want signed URL", got)
	}
}

func TestExpiredObjectsAreDeleted(t *testing.T) {
	directory := t.TempDir()
	service, err := newService(config{DataDir: directory, MaxUploadBytes: 1024, Retention: time.Hour, Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := os.WriteFile(filepath.Join(directory, "objects", id), tinyPNG, 0600); err != nil {
		t.Fatal(err)
	}
	service.records[id] = record{ID: id, ObjectKey: id, ContentType: "image/png", Created: time.Now().Add(-2 * time.Hour)}
	if err := service.cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "objects", id)); !os.IsNotExist(err) {
		t.Fatalf("expired object still exists: %v", err)
	}
}

func TestUploadRejectsOversizedFile(t *testing.T) {
	service, err := newService(config{DataDir: t.TempDir(), MaxUploadBytes: int64(len(tinyPNG) - 1), MaxStorageBytes: 1024, Retention: 24 * time.Hour, Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	response := postFile(t, service, tinyPNG)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload status = %d: %s", response.Code, response.Body.String())
	}
}

func TestUploadRejectsWhenStorageCapacityIsFull(t *testing.T) {
	image := append(append([]byte{}, tinyPNG...), bytes.Repeat([]byte{0}, 600)...)
	service, err := newService(config{DataDir: t.TempDir(), MaxUploadBytes: 1024, MaxStorageBytes: 1024, Retention: 24 * time.Hour, Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if response := postFile(t, service, image); response.Code != http.StatusCreated {
		t.Fatalf("first upload status = %d: %s", response.Code, response.Body.String())
	}
	secondImage := append(append([]byte{}, image...), 0)
	if response := postFile(t, service, secondImage); response.Code != http.StatusInsufficientStorage {
		t.Fatalf("storage-full upload status = %d: %s", response.Code, response.Body.String())
	}
	entries, err := os.ReadDir(filepath.Join(service.config.DataDir, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("storage-full request left %d temporary files", len(entries))
	}
}

func TestReservationsPreventConcurrentTemporaryUploadsFromExceedingCapacity(t *testing.T) {
	service, err := newService(config{DataDir: t.TempDir(), MaxUploadBytes: 1024, MaxStorageBytes: 100, Retention: 24 * time.Hour, Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.reserve(context.Background(), 60); err != nil {
		t.Fatalf("first reservation: %v", err)
	}
	defer service.release(60)
	if err := service.reserve(context.Background(), 41); !errors.Is(err, errStorageFull) {
		t.Fatalf("second reservation error = %v, want storage full", err)
	}
}

func TestStartupRemovesInterruptedLocalUploads(t *testing.T) {
	directory := t.TempDir()
	cfg := config{DataDir: directory, MaxUploadBytes: 1024, MaxStorageBytes: 1024, Retention: 24 * time.Hour, Driver: "local"}
	if _, err := newService(cfg); err != nil {
		t.Fatal(err)
	}
	orphanID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	orphanObject := filepath.Join(directory, "objects", orphanID)
	orphanTemp := filepath.Join(directory, "tmp", "upload-interrupted")
	if err := os.WriteFile(orphanObject, tinyPNG, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphanTemp, tinyPNG, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newService(cfg); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{orphanObject, orphanTemp} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("interrupted upload remains at %s: %v", path, err)
		}
	}
}

func postFile(t *testing.T, service *service, contents []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "image.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	service.upload(response, request)
	return response
}

func decodeURL(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	return payload.URL
}

// tinyPDF holds the magic bytes http.DetectContentType recognises as application/pdf.
var tinyPDF = []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3")

func TestTypePolicyPresetsAndWildcards(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		allowed  []string
		rejected []string
	}{
		{"empty defaults to all", "", []string{"image/jpeg", "image/png", "image/gif", "image/webp", "video/mp4", "application/pdf"}, nil},
		{"all accepts anything", "all", []string{"image/jpeg", "video/mp4", "application/octet-stream", "text/plain"}, nil},
		{"alias videos", "videos", []string{"video/mp4", "video/webm", "video/quicktime"}, []string{"image/png", "audio/mpeg"}},
		{"prefix wildcard image", "image/*", []string{"image/jpeg", "image/webp", "image/avif"}, []string{"video/mp4"}},
		{"mixed list", "images,video/*,application/pdf", []string{"image/png", "video/mp4", "application/pdf"}, []string{"audio/mpeg", "application/zip"}},
		{"exact mime", "image/png", []string{"image/png"}, []string{"image/jpeg", "image/webp"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := parseTypePolicy(tt.raw)
			if err != nil {
				t.Fatalf("parseTypePolicy(%q): %v", tt.raw, err)
			}
			for _, mt := range tt.allowed {
				if !policy.allows(mt) {
					t.Errorf("allows(%q) = false, want true", mt)
				}
			}
			for _, mt := range tt.rejected {
				if policy.allows(mt) {
					t.Errorf("allows(%q) = true, want false", mt)
				}
			}
		})
	}
}

func TestTypePolicyRejectsInvalidInput(t *testing.T) {
	for _, raw := range []string{"image", "image/", "/plain", "image/png,badtype", "not-a-preset"} {
		if _, err := parseTypePolicy(raw); err == nil {
			t.Errorf("parseTypePolicy(%q) expected error, got nil", raw)
		}
	}
}

func TestTypePolicyAllAcceptsNonImageUpload(t *testing.T) {
	service, err := newService(config{DataDir: t.TempDir(), PublicBaseURL: "http://files.test", MaxUploadBytes: 1024, MaxStorageBytes: 1024, Retention: 24 * time.Hour, Driver: "local", AllowedTypes: "all"})
	if err != nil {
		t.Fatal(err)
	}
	response := postFile(t, service, tinyPDF)
	if response.Code != http.StatusCreated {
		t.Fatalf("all-mode PDF upload status = %d: %s", response.Code, response.Body.String())
	}
}

func TestTypePolicyImagesRejectsNonImageUpload(t *testing.T) {
	service, err := newService(config{DataDir: t.TempDir(), PublicBaseURL: "http://files.test", MaxUploadBytes: 1024, MaxStorageBytes: 1024, Retention: 24 * time.Hour, Driver: "local", AllowedTypes: "images"})
	if err != nil {
		t.Fatal(err)
	}
	response := postFile(t, service, tinyPDF)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("images-mode PDF upload status = %d, want 400: %s", response.Code, response.Body.String())
	}
}

func TestTypePolicyStoresCanonicalContentType(t *testing.T) {
	// Even though http.DetectContentType may emit "image/png", we store it
	// lowercased and without parameters; the served Content-Type must match.
	service, err := newService(config{DataDir: t.TempDir(), PublicBaseURL: "http://files.test", MaxUploadBytes: 1024, MaxStorageBytes: 1024, Retention: 24 * time.Hour, Driver: "local"})
	if err != nil {
		t.Fatal(err)
	}
	response := postFile(t, service, tinyPNG)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d: %s", response.Code, response.Body.String())
	}
	for _, record := range service.records {
		if record.ContentType != "image/png" {
			t.Fatalf("stored content type = %q, want image/png", record.ContentType)
		}
		return
	}
}
