package server

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/common/i18n"
)

type languageOption struct {
	Code       string
	NativeName string
}

func (s *Server) pageContext(w http.ResponseWriter, r *http.Request, path string) map[string]interface{} {
	lang := s.languageForRequest(w, r)
	return map[string]interface{}{
		"Lang": lang, "Dir": i18n.Direction(lang), "Languages": languageOptions(),
		"Theme": s.themeForRequest(r), "Path": path, "APIBasePath": api.APIBasePath(),
	}
}

func languageOptions() []languageOption {
	languages := make([]languageOption, 0, len(i18n.Languages()))
	for _, code := range i18n.Languages() {
		languages = append(languages, languageOption{Code: code, NativeName: i18n.NativeName(code)})
	}
	return languages
}

func (s *Server) languageForRequest(w http.ResponseWriter, r *http.Request) string {
	if query := r.URL.Query().Get("lang"); query != "" {
		lang := strings.ToLower(strings.TrimSpace(query))
		if !i18n.IsSupported(lang) {
			return "en"
		}
		http.SetCookie(w, &http.Cookie{
			Name: "lang", Value: lang, Path: "/", MaxAge: 365 * 24 * 60 * 60,
			HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode,
		})
		return lang
	}
	if cookie, err := r.Cookie("lang"); err == nil && i18n.IsSupported(cookie.Value) {
		return strings.ToLower(cookie.Value)
	}
	for _, lang := range acceptedLanguages(r.Header.Get("Accept-Language")) {
		if len(lang) > 2 {
			lang = lang[:2]
		}
		if i18n.IsSupported(lang) {
			return lang
		}
	}
	return "en"
}

func acceptedLanguages(header string) []string {
	type preference struct {
		code  string
		q     float64
		order int
	}
	preferences := make([]preference, 0)
	for index, entry := range strings.Split(header, ",") {
		parts := strings.Split(entry, ";")
		code := strings.ToLower(strings.TrimSpace(parts[0]))
		if code == "" || code == "*" {
			continue
		}
		quality := 1.0
		for _, parameter := range parts[1:] {
			name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && strings.TrimSpace(name) == "q" {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if quality > 0 {
			preferences = append(preferences, preference{code: code, q: quality, order: index})
		}
	}
	sort.SliceStable(preferences, func(i, j int) bool { return preferences[i].q > preferences[j].q })
	languages := make([]string, len(preferences))
	for index, item := range preferences {
		languages[index] = item.code
	}
	return languages
}
