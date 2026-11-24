package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/apimgr/countries/src/common/i18n"
)

func (s *Server) handlePreferencesPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.templates == nil {
		renderFallbackError(w, r, http.StatusInternalServerError)
		return
	}
	data := s.pageContext(w, r, "/server/preferences")
	data["PreferencePages"] = s.registeredPages("/server/preferences")
	data["ShowCCPA"] = s.config.Server.Privacy.Data.Sold
	if cookie, err := r.Cookie("cookie_consent"); err == nil {
		var consent map[string]interface{}
		consentValue, err := url.QueryUnescape(cookie.Value)
		if err == nil && json.Unmarshal([]byte(consentValue), &consent) == nil {
			data["ConsentPreferences"] = consent["preferences"] == true
			data["ConsentAnalytics"] = consent["analytics"] == true
		}
	}
	if cookie, err := r.Cookie("ccpa_opt_out"); err == nil && cookie.Value == "true" {
		data["CCPAOptOut"] = true
	}
	s.renderTemplateResponse(w, r, "preferences-page.html", data, http.StatusOK)
}

func (s *Server) handlePreferencesAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	lang := s.languageForRequest(w, r)
	s.jsonResponse(w, map[string]interface{}{"ok": true, "data": map[string]interface{}{
		"theme": s.themeForRequest(r), "lang": lang, "prefs": map[string]string{},
	}})
}

