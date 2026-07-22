package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"mime"

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
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	webFS, err := fs.Sub(webAssets, "src/web")
	if err != nil {
		log.Fatalf("load embedded web assets: %v", err)
	}

	server, err := app.NewServer(context.Background(), cfg, webFS)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("easy-temp-cloud listening on %s with %s storage", cfg.ListenAddr, cfg.Driver)
	log.Fatal(server.ListenAndServe())
}
