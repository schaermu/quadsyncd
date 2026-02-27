// Package webui embeds the built Svelte SPA assets so they can be served
// by the webhook HTTP server at runtime without external file dependencies.
package webui

import (
	"embed"
	"io/fs"
)

// Assets holds the built Vite output from webui/dist.
// The embed directive references the dist directory relative to this file.
// A Makefile target copies webui/dist → internal/webui/dist before go build.

//go:embed all:dist
var assets embed.FS

// FS returns a filesystem rooted at the dist/ directory, stripping the
// "dist" prefix so callers can serve files directly (e.g. index.html,
// assets/index-xxx.js).
func FS() (fs.FS, error) {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		return nil, fmt.Errorf("embedded webui dist sub-filesystem: %w", err)
	}
	return sub, nil
}