func (s *Server) handleSavePreferencesAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	values := make(map[string]string)
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
			writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid preferences JSON")
			return
		}
	} else if err := r.ParseForm(); err != nil {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid preferences form")
		return
	} else {
		for key := range r.Form {
			values[key] = r.Form.Get(key)
		}
	}
	applied := make([]string, 0, len(values))
	ignored := make([]string, 0)
	theme := s.themeForRequest(r)
	lang := s.languageForRequest(w, r)
	for key, value := range values {
		switch key {
		case "theme":
			if !validTheme(value) {
				ignored = append(ignored, key)
				continue
			}
			http.SetCookie(w, &http.Cookie{Name: "theme", Value: value, Path: "/", MaxAge: 365 * 24 * 60 * 60, Secure: strings.HasPrefix(s.reportOrigin(r), "https://"), SameSite: http.SameSiteLaxMode})
			theme = value
			applied = append(applied, key)
		case "lang":
			value = strings.ToLower(strings.TrimSpace(value))
			if !i18n.IsSupported(value) {
				ignored = append(ignored, key)
				continue
			}
			http.SetCookie(w, &http.Cookie{Name: "lang", Value: value, Path: "/", MaxAge: 365 * 24 * 60 * 60, Secure: strings.HasPrefix(s.reportOrigin(r), "https://"), SameSite: http.SameSiteLaxMode})
			lang = value
			applied = append(applied, key)
		default:
			ignored = append(ignored, key)
		}
	}
	if len(applied) == 0 {
		writeAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "No supplied preference value was valid")
		return
	}
	data := map[string]interface{}{"theme": theme, "lang": lang, "prefs": map[string]string{}, "applied": applied, "ignored": ignored}
	s.jsonResponse(w, map[string]interface{}{"ok": true, "data": data})
}

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
		s.errorResponse(w, r, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	if !validCSRF(r) {
		s.errorResponse(w, r, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	if r.FormValue("action") == "reset" {
		cookieNames := map[string]struct{}{
			"theme": {}, "lang": {}, "cookie_consent": {}, "ccpa_opt_out": {},
			"countries_build": {}, "dismissed_announcements": {},
		}
		for _, cookie := range r.Cookies() {
			if strings.HasPrefix(cookie.Name, "countries_pref_") {
				cookieNames[cookie.Name] = struct{}{}
			}
		}
		secure := strings.HasPrefix(s.reportOrigin(r), "https://")
		for name := range cookieNames {
			http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, Secure: secure, SameSite: http.SameSiteLaxMode})
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Clear-Site-Data", `"storage"`)
		http.Redirect(w, r, "/server/preferences", http.StatusSeeOther)
		return
	}
	theme := strings.TrimSpace(r.FormValue("theme"))
	if validTheme(theme) {
		http.SetCookie(w, &http.Cookie{
			Name: "theme", Value: theme, Path: "/", MaxAge: 365 * 24 * 60 * 60,
			Secure: strings.HasPrefix(s.reportOrigin(r), "https://"), SameSite: http.SameSiteLaxMode,
		})
	}
	lang := strings.ToLower(strings.TrimSpace(r.FormValue("lang")))
	if lang == "auto" {
		http.SetCookie(w, &http.Cookie{Name: "lang", Value: "", Path: "/", MaxAge: -1, Secure: strings.HasPrefix(s.reportOrigin(r), "https://"), SameSite: http.SameSiteLaxMode})
	} else if i18n.IsSupported(lang) {
		http.SetCookie(w, &http.Cookie{
			Name: "lang", Value: lang, Path: "/", MaxAge: 365 * 24 * 60 * 60,
			Secure: strings.HasPrefix(s.reportOrigin(r), "https://"), SameSite: http.SameSiteLaxMode,
		})
	}
	if r.FormValue("consent_form") == "1" {
		preferencesConsent := r.Form.Has("consent_preferences")
		analyticsConsent := r.Form.Has("consent_analytics")
		if s.consentWithdrawn(r, preferencesConsent, analyticsConsent) {
			w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
		}
		consent, _ := json.Marshal(map[string]interface{}{
			"essential": true, "preferences": preferencesConsent,
			"analytics": analyticsConsent, "timestamp": time.Now().Unix(),
		})
		http.SetCookie(w, &http.Cookie{Name: "cookie_consent", Value: url.QueryEscape(string(consent)), Path: "/", MaxAge: 31536000, Secure: strings.HasPrefix(s.reportOrigin(r), "https://"), SameSite: http.SameSiteLaxMode})
		if s.config.Server.Privacy.Data.Sold {
			s.setCCPAOptOut(w, r, r.Form.Has("ccpa_opt_out"))
		}
	}
	path := s.preferencesRedirect(r, "/")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", path)
	w.WriteHeader(http.StatusSeeOther)
}

func (s *Server) handleCCPAOptOut(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := r.ParseForm(); err != nil || !validCSRF(r) {
		s.errorResponse(w, r, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	if !s.config.Server.Privacy.IsCCPAApplicable() {
		s.errorResponse(w, r, "Page not found", http.StatusNotFound)
		return
	}
	choice := r.FormValue("choice")
	if choice != "opt-out" && choice != "opt-in" {
		s.errorResponse(w, r, "Invalid privacy preference", http.StatusBadRequest)
		return
	}
	s.setCCPAOptOut(w, r, choice == "opt-out")
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/server/privacy#ccpa-opt-out", http.StatusSeeOther)
}

func (s *Server) setCCPAOptOut(w http.ResponseWriter, r *http.Request, optedOut bool) {
	value, maxAge := "", 0
	if optedOut {
		value, maxAge = "true", 31536000
	}
	http.SetCookie(w, &http.Cookie{
		Name: "ccpa_opt_out", Value: value, Path: "/", MaxAge: maxAge,
		Secure: strings.HasPrefix(s.reportOrigin(r), "https://"), SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) handleConsent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := r.ParseForm(); err != nil || !validCSRF(r) {
		s.errorResponse(w, r, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	choice := r.FormValue("choice")
	if choice != "accept" && choice != "decline" {
		s.errorResponse(w, r, "Invalid consent choice", http.StatusBadRequest)
		return
	}
	consent, _ := json.Marshal(map[string]interface{}{
		"essential": true, "preferences": choice == "accept", "analytics": choice == "accept", "timestamp": time.Now().Unix(),
	})
	if s.consentWithdrawn(r, choice == "accept", choice == "accept") {
		w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
	}
	http.SetCookie(w, &http.Cookie{
		Name: "cookie_consent", Value: url.QueryEscape(string(consent)), Path: "/", MaxAge: 31536000,
		Secure: strings.HasPrefix(s.reportOrigin(r), "https://"), SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, s.preferencesRedirect(r, "/"), http.StatusSeeOther)
}

func (s *Server) consentWithdrawn(r *http.Request, preferences, analytics bool) bool {
	if !s.config.Web.Headers.ClearSiteData.OnConsentWithdrawal {
		return false
	}
	cookie, err := r.Cookie("cookie_consent")
	if err != nil {
		return false
	}
	value, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		return false
	}
	var previous struct {
		Preferences bool `json:"preferences"`
		Analytics   bool `json:"analytics"`
	}
	if json.Unmarshal([]byte(value), &previous) != nil {
		return false
	}
	return previous.Preferences && !preferences || previous.Analytics && !analytics
}

func (s *Server) preferencesRedirect(r *http.Request, fallback string) string {
	if target := r.FormValue("redirect"); isLocalReturnPath(target) && !isPreferencesSubmitTarget(target) {
		return target
	}
	if referer := r.Header.Get("Referer"); referer != "" {
		parsed, err := url.Parse(referer)
		origin, originErr := url.Parse(s.reportOrigin(r))
		if err == nil && originErr == nil && parsed.Scheme != "" && parsed.Host != "" && parsed.User == nil &&
			strings.EqualFold(parsed.Scheme, origin.Scheme) && strings.EqualFold(parsed.Host, origin.Host) {
			target := parsed.EscapedPath()
			if target == "" {
				target = "/"
			}
			if parsed.RawQuery != "" {
				target += "?" + parsed.RawQuery
			}
			if isLocalReturnPath(target) && !isPreferencesSubmitTarget(target) {
				return target
			}
		}
	}
	return fallback
}

func isPreferencesSubmitTarget(target string) bool {
	parsed, err := url.ParseRequestURI(target)
	if err != nil {
		return false
	}
	switch parsed.Path {
	case "/server/preferences", "/server/preferences/sync", "/server/preferences/sync/import":
		return true
	default:
		return false
	}
}

func validTheme(theme string) bool {
	return theme == "dark" || theme == "light" || theme == "auto"
}

func isLocalReturnPath(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "/\\") || strings.ContainsAny(path, "\\\r\n") {
		return false
	}
	parsed, err := url.ParseRequestURI(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Scheme != "" {
		return false
	}
	switch parsed.Path {
	case "/server/preferences/sync/import":
		return false
	}
	return true
}
