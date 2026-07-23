package app

import (
	"io/fs"
	"net/http"
)

// NewRouter wires every route onto a new ServeMux. Call SetWebFS first.
func NewRouter(svc *service) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", svc.home)
	mux.HandleFunc("GET /login", svc.loginPageHandler)
	mux.HandleFunc("POST /api/auth/token", svc.issueToken)
	mux.Handle("GET /api/config", svc.requireBearer(http.HandlerFunc(svc.clientConfig)))
	mux.Handle("GET /api/files", svc.requireBearer(http.HandlerFunc(svc.listFiles)))
	mux.Handle("POST /api/files/delete", svc.requireBearer(http.HandlerFunc(svc.batchDelete)))
	mux.Handle("GET /api/files/archive", svc.requireBearer(http.HandlerFunc(svc.archiveFiles)))
	mux.Handle("DELETE /api/files/{id}", svc.requireBearer(http.HandlerFunc(svc.deleteFile)))
	mux.Handle("POST /api/upload", svc.requireBearer(http.HandlerFunc(svc.upload)))
	mux.Handle("GET /api/uploads/{id}/result", svc.requireBearer(http.HandlerFunc(svc.tusResult)))
	mux.Handle("/api/uploads/", svc.requireBearer(http.StripPrefix("/api/uploads/", svc.tus)))
	mux.HandleFunc("GET /files/{id}", svc.file)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	// Static vendored assets (Uppy bundle, icons) served under /assets/.
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(webSubtree()))))
	return mux
}

// Logging returns the access-log middleware. It records method, path, duration.
func Logging(next http.Handler) http.Handler { return logging(next) }

// webSubtree returns the injected web/ filesystem for serving static assets.
// webFS is configured by SetWebFS at startup; its root already points at the
// web/ contents (index.html, login.html, vendor/, ...).
func webSubtree() fs.FS { return webFS }
