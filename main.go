package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"time"
)

// webAssets embeds the entire web/ subtree: index.html plus vendored Uppy
// assets so the binary is fully self-contained and works offline.
//
//go:embed all:web
var webAssets embed.FS

func init() {
	// Go's MIME registry does not consistently know about ES modules.
	_ = mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8")
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	svc, err := newService(cfg)
	if err != nil {
		log.Fatal(err)
	}
	go svc.reapTusLoop(context.Background())

	if err := svc.cleanup(context.Background()); err != nil {
		log.Printf("initial cleanup failed: %v", err)
	}

	mux := newRouter(svc)

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           logging(mux),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       90 * time.Second,
		// ReadTimeout is generous: chunked uploads stream many small requests,
		// but a slow single-shot upload of up to 10 GiB still needs hours.
		ReadTimeout: 2 * time.Hour,
	}
	log.Printf("easy-temp-host listening on %s with %s storage (allowed types: %s)", cfg.ListenAddr, cfg.Driver, svc.policy)
	log.Fatal(server.ListenAndServe())
}

func newRouter(svc *service) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", svc.home)
	mux.HandleFunc("POST /login", svc.login)
	mux.HandleFunc("POST /logout", svc.logout)
	mux.Handle("GET /api/config", svc.requireSession(http.HandlerFunc(svc.clientConfig)))
	mux.Handle("POST /api/upload", svc.requirePasswordQuery(http.HandlerFunc(svc.upload)))
	mux.Handle("GET /api/uploads/{id}/result", svc.requireSession(http.HandlerFunc(svc.tusResult)))
	mux.Handle("/api/uploads/", svc.requireSession(http.StripPrefix("/api/uploads/", svc.tus)))
	mux.HandleFunc("GET /files/{id}", svc.file)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	// Static vendored assets (Uppy bundle, icons) served under /assets/.
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(webSubtree()))))
	return mux
}

// webSubtree returns the embedded web/ directory as an fs.FS for serving.
func webSubtree() fs.FS {
	sub, err := fs.Sub(webAssets, "web")
	if err != nil {
		// embed.FS.Sub never errors for a valid path prefix; this is fatal.
		log.Fatalf("embed web subtree: %v", err)
	}
	return sub
}
