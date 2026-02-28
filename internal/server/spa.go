package server

import "net/http"

// handleRoot serves the Web UI SPA from embedded assets.
// For the exact root path or any path not matching /webhook, /api/, or
// /assets/, it serves index.html to support client-side routing.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// For the root path, serve index.html directly.
	if r.URL.Path == "/" {
		s.uiHandler.ServeHTTP(w, r)
		return
	}

	// Rewrite to "/" so the file server returns index.html for SPA client-side routing.
	// Clone the request to avoid mutating the original.
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/"
	s.uiHandler.ServeHTTP(w, r2)
}

// handleAssets serves static assets for the Web UI from the embedded filesystem.
func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Serve directly from the embedded filesystem.
	s.uiHandler.ServeHTTP(w, r)
}
