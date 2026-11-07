package server

import (
	"net/http"
	"strings"
)

func (s *Server) themeForRequest(r *http.Request) string {
	if cookie, err := r.Cookie("theme"); err == nil && validTheme(cookie.Value) {
		return cookie.Value
	}
	if validTheme(s.config.WebUI.Theme) {
		return s.config.WebUI.Theme
	}
	return "dark"
}

func (s *Server) handleSavePreferences(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		s.errorResponse(w, "Invalid preferences form", http.StatusBadRequest)
		return
	}
	theme := strings.TrimSpace(r.FormValue("theme"))
	if !validTheme(theme) {
		s.errorResponse(w, "Theme must be dark, light, or auto", http.StatusBadRequest)
		return
	}
	path := r.FormValue("return_to")
	if !isLocalReturnPath(path) {
		path = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name: "theme", Value: theme, Path: "/", MaxAge: 365 * 24 * 60 * 60,
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func validTheme(theme string) bool {
	return theme == "dark" || theme == "light" || theme == "auto"
}

func isLocalReturnPath(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "\\") {
		return false
	}
	switch path {
	case "/", "/search", "/coordinates", "/server/about", "/server/help", "/server/healthz", "/server/privacy", "/server/terms", "/server/security", "/server/contact", "/server/docs/swagger", "/server/docs/graphql":
		return true
	default:
		return false
	}
}
