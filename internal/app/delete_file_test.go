package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"easy-temp-cloud/internal/auth"
	"easy-temp-cloud/internal/config"
)

func TestDeleteFileRequiresSessionAndRemovesObject(t *testing.T) {
	directory := t.TempDir()
	svc, err := NewService(config.Config{
		DataDir:         directory,
		AuthPassword:    "test-password",
		MaxUploadBytes:  1024,
		MaxStorageBytes: 1024,
		Retention:       time.Hour,
		Driver:          "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.close() })

	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	objectPath := filepath.Join(directory, "objects", id)
	if err := os.WriteFile(objectPath, []byte("file contents"), 0600); err != nil {
		t.Fatal(err)
	}
	svc.records[id] = record{ID: id, ObjectKey: id, ContentType: "text/plain", Size: 13, Created: time.Now()}

	router := NewRouter(svc)
	request := httptest.NewRequest(http.MethodDelete, "/api/files/"+id, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated delete status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/files/"+id, nil)
	addBearer(t, router, svc.config.AuthPassword, request)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d: %s", response.Code, http.StatusNoContent, response.Body.String())
	}
	if _, err := os.Stat(objectPath); !os.IsNotExist(err) {
		t.Fatalf("deleted object remains at %s: %v", objectPath, err)
	}
	if _, ok := svc.records[id]; ok {
		t.Fatal("deleted record remains in the index")
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/files/"+id, nil)
	addBearer(t, router, svc.config.AuthPassword, request)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestDeleteFileRejectsInvalidID(t *testing.T) {
	svc := &service{auth: &auth.Auth{}}
	response := httptest.NewRecorder()
	svc.deleteFile(response, httptest.NewRequest(http.MethodDelete, "/api/files/not-an-id", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("invalid delete status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
