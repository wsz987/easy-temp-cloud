//go:build ignore

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const tusVersion = "1.0.0"

func newTestService(t *testing.T, maxUpload, maxStorage int64, allowedTypes string) *service {
	t.Helper()
	cfg := config{
		DataDir:         t.TempDir(),
		PublicBaseURL:   "http://test",
		MaxUploadBytes:  maxUpload,
		MaxStorageBytes: maxStorage,
		Retention:       24 * time.Hour,
		Driver:          "local",
		AllowedTypes:    allowedTypes,
		AuthPassword:    "short1",
	}
	svc, err := newService(cfg)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	return svc
}

func testRouter(svc *service) http.Handler {
	router := newRouter(svc)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(authCookieName); err != nil {
			r.AddCookie(svc.auth.newSessionCookie(time.Now()))
		}
		router.ServeHTTP(w, r)
	})
}

func tusCreate(t *testing.T, router http.Handler, size int64) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/uploads/", nil)
	req.Header.Set("Tus-Resumable", tusVersion)
	req.Header.Set("Upload-Length", "5")
	if size != 5 {
		req.Header.Set("Upload-Length", "1")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if location == "" {
		t.Fatal("create response has no Location")
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	return parsed.Path
}

func tusPatch(t *testing.T, router http.Handler, location string, offset int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, location, bytes.NewBufferString(body))
	req.Header.Set("Tus-Resumable", tusVersion)
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestTusUploadResumesAndPersists(t *testing.T) {
	svc := newTestService(t, 1024, 1024, "all")
	router := testRouter(svc)
	location := tusCreate(t, router, 5)

	if rec := tusPatch(t, router, location, 0, "he"); rec.Code != http.StatusNoContent {
		t.Fatalf("first patch status = %d: %s", rec.Code, rec.Body.String())
	}
	head := httptest.NewRequest(http.MethodHead, location, nil)
	head.Header.Set("Tus-Resumable", tusVersion)
	headRec := httptest.NewRecorder()
	router.ServeHTTP(headRec, head)
	if headRec.Code != http.StatusOK || headRec.Header().Get("Upload-Offset") != "2" {
		t.Fatalf("resume HEAD = %d, offset %q", headRec.Code, headRec.Header().Get("Upload-Offset"))
	}
	if rec := tusPatch(t, router, location, 2, "llo"); rec.Code != http.StatusNoContent {
		t.Fatalf("final patch status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(svc.records) != 1 {
		t.Fatalf("persisted records = %d, want 1", len(svc.records))
	}
}

func TestTusDeleteReleasesReservation(t *testing.T) {
	svc := newTestService(t, 5, 5, "all")
	router := testRouter(svc)
	location := tusCreate(t, router, 5)

	deleteReq := httptest.NewRequest(http.MethodDelete, location, nil)
	deleteReq.Header.Set("Tus-Resumable", tusVersion)
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
	tusCreate(t, router, 5)
}

// TestTusRespectsForwardedProto guards the mixed-content fix: behind a
// TLS-terminating reverse proxy, nginx sets X-Forwarded-Proto: https, and the
// tus upload Location must use https so browsers do not block the upload as
// mixed content. See tus.go RespectForwardedHeaders.
func TestTusRespectsForwardedProto(t *testing.T) {
	svc := newTestService(t, 1024, 1024, "all")
	router := testRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/uploads/", nil)
	req.Host = "img.example.com"
	req.Header.Set("Tus-Resumable", tusVersion)
	req.Header.Set("Upload-Length", "5")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "img.example.com")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if location == "" {
		t.Fatal("create response has no Location")
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if parsed.Scheme != "https" {
		t.Fatalf("Location scheme = %q, want https (mixed-content fix)", parsed.Scheme)
	}
	if parsed.Host != "img.example.com" {
		t.Fatalf("Location host = %q, want img.example.com", parsed.Host)
	}
}

func TestTusRejectsFileOverSingleFileLimit(t *testing.T) {
	svc := newTestService(t, 4, 10, "all")
	router := testRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/uploads/", nil)
	req.Header.Set("Tus-Resumable", tusVersion)
	req.Header.Set("Upload-Length", "5")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized create status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestClientConfigExposesDeterministicLimits(t *testing.T) {
	svc := newTestService(t, 1<<20, 2<<20, "images")
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	testRouter(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("config status = %d", rec.Code)
	}
	var body struct {
		AllowedTypes string `json:"allowedTypes"`
		Retention    string `json:"retention"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if body.AllowedTypes != "image/gif, image/jpeg, image/png, image/webp" {
		t.Fatalf("allowedTypes = %q", body.AllowedTypes)
	}
	if body.Retention != "1d" {
		t.Fatalf("retention = %q, want 1d", body.Retention)
	}
}

func TestClientConfigPreservesMinuteRetention(t *testing.T) {
	svc, err := newService(config{
		DataDir:         t.TempDir(),
		MaxUploadBytes:  1024,
		MaxStorageBytes: 1024,
		Retention:       time.Minute,
		Driver:          "local",
	})
	if err != nil {
		t.Fatalf("newService: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	testRouter(svc).ServeHTTP(rec, req)

	var body struct {
		Retention string `json:"retention"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if body.Retention != "1m" {
		t.Fatalf("retention = %q, want 1m", body.Retention)
	}
}

func TestTusCleanupReleasesExpiredReservation(t *testing.T) {
	svc := newTestService(t, 5, 5, "all")
	location := tusCreate(t, testRouter(svc), 5)
	id := filepath.Base(location)
	path := filepath.Join(svc.tusDir, id)
	past := time.Now().Add(-tusSessionTTL - time.Minute)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatalf("age tus upload: %v", err)
	}
	if err := svc.reapTus(); err != nil {
		t.Fatalf("reap tus: %v", err)
	}
	tusCreate(t, testRouter(svc), 5)
}

func TestTusResultCacheIsBounded(t *testing.T) {
	svc := newTestService(t, 1024, 1024, "all")
	for i := 0; i < 2048; i++ {
		svc.rememberTusResult(fmt.Sprintf("upload-%d", i), record{})
	}
	if len(svc.tusResults) > 1024 {
		t.Fatalf("cached results = %d, want at most 1024", len(svc.tusResults))
	}
}

func TestUploadPageUsesOfficialUppyTusPlugin(t *testing.T) {
	source, err := webAssets.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	app := string(source)
	if !strings.Contains(app, "Tus") || !strings.Contains(app, ".use(Tus,") {
		t.Fatal("upload page does not configure Uppy Tus")
	}
	if strings.Contains(app, "ChunkedUploader") {
		t.Fatal("upload page still imports the custom chunk uploader")
	}
	if !strings.Contains(app, "storeFingerprintForResuming: true") {
		t.Fatal("upload page does not persist tus resume URLs")
	}
	if !strings.Contains(app, "theme: 'light'") {
		t.Fatal("upload page does not enable Uppy light theme")
	}
	index, err := webAssets.ReadFile("web/index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	if !strings.Contains(string(index), "upload-card") {
		t.Fatal("upload page has no upload workspace shell")
	}
}
