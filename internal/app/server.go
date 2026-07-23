package app

import (
	"context"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"easy-temp-cloud/internal/config"
)

// Server owns the HTTP server and the storage resources used by its handler.
// Always use Close or Shutdown so the metadata database is released.
type Server struct {
	*http.Server
	service         *service
	cancel          context.CancelFunc
	maintenance     sync.WaitGroup
	maintenanceDone chan struct{}
	requestMu       sync.Mutex
	closing         bool
	requests        int
	requestsDone    chan struct{}
	close           sync.Once
	closeErr        error
	closed          chan struct{}
}

func (s *Server) trackRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requestMu.Lock()
		if s.closing {
			s.requestMu.Unlock()
			http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
			return
		}
		s.requests++
		if s.requests == 1 {
			s.requestsDone = make(chan struct{})
		}
		s.requestMu.Unlock()
		defer func() {
			s.requestMu.Lock()
			s.requests--
			if s.requests == 0 {
				close(s.requestsDone)
			}
			s.requestMu.Unlock()
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) stopAcceptingRequests() {
	s.requestMu.Lock()
	s.closing = true
	s.requestMu.Unlock()
}

func (s *Server) waitForRequests(ctx context.Context) error {
	s.requestMu.Lock()
	if s.requests == 0 {
		s.requestMu.Unlock()
		return nil
	}
	done := s.requestsDone
	s.requestMu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) waitForMaintenance(ctx context.Context) error {
	if s.maintenanceDone == nil {
		return nil
	}
	select {
	case <-s.maintenanceDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) closeResources(ctx context.Context) error {
	s.cancel()
	if err := s.waitForMaintenance(ctx); err != nil {
		return err
	}
	if err := s.waitForRequests(ctx); err != nil {
		return err
	}
	s.close.Do(func() {
		s.closeErr = s.service.close()
		if s.closed != nil {
			close(s.closed)
		}
	})
	return s.closeErr
}

func (s *Server) closeResourcesAsync() {
	go func() { _ = s.closeResources(context.Background()) }()
}

// Shutdown stops maintenance, waits for active HTTP requests, then releases
// the metadata database.
func (s *Server) Shutdown(ctx context.Context) error {
	s.stopAcceptingRequests()
	s.cancel()
	if err := s.Server.Shutdown(ctx); err != nil {
		s.closeResourcesAsync()
		return err
	}
	if err := s.closeResources(ctx); err != nil {
		s.closeResourcesAsync()
		return err
	}
	return nil
}

// Close stops maintenance, closes active connections, then releases the
// metadata database.
func (s *Server) Close() error {
	s.stopAcceptingRequests()
	s.cancel()
	err := s.Server.Close()
	s.closeResourcesAsync()
	return err
}

// NewServer creates a fully configured HTTP server and starts the background
// maintenance tasks. The provided filesystem must contain the web assets at
// its root.
func NewServer(ctx context.Context, cfg config.Config, webFS fs.FS) (*Server, error) {
	SetWebFS(webFS)

	svc, err := NewService(cfg)
	if err != nil {
		return nil, err
	}
	maintenanceCtx, cancel := context.WithCancel(ctx)
	managed := &Server{service: svc, cancel: cancel, maintenanceDone: make(chan struct{}), closed: make(chan struct{})}
	managed.maintenance.Add(2)
	go func() {
		defer managed.maintenance.Done()
		svc.reapTusLoop(maintenanceCtx)
	}()
	go func() {
		defer managed.maintenance.Done()
		svc.cleanupLoop(maintenanceCtx)
	}()
	go func() {
		managed.maintenance.Wait()
		close(managed.maintenanceDone)
	}()

	if err := svc.cleanup(ctx); err != nil {
		cancel()
		_ = managed.closeResources(context.Background())
		return nil, err
	}

	managed.Server = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           managed.trackRequests(Logging(NewRouter(svc))),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       90 * time.Second,
		// Chunked uploads can take hours on slow connections.
		ReadTimeout: 2 * time.Hour,
	}
	return managed, nil
}
