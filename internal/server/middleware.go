package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// cspPolicy is the Content-Security-Policy applied to all UI and API responses.
// It is intentionally restrictive for an embedded SPA served from the same origin:
//
//   - default-src 'none'        – deny everything not explicitly allowed
//   - script-src 'self'         – scripts from same origin only
//   - style-src 'self'          – stylesheets from same origin only
//   - img-src 'self' data:      – images from same origin + inline data URIs
//   - font-src 'self'           – fonts from same origin only
//   - connect-src 'self'        – XHR/fetch/EventSource to same origin only
//   - frame-ancestors 'none'    – disallow embedding in frames (clickjacking)
//   - base-uri 'self'           – restrict <base> tag target
//   - form-action 'self'        – restrict <form> submission target
const cspPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; " +
	"frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// securityHeadersMiddleware adds security headers to every response.
// These headers apply to both UI HTML pages and JSON API responses.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "interest-cohort=()")
		w.Header().Set("Content-Security-Policy", cspPolicy)
		next.ServeHTTP(w, r)
	})
}

// csrfCookieName is the name of the double-submit CSRF cookie.
const csrfCookieName = "csrf_token"

// generateCSRFToken returns a cryptographically random 32-hex-char token.
func generateCSRFToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate CSRF token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// csrfMiddleware implements the double-submit cookie CSRF protection pattern.
//
// On GET / the middleware ensures the csrf_token cookie is set (readable by JS,
// SameSite=Lax, not HttpOnly). For mutating requests (POST/PUT/PATCH/DELETE) on
// any path except /webhook (which is protected by HMAC-SHA256 signature), the
// middleware requires the X-CSRF-Token request header to match the cookie value
// using a constant-time comparison; mismatches are rejected with HTTP 403.
func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /webhook has its own HMAC-based authentication; skip CSRF for it.
		if r.URL.Path == "/webhook" {
			next.ServeHTTP(w, r)
			return
		}

		// On GET / ensure the CSRF cookie is present (or non-empty) so the SPA can read it.
		if r.URL.Path == "/" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			existing, err := r.Cookie(csrfCookieName)
			if err != nil || existing.Value == "" {
				token, err := generateCSRFToken()
				if err != nil {
					http.Error(w, "Failed to generate CSRF token", http.StatusInternalServerError)
					return
				}
				// Mark Secure only when the connection is HTTPS — either direct TLS
				// or a trusted local reverse proxy that sets X-Forwarded-Proto=https.
				secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
				http.SetCookie(w, &http.Cookie{
					Name:     csrfCookieName,
					Value:    token,
					Path:     "/",
					SameSite: http.SameSiteLaxMode,
					HttpOnly: false, // must be readable by JavaScript
					Secure:   secure,
				})
			}
			next.ServeHTTP(w, r)
			return
		}

		// For mutating methods, validate the double-submit token.
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			cookie, err := r.Cookie(csrfCookieName)
			if err != nil || cookie.Value == "" {
				http.Error(w, "CSRF cookie missing", http.StatusForbidden)
				return
			}
			headerVal := r.Header.Get("X-CSRF-Token")
			if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(headerVal)) != 1 {
				http.Error(w, "CSRF token mismatch", http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
