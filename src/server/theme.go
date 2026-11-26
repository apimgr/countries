package server

import (
	"net/http"
)

func (s *Server) themeForRequest(r *http.Request) string {
	if requestPrivacySignal(r) == "" {
		if cookie, err := r.Cookie("theme"); err == nil && validTheme(cookie.Value) {
			return cookie.Value
		}
	}
	if validTheme(s.config.WebUI.Theme) {
		return s.config.WebUI.Theme
	}
	return "dark"
}
