package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/apimgr/countries/src/common/i18n"
)

func (s *Server) preferenceExport(w http.ResponseWriter, r *http.Request) (string, string) {
	values := url.Values{}
	lang := s.languageForRequest(w, r)
	values.Set("lang", lang)
	values.Set("theme", s.themeForRequest(r))
	canonical := values.Encode()
	return s.BuildURL(r, "/server/preferences/sync/import?"+canonical), base64.RawURLEncoding.EncodeToString([]byte(canonical))
}

func (s *Server) handlePreferencesSyncPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fullURL, code := s.preferenceExport(w, r)
	if strings.Contains(r.Header.Get("Accept"), "text/plain") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("URL: " + fullURL + "\nCode: " + code + "\n"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || acceptsJSON(r) {
		lang := s.languageForRequest(w, r)
		s.jsonResponse(w, map[string]interface{}{"ok": true, "data": map[string]interface{}{
			"theme": s.themeForRequest(r), "lang": lang, "prefs": map[string]string{}, "url": fullURL, "code": code,
		}})
		return
	}
	data := s.pageContext(w, r, "/server/preferences/sync")
	data["ExportURL"], data["ExportCode"] = fullURL, code
	s.renderTemplateResponse(w, r, "preferences-sync.html", data, http.StatusOK)
}

func (s *Server) handlePreferencesSyncAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	values := make(url.Values)
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid preferences JSON")
			return
		}
		for key, value := range payload {
			values.Set(key, value)
		}
	} else {
		if err := r.ParseForm(); err != nil {
			writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid preferences form")
			return
		}
		values = r.Form
	}
	values = mergePreferenceValues(decodePreferenceCode(values.Get("code")), values)
	applied, ignored := s.applyPreferenceImport(w, r, values)
	consentRequired := !s.preferencesConsentAccepted(r)
	if len(applied) == 0 && !consentRequired {
		writeAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "No supported preference value was supplied")
		return
	}
	theme, lang := s.themeForRequest(r), s.languageForRequest(w, r)
	if value := values.Get("theme"); validTheme(value) {
		theme = value
	}
	if value, valid := normalizePreferenceLanguage(values.Get("lang")); valid {
		lang = value
	}
	data := map[string]interface{}{"theme": theme, "lang": lang, "prefs": map[string]string{}, "applied": applied, "ignored": ignored}
	if consentRequired {
		data["consent_required"] = true
	}
	s.jsonResponse(w, map[string]interface{}{"ok": true, "data": data})
}

func (s *Server) applyPreferenceImport(w http.ResponseWriter, r *http.Request, values url.Values) ([]string, []string) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	applied, ignored := make([]string, 0), make([]string, 0)
	for index, key := range keys {
		if index >= 32 {
			break
		}
		if key == "csrf_token" || key == "code" || key == "redirect" || key == "action" || strings.HasPrefix(key, "consent_") || key == "ccpa_opt_out" {
			continue
		}
		list := values[key]
		if len(list) == 0 || len(list[0]) > 64 {
			ignored = append(ignored, key)
			continue
		}
		value := strings.TrimSpace(list[0])
		switch key {
		case "theme":
			if !validTheme(value) {
				ignored = append(ignored, key)
				continue
			}
			if !s.preferencesConsentAccepted(r) {
				ignored = append(ignored, key)
				continue
			}
			http.SetCookie(w, &http.Cookie{Name: "theme", Value: value, Path: "/", MaxAge: 31536000, Secure: strings.HasPrefix(s.reportOrigin(r), "https://"), SameSite: http.SameSiteLaxMode})
			applied = append(applied, key)
		case "lang":
			var valid bool
			value, valid = normalizePreferenceLanguage(value)
			if !valid {
				ignored = append(ignored, key)
				continue
			}
			if !s.preferencesConsentAccepted(r) {
				ignored = append(ignored, key)
				continue
			}
			http.SetCookie(w, &http.Cookie{Name: "lang", Value: value, Path: "/", MaxAge: 31536000, Secure: strings.HasPrefix(s.reportOrigin(r), "https://"), SameSite: http.SameSiteLaxMode})
			applied = append(applied, key)
		default:
			ignored = append(ignored, key)
		}
	}
	return applied, ignored
}

func normalizePreferenceLanguage(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if i18n.IsSupported(value) {
		return value, true
	}
	if base, _, ok := strings.Cut(value, "-"); ok && i18n.IsSupported(base) {
		return base, true
	}
	return "", false
}

func importValues(r *http.Request) url.Values {
	return mergePreferenceValues(decodePreferenceCode(r.URL.Query().Get("code")), r.URL.Query())
}

func decodePreferenceCode(raw string) url.Values {
	values := url.Values{}
	code := strings.TrimSpace(raw)
	if len(code) <= 2048 && code != "" {
		if q := strings.IndexByte(code, '?'); q >= 0 {
			code = code[q+1:]
		}
		if !strings.Contains(code, "=") || strings.HasSuffix(code, "=") {
			original := code
			decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(code, "="))
			if err != nil {
				decoded, err = base64.URLEncoding.DecodeString(code)
			}
			if err == nil {
				code = string(decoded)
			} else if !strings.Contains(original, "=") {
				code = ""
			}
		}
		if parsed, err := url.ParseQuery(code); err == nil {
			values = parsed
		}
	}
	return values
}

func mergePreferenceValues(codeValues, directValues url.Values) url.Values {
	combined := make(url.Values, len(codeValues)+len(directValues))
	for key, list := range codeValues {
		combined[key] = list
	}
	for key, list := range directValues {
		if key != "code" {
			combined[key] = list
		}
	}
	return combined
}

func (s *Server) handlePreferencesSyncSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil || !validCSRF(r) {
		s.errorResponse(w, r, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	values := mergePreferenceValues(decodePreferenceCode(r.Form.Get("code")), r.Form)
	s.applyPreferenceImport(w, r, values)
	w.Header().Set("Cache-Control", "no-store")
	target := s.preferencesRedirect(r, "/server/preferences/sync")
	if !s.preferencesConsentAccepted(r) {
		target = addConsentRequiredQuery(target)
	}
	redirectPreferenceRequest(w, target)
}

func (s *Server) handlePreferencesImport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	values := importValues(r)
	applied, ignored := s.applyPreferenceImport(w, r, values)
	if strings.HasPrefix(r.URL.Path, "/api/") || acceptsJSON(r) {
		theme, lang := s.themeForRequest(r), s.languageForRequest(w, r)
		if value := values.Get("theme"); validTheme(value) {
			theme = value
		}
		if value, valid := normalizePreferenceLanguage(values.Get("lang")); valid {
			lang = value
		}
		data := map[string]interface{}{"theme": theme, "lang": lang, "prefs": map[string]string{}, "applied": applied, "ignored": ignored}
		if !s.preferencesConsentAccepted(r) {
			data["consent_required"] = true
		}
		s.jsonResponse(w, map[string]interface{}{"ok": true, "data": data})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	target := s.preferencesRedirect(r, "/server/preferences/sync")
	if !s.preferencesConsentAccepted(r) {
		target = addConsentRequiredQuery(target)
	}
	redirectPreferenceRequest(w, target)
}

func redirectPreferenceRequest(w http.ResponseWriter, target string) {
	w.Header().Set("Location", target)
	w.WriteHeader(http.StatusSeeOther)
}

func acceptsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}
