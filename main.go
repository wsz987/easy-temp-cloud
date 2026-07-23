package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"easy-temp-cloud/internal/app"
	"easy-temp-cloud/internal/config"
)

// webAssets embeds the browser application so the binary runs without
// separately deployed static files.
//
//go:embed all:src/web
var webAssets embed.FS

func init() {
	// Go's MIME registry does not consistently know about ES modules.
	_ = mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8")
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	webFS, err := fs.Sub(webAssets, "src/web")
	if err != nil {
		log.Fatalf("load embedded web assets: %v", err)
	}

	server, err := app.NewServer(ctx, cfg, webFS)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("easy-temp-cloud listening on %s with %s storage", cfg.ListenAddr, cfg.Driver)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown server: %v", err)
		}
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
