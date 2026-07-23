package app

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"easy-temp-cloud/internal/config"
)

func TestServerCloseReleasesMetadataDatabase(t *testing.T) {
	dir := t.TempDir()
	server, err := NewServer(context.Background(), config.Config{
		DataDir:         dir,
		AuthPassword:    "test-password",
		MaxUploadBytes:  1024,
		MaxStorageBytes: 1024,
		Retention:       time.Hour,
		Driver:          "local",
		AllowedTypes:    "all",
	}, fs.FS(os.DirFS(dir)))
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
	select {
	case <-server.closed:
	case <-time.After(time.Second):
		t.Fatal("metadata database was not released after server close")
	}
	if err := os.Remove(filepath.Join(dir, "metadata.db")); err != nil {
		t.Fatalf("remove metadata database after server close: %v", err)
	}
}

func TestServerShutdownHonorsDeadlineForActiveRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	managed := &Server{service: &service{}, cancel: func() {}}
	managed.Server = &http.Server{Handler: managed.trackRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = managed.Serve(listener) }()

	responseDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
		responseDone <- err
	}()
	<-started
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := managed.Shutdown(shutdownCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}

	close(release)
	if err := <-responseDone; err != nil {
		t.Fatal(err)
	}
	if err := managed.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
}

func TestServerShutdownHonorsDeadlineForBlockedMaintenance(t *testing.T) {
	maintenanceDone := make(chan struct{})
	closed := make(chan struct{})
	managed := &Server{
		service:         &service{},
		cancel:          func() {},
		maintenanceDone: maintenanceDone,
		closed:          closed,
	}
	managed.Server = &http.Server{Handler: http.NotFoundHandler()}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = managed.Serve(listener) }()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := managed.Shutdown(shutdownCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	close(maintenanceDone)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("metadata resources were not released after maintenance completed")
	}
}
