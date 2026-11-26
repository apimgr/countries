package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
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

func (s *Server) languagePreferenceConsentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiPath := strings.TrimSuffix(s.requestURLPrefix(r), "/") + "/api/"
		if r.Method == http.MethodGet && r.URL.Query().Has("lang") &&
			!strings.HasPrefix(r.URL.Path, apiPath) && !s.preferencesConsentAccepted(r) {
			query := r.URL.Query()
			query.Del("lang")
			query.Set("consent_required", "1")
			redirect := *r.URL
			redirect.RawQuery = query.Encode()
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, redirect.RequestURI(), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func nextTheme(theme string) string {
	switch theme {
	case "dark":
		return "light"
	case "light":
		return "auto"
	default:
		return "dark"
	}
}

func languagePreference(r *http.Request) string {
	if requestPrivacySignal(r) != "" {
		return "auto"
	}
	if cookie, err := r.Cookie("lang"); err == nil && i18n.IsSupported(cookie.Value) {
		return strings.ToLower(cookie.Value)
	}
	return "auto"
}

func (s *Server) pageContext(w http.ResponseWriter, r *http.Request, path string) map[string]interface{} {
	lang := s.languageForRequest(w, r)
	csrf := csrfToken(w, r, strings.HasPrefix(s.reportOrigin(r), "https://"))
	return map[string]interface{}{
		"AppName": s.appName(),
		"Lang":    lang, "Dir": i18n.Direction(lang), "Languages": languageOptions(),
		"Theme": s.themeForRequest(r), "Path": path, "URLPrefix": s.requestURLPrefix(r), "APIBasePath": s.requestURLPrefix(r) + api.APIBasePath(), "CSRFToken": csrf, "Request": r,
		"HasConsentCookie":   s.hasConsentCookie(r),
		"ConsentRequired":    r.URL.Query().Get("consent_required") == "1",
		"LanguagePreference": languagePreference(r), "DetectedLanguageName": i18n.NativeName(lang),
	}
}

func (s *Server) appName() string {
	if name := strings.TrimSpace(s.config.Server.Branding.Title); name != "" {
		return name
	}
	return "Countries API"
}

func (s *Server) hasConsentCookie(r *http.Request) bool {
	cookie, err := r.Cookie("cookie_consent")
	if err != nil {
		return false
	}
	var consent struct {
		Essential bool  `json:"essential"`
		Timestamp int64 `json:"timestamp"`
	}
	value, err := url.PathUnescape(cookie.Value)
	if err != nil || json.Unmarshal([]byte(value), &consent) != nil {
		return false
	}
	return consent.Essential && consent.Timestamp > 0
}

func csrfToken(w http.ResponseWriter, r *http.Request, secure bool) string {
	if cookie, err := r.Cookie("csrf_token"); err == nil && len(cookie.Value) >= 32 {
		return cookie.Value
	}
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(value)
	http.SetCookie(w, &http.Cookie{Name: "csrf_token", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, Secure: secure, HttpOnly: false})
	return token
}

func validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie("csrf_token")
	if err != nil {
		return false
	}
	formToken := r.FormValue("csrf_token")
	return len(formToken) == len(cookie.Value) && subtle.ConstantTimeCompare([]byte(formToken), []byte(cookie.Value)) == 1
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
		if s.preferencesConsentAccepted(r) && requestPrivacySignal(r) == "" {
			http.SetCookie(w, &http.Cookie{
				Name: "lang", Value: lang, Path: "/", MaxAge: 365 * 24 * 60 * 60,
				Secure: r.TLS != nil || (s.isTrustedPeer(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")), SameSite: http.SameSiteLaxMode,
			})
		}
		return lang
	}
	if requestPrivacySignal(r) == "" {
		if cookie, err := r.Cookie("lang"); err == nil && i18n.IsSupported(cookie.Value) {
		return strings.ToLower(cookie.Value)
		}
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
