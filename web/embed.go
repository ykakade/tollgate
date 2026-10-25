// Package web holds the Tollgate dashboard: a Vite, React and TypeScript
// app in this directory. "make web" builds it into dist/, and the tollgate
// binary embeds dist/ (ADR 0033). The dashboard reads only the admin API.
package web

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the built dashboard. The files in assets/ have a hash in
// their names, so clients can keep them. Other files, such as index.html,
// must be checked again on each load.
func Handler() (http.Handler, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, fmt.Errorf("open dashboard files: %w", err)
	}
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	}), nil
}
