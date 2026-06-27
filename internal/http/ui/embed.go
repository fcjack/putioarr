// Package ui embeds the built Vue single-page application and serves it from the Go
// binary. Assets are produced by Vite at build time (see web/) into the dist directory
// and embedded via go:embed so the distroless runtime image needs no Node.
package ui

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// distFS holds the built SPA. The all: prefix ensures dotfiles (e.g. the committed
// dist/.gitkeep placeholder used before the first frontend build) are embedded so the
// package always compiles.
//
//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded SPA. Requests that do not
// map to a real embedded asset fall back to index.html so client-side routing works.
func Handler() (http.Handler, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("failed to open embedded ui assets: %w", err)
	}

	fileServer := http.FileServerFS(sub)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

		if upath != "" {
			if _, statErr := fs.Stat(sub, upath); statErr != nil {
				// Unknown path: serve the SPA entrypoint for client-side routing.
				r.URL.Path = "/"
			}
		}

		fileServer.ServeHTTP(w, r)
	}), nil
}
