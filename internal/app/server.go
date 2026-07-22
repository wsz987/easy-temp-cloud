package app

import (
	"context"
	"io/fs"
	"net/http"
	"time"

	"easy-temp-cloud/internal/config"
)

// NewServer creates a fully configured HTTP server and starts the background
// maintenance tasks. The provided filesystem must contain the web assets at
// its root.
func NewServer(ctx context.Context, cfg config.Config, webFS fs.FS) (*http.Server, error) {
	SetWebFS(webFS)

	svc, err := NewService(cfg)
	if err != nil {
		return nil, err
	}
	go svc.reapTusLoop(ctx)

	if err := svc.cleanup(ctx); err != nil {
		return nil, err
	}

	return &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           Logging(NewRouter(svc)),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       90 * time.Second,
		// Chunked uploads can take hours on slow connections.
		ReadTimeout: 2 * time.Hour,
	}, nil
}
