package server

import (
	"context"
	"net/http"
	"strings"
)

type originalRequestPathKey struct{}

// URLNormalizeMiddleware redirects non-canonical paths before route handling.
func URLNormalizeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		original := r.URL.Path
		r = r.WithContext(context.WithValue(r.Context(), originalRequestPathKey{}, original))
		canonical := original
		for strings.Contains(canonical, "//") {
			canonical = strings.ReplaceAll(canonical, "//", "/")
		}
		if canonical != "/" && strings.HasSuffix(canonical, "/") {
			lastSegment := canonical[strings.LastIndex(canonical[:len(canonical)-1], "/")+1 : len(canonical)-1]
			if !strings.Contains(lastSegment, ".") {
				canonical = strings.TrimRight(canonical, "/")
			}
		}
		if canonical != original {
			target := *r.URL
			target.Path = canonical
			target.RawPath = ""
			status := http.StatusMovedPermanently
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				status = http.StatusPermanentRedirect
			}
			http.Redirect(w, r, target.RequestURI(), status)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PathSecurityMiddleware rejects encoded or decoded traversal before routing.
func (s *Server) PathSecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		original, _ := r.Context().Value(originalRequestPathKey{}).(string)
		if original == "" {
			original = r.URL.Path
		}
		rawPath := r.URL.RawPath
		if rawPath == "" {
			rawPath = original
		}
		if strings.Contains(original, "..") || strings.Contains(rawPath, "..") || strings.Contains(strings.ToLower(rawPath), "%2e") {
			s.errorResponse(w, r, "Bad request", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}
