package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/notifications"
	"github.com/go-chi/chi/v5"
)

type contactPageData struct {
	Request                                                             *http.Request
	AppName                                                             string
	Lang, Dir, Theme, Title, Description, Type, Token, Challenge, Error string
	Path                                                                string
	URLPrefix                                                           string
	Types                                                               []string
	SecurityURL, AbuseEmail, FQDN                                       string
	Review                                                              bool
	Success                                                             string
	Values                                                              map[string]string
	CSRFToken                                                           string
	SecurityID                                                          string
	SecurityMode                                                        bool
	CaptchaProvider                                                     string
	CaptchaSiteKey                                                      string
	HasConsentCookie                                                    bool
}

type contactAPIContextKey struct{}

func isContactAPI(r *http.Request) bool {
	value, _ := r.Context().Value(contactAPIContextKey{}).(bool)
	return value
}

var contactUsedTokens = map[string]time.Time{}
var contactUsedMu sync.Mutex
var fallbackCaptchaAnswers = map[string]string{}
var contactRateMu sync.Mutex
var contactRateEvents = map[string][]time.Time{}
var contactStrikes = map[string][]time.Time{}
var contactBans = map[string]time.Time{}
var contactDuplicates = map[string]time.Time{}

var contactURLPattern = regexp.MustCompile(`(?i)(https?://[^\s<>]+|\bwww\.[a-z0-9.-]+\.[a-z]{2,}\b|\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,}\b)`)
var contactMarkupPattern = regexp.MustCompile(`(?i)(</?[a-z][^>]*|<!|&(?:lt|gt|#0*60|#0*62);|\[(?:url|img|b|/))`)

func loadContactKey(db *sql.DB) []byte {
	key := make([]byte, 32)
	if db != nil {
		var encoded string
		if err := db.QueryRow(`SELECT value FROM app_secrets WHERE name = 'installation_secret'`).Scan(&encoded); err == nil {
			if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil && len(decoded) >= 32 {
				return decoded
			}
		}
		if _, err := rand.Read(key); err == nil {
			encoded = base64.StdEncoding.EncodeToString(key)
			_, _ = db.Exec(`INSERT INTO app_secrets(name,value,updated_at) VALUES('installation_secret',?,?) ON CONFLICT(name) DO NOTHING`, encoded, time.Now().UTC().Format(time.RFC3339Nano))
			var stored string
			if db.QueryRow(`SELECT value FROM app_secrets WHERE name='installation_secret'`).Scan(&stored) == nil {
				if decoded, e := base64.StdEncoding.DecodeString(stored); e == nil && len(decoded) >= 32 {
					return decoded
				}
			}
		}
	}
	if _, err := rand.Read(key); err != nil {
		return []byte("countries-contact-ephemeral-fallback-key")
	}
	return key
}

func loadPreviousContactKey(db *sql.DB) ([]byte, time.Time) {
	if db == nil {
		return nil, time.Time{}
	}
	var encoded, expiry string
	if err := db.QueryRow(`SELECT value FROM app_secrets WHERE name='installation_secret_previous'`).Scan(&encoded); err != nil {
		return nil, time.Time{}
	}
	if err := db.QueryRow(`SELECT value FROM app_secrets WHERE name='installation_secret_previous_until'`).Scan(&expiry); err != nil {
		return nil, time.Time{}
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, time.Time{}
	}
	until, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil || !time.Now().Before(until) {
		return nil, time.Time{}
	}
	return key, until
}

func hmacBytes(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func (s *Server) abuseContactEmail() string {
	for _, candidate := range []string{s.config.Server.Contact.Abuse.Email, s.config.Server.Contact.General.Email} {
		candidate = strings.TrimSpace(candidate)
		if address, err := mail.ParseAddress(candidate); err == nil && address.Address == candidate {
			return candidate
		}
	}
	return ""
}

func (s *Server) contactRecipient(kind string) string {
	addresses := []string{s.config.Server.Contact.Abuse.Email, s.config.Server.Contact.General.Email, s.config.Server.Contact.Admin.Email}
	if kind == "privacy" {
		addresses = []string{s.config.Server.Contact.DPO.Email, s.config.Server.Contact.General.Email, s.config.Server.Contact.Admin.Email}
	}
	for _, candidate := range addresses {
		candidate = strings.TrimSpace(candidate)
		if parsed, err := mail.ParseAddress(candidate); err == nil && parsed.Address == candidate {
			return candidate
		}
	}
	return ""
}

func offeredContactTypes(r *http.Request) []string {
	return []string{"abuse", "dmca", "privacy"}
}

func securityIDFor(key []byte, window int64) string {
	return hex.EncodeToString(hmacBytes(key, fmt.Sprint(window)))[:16]
}

func securityIDWindow(key []byte, supplied string, now time.Time) (int64, bool) {
	if len(supplied) != 16 {
		return 0, false
	}
	window := now.Unix() / 172800
	if hmac.Equal([]byte(supplied), []byte(securityIDFor(key, window))) {
		return window, true
	}
	if hmac.Equal([]byte(supplied), []byte(securityIDFor(key, window-1))) {
		return window - 1, true
	}
	return 0, false
}

func (s *Server) validSecurityID(supplied string, now time.Time) bool {
	_, ok := s.securityIDWindow(supplied, now)
	return ok
}

func (s *Server) securityIDWindow(supplied string, now time.Time) (int64, bool) {
	if window, ok := securityIDWindow(s.contactKey, supplied, now); ok {
		return window, true
	}
	if len(s.contactPreviousKey) == 32 && now.Before(s.contactPreviousUntil) {
		return securityIDWindow(s.contactPreviousKey, supplied, now)
	}
	return 0, false
}

func (s *Server) validContactMAC(expected []byte, payload string, now time.Time) bool {
	if hmac.Equal(expected, hmacBytes(s.contactKey, payload)) {
		return true
	}
	return len(s.contactPreviousKey) == 32 && now.Before(s.contactPreviousUntil) && hmac.Equal(expected, hmacBytes(s.contactPreviousKey, payload))
}

func logInvalidSecurityID(r *http.Request, supplied string) {
	sum := sha256.Sum256([]byte(supplied))
	log.Printf("security.security_id_invalid ip=%q user_agent=%q id_hash_prefix=%s", contactClientIP(r), r.UserAgent(), hex.EncodeToString(sum[:4]))
}

func (s *Server) issueSimpleCaptcha() (string, string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", "", err
	}
	id := hex.EncodeToString(random)
	var numbers [2]int
	if _, err := rand.Read(random); err != nil {
		return "", "", err
	}
	numbers[0] = int(random[0]%20) + 1
	if _, err := rand.Read(random); err != nil {
		return "", "", err
	}
	numbers[1] = int(random[0]%20) + 1
	operator := "+"
	answer := numbers[0] + numbers[1]
	if numbers[0] > numbers[1] && numbers[0]%2 == 0 {
		operator = "-"
		answer = numbers[0] - numbers[1]
	}
	if s.db != nil {
		hash := sha256.Sum256([]byte(id + "|" + fmt.Sprint(answer)))
		if _, err := s.db.Exec(`INSERT INTO captcha_challenges(id,answer_hash,expires_at) VALUES(?,?,?)`, id, hex.EncodeToString(hash[:]), time.Now().Add(10*time.Minute)); err != nil {
			return "", "", err
		}
	} else {
		contactUsedMu.Lock()
		if len(fallbackCaptchaAnswers) >= 50000 {
			for old := range fallbackCaptchaAnswers {
				delete(fallbackCaptchaAnswers, old)
				break
			}
		}
		fallbackCaptchaAnswers[id] = fmt.Sprint(answer)
		contactUsedMu.Unlock()
	}
	return id, fmt.Sprintf("%d %s %d", numbers[0], operator, numbers[1]), nil
}

func (s *Server) contactCaptchaProvider(r *http.Request) string {
	host := strings.ToLower(r.Host)
	if torRequest(r) || strings.Contains(host, ".onion") || strings.Contains(host, ".i2p") {
		return "simple"
	}
	provider := strings.ToLower(strings.TrimSpace(s.config.Server.Pages.Contact.Captcha))
	if provider == "" {
		return "simple"
	}
	return provider
}

func (s *Server) verifyContactCaptcha(r *http.Request) bool {
	provider := s.contactCaptchaProvider(r)
	response := strings.TrimSpace(r.FormValue("captcha_response"))
	if provider != "simple" {
		field := "g-recaptcha-response"
		endpoint := "https://www.google.com/recaptcha/api/siteverify"
		if provider == "hcaptcha" {
			field = "h-captcha-response"
			endpoint = "https://api.hcaptcha.com/siteverify"
		}
		if response == "" {
			response = strings.TrimSpace(r.FormValue(field))
		}
		if response != "" {
			return s.verifyProviderCaptcha(provider, endpoint, response, r)
		}
	}
	return s.verifySimpleCaptcha(r.FormValue("captcha_id"), r.FormValue("captcha_answer"))
}

func (s *Server) verifyProviderCaptcha(provider, endpoint, token string, r *http.Request) bool {
	secret := s.config.Server.Pages.Contact.CaptchaSecretKey
	if secret == "" || token == "" {
		return false
	}
	values := url.Values{"secret": {secret}, "response": {token}}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		values.Set("remoteip", host)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	if s.config.Server.Tor.UseNetwork {
		client = s.torOutboundHTTPClient()
		if client == nil {
			return false
		}
		clientCopy := *client
		clientCopy.Timeout = 5 * time.Second
		client = &clientCopy
	}
	response, err := client.PostForm(endpoint, values)
	if err != nil {
		return false
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			log.Printf("captcha provider response close failed: %v", closeErr)
		}
	}()
	var result struct {
		Success bool `json:"success"`
	}
	return response.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(response.Body, 16*1024)).Decode(&result) == nil && result.Success && (provider == "recaptcha" || provider == "hcaptcha")
}

func (s *Server) verifySimpleCaptcha(id, submitted string) bool {
	id, submitted = strings.TrimSpace(id), strings.TrimSpace(submitted)
	if len(id) != 32 || submitted == "" {
		return false
	}
	if s.db != nil {
		var expected string
		err := s.db.QueryRow(`SELECT answer_hash FROM captcha_challenges WHERE id=? AND expires_at>?`, id, time.Now()).Scan(&expected)
		_, _ = s.db.Exec(`DELETE FROM captcha_challenges WHERE id=?`, id)
		if err != nil {
			return false
		}
		got := sha256.Sum256([]byte(id + "|" + submitted))
		want, err := hex.DecodeString(expected)
		return err == nil && hmac.Equal(want, got[:])
	}
	contactUsedMu.Lock()
	defer contactUsedMu.Unlock()
	expected, ok := fallbackCaptchaAnswers[id]
	delete(fallbackCaptchaAnswers, id)
	return ok && hmac.Equal([]byte(expected), []byte(submitted))
}

func (s *Server) handleContactPage(w http.ResponseWriter, r *http.Request) {
	if !s.config.Server.Pages.Contact.Enabled {
		s.errorResponse(w, r, "Page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if s.templates == nil {
		renderFallbackError(w, r, http.StatusInternalServerError)
		return
	}
	lang := s.languageForRequest(w, r)
	typeName := r.URL.Query().Get("type")
	suppliedSecurityID := r.URL.Query().Get("security_id")
	securityMode := s.validSecurityID(suppliedSecurityID, time.Now())
	if suppliedSecurityID != "" && !securityMode {
		logInvalidSecurityID(r, suppliedSecurityID)
	}
	data := contactPageData{Request: r, AppName: s.appName(), Lang: lang, Dir: i18n.Direction(lang), Theme: s.themeForRequest(r), Title: "Contact and reports", Description: "This form is only for security reports, terms violations at a URL on this site, DMCA notices and privacy requests. It cannot be used for support, bug reports, feature requests or general questions.", Type: typeName, Types: offeredContactTypes(r), SecurityURL: "/server/security", FQDN: contactRequestFQDN(s, r), Values: map[string]string{}, CSRFToken: csrfToken(w, r, strings.HasPrefix(s.reportOrigin(r), "https://")), AbuseEmail: s.abuseContactEmail(), SecurityMode: securityMode, Path: r.URL.Path, URLPrefix: s.requestURLPrefix(r), HasConsentCookie: s.hasConsentCookie(r)}
	if r.URL.Query().Get("sent") == "1" {
		data.Success = s.config.Server.Pages.Contact.SuccessMessage
		data.Type = ""
		typeName = ""
	}
	if securityMode {
		data.Type = "security"
		data.SecurityID = suppliedSecurityID
	}
	if typeName != "" && !containsString(data.Types, typeName) {
		data.Type = ""
	}
	if data.Type != "" {
		data.Token = s.issueContactFormToken()
		data.Values["captcha_id"], data.Challenge, _ = s.issueSimpleCaptcha()
		data.CaptchaProvider = s.contactCaptchaProvider(r)
		data.CaptchaSiteKey = s.config.Server.Pages.Contact.CaptchaSiteKey
		if securityMode {
			data.Values["security_id"] = suppliedSecurityID
			data.Values["app_version"] = s.version
			data.Values["commit_hash"] = s.commit
			data.Values["timestamp"] = time.Now().UTC().Format(time.RFC3339)
			data.Values["request_user_agent"] = r.UserAgent()
		}
	}
	s.renderTemplateResponse(w, r, "contact.html", data, http.StatusOK)
}

func (s *Server) contactEditData(w http.ResponseWriter, r *http.Request, kind string, values map[string]string) (contactPageData, error) {
	lang := s.languageForRequest(w, r)
	issued, nonce := time.Now().Unix(), randomContactNonce()
	payload := fmt.Sprintf("%d|%s", issued, nonce)
	id, question, err := s.issueSimpleCaptcha()
	if err != nil {
		return contactPageData{}, err
	}
	values["form_token"] = fmt.Sprintf("%d.%s.%s", issued, nonce, hex.EncodeToString(hmacBytes(s.contactKey, "contact-form|"+payload)))
	values["captcha_id"] = id
	values["captcha_answer"] = ""
	return contactPageData{Request: r, AppName: s.appName(), Lang: lang, Dir: i18n.Direction(lang), Theme: s.themeForRequest(r), Title: "Contact and reports", Description: "This form is only for security reports, terms violations at a URL on this site, DMCA notices and privacy requests. It cannot be used for support, bug reports, feature requests or general questions.", Type: kind, Values: values, SecurityID: values["security_id"], SecurityMode: kind == "security", FQDN: contactRequestFQDN(s, r), AbuseEmail: s.abuseContactEmail(), CSRFToken: csrfToken(w, r, strings.HasPrefix(s.reportOrigin(r), "https://")), Token: values["form_token"], Challenge: question, CaptchaProvider: s.contactCaptchaProvider(r), CaptchaSiteKey: s.config.Server.Pages.Contact.CaptchaSiteKey, Path: r.URL.Path, URLPrefix: s.requestURLPrefix(r), HasConsentCookie: s.hasConsentCookie(r)}, nil
}

func randomContactNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) issueContactFormToken() string {
	issued, nonce := time.Now().Unix(), randomContactNonce()
	payload := fmt.Sprintf("%d|%s", issued, nonce)
	return fmt.Sprintf("%d.%s.%s", issued, nonce, hex.EncodeToString(hmacBytes(s.contactKey, "contact-form|"+payload)))
}

func containsString(items []string, item string) bool {
	for _, value := range items {
		if item == value {
			return true
		}
	}
	return false
}

func validContactShape(r *http.Request, kind string, apiRequest bool) bool {
	allowed := map[string]bool{"type": true, "name": true, "email": true, "subject": true, "message": true, "reported_url": true, "reason": true, "copyrighted_work": true, "good_faith": true, "accuracy": true, "signature": true, "request_kind": true, "captcha_answer": true, "captcha_seed": true, "captcha_id": true, "captcha_response": true, "g-recaptcha-response": true, "h-captcha-response": true, "form_token": true, "review_token": true, "step": true, "confirmed": true, "honeypot_flag": true, "security_id": true, "component": true, "component_other": true, "severity": true, "summary": true, "endpoint": true, "steps": true, "impact": true, "suggested_fix": true, "credit_preference": true, "credit_handle": true, "cve_requested": true, "disclosure_days": true, "coordinated_disclosure": true, "app_version": true, "commit_hash": true, "timestamp": true, "request_user_agent": true, "researcher_gpg": true}
	if !apiRequest {
		allowed["csrf_token"], allowed["website"] = true, true
	}
	for key := range r.Form {
		if !allowed[key] {
			return false
		}
		if len(r.Form[key]) != 1 {
			return false
		}
		if key == "csrf_token" || key == "website" || key == "type" || key == "form_token" || key == "review_token" || key == "step" || key == "confirmed" || key == "honeypot_flag" || strings.HasPrefix(key, "captcha") || key == "g-recaptcha-response" || key == "h-captcha-response" {
			continue
		}
		var typeFields []string
		switch kind {
		case "abuse":
			typeFields = []string{"name", "email", "reported_url", "reason", "subject", "message"}
		case "dmca":
			typeFields = []string{"name", "email", "reported_url", "copyrighted_work", "good_faith", "accuracy", "signature", "subject", "message"}
		case "privacy":
			typeFields = []string{"name", "email", "request_kind", "subject", "message"}
		case "security":
			typeFields = []string{"name", "email", "security_id", "component", "component_other", "severity", "summary", "endpoint", "steps", "impact", "suggested_fix", "credit_preference", "credit_handle", "cve_requested", "disclosure_days", "coordinated_disclosure", "app_version", "commit_hash", "timestamp", "request_user_agent", "researcher_gpg"}
		}
		if !containsString(typeFields, key) {
			return false
		}
	}
	if r.FormValue("step") != "submit" && r.FormValue("step") != "confirm" && r.FormValue("step") != "edit" {
		return false
	}
	if len(r.Form) == 0 {
		return false
	}
	if r.FormValue("form_token") == "" {
		return false
	}
	if r.FormValue("step") == "confirm" && (r.FormValue("review_token") == "" || r.FormValue("confirmed") != "on") {
		return false
	}
	if kind == "security" && r.FormValue("security_id") == "" {
		return false
	}
	_ = kind
	return true
}

func containsForbiddenContactChars(value string) bool {
	for _, r := range value {
		if r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\u202a' || r == '\u202b' || r == '\u202c' || r == '\u202d' || r == '\u202e' || r == '\u2066' || r == '\u2067' || r == '\u2068' || r == '\u2069' {
			return true
		}
	}
	return false
}

func containsControlChars(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
func contactLength(value string) int { return utf8.RuneCountInString(strings.TrimSpace(value)) }

func contactHostAllowed(r *http.Request, candidate, fqdn string) bool {
	allowed := map[string]bool{}
	add := func(host string) {
		host = strings.TrimSpace(strings.ToLower(host))
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.TrimSuffix(host, ".")
		if host != "" {
			allowed[host] = true
		}
	}
	add(fqdn)
	for _, domain := range strings.Split(os.Getenv("DOMAIN"), ",") {
		add(domain)
	}
	requestHost := strings.ToLower(r.Host)
	if strings.Contains(requestHost, ".onion") || strings.Contains(requestHost, ".i2p") {
		add(requestHost)
	}
	candidate = strings.TrimSuffix(strings.ToLower(candidate), ".")
	return allowed[candidate]
}

func contactRequestFQDN(s *Server, r *http.Request) string {
	parsed, err := url.Parse(s.reportOrigin(r))
	if err != nil {
		return s.config.Server.FQDN
	}
	return parsed.Hostname()
}

func contactLogBlocked(r *http.Request, reason string) {
	log.Printf("contact.spam_blocked reason=%q ip=%q", reason, contactClientIP(r))
}

func (s *Server) normalizedReviewFields(kind string, values map[string]string) string {
	keys := []string{"name", "email", "reported_url", "reason", "copyrighted_work", "good_faith", "accuracy", "signature", "request_kind", "subject", "message", "component", "component_other", "severity", "summary", "endpoint", "steps", "impact", "suggested_fix", "credit_preference", "credit_handle", "cve_requested", "disclosure_days", "coordinated_disclosure", "security_id", "researcher_gpg", "app_version", "commit_hash", "timestamp", "request_user_agent"}
	parts := []string{kind}
	for _, key := range keys {
		value := strings.Join(strings.Fields(strings.TrimSpace(values[key])), " ")
		if key == "email" {
			value = strings.ToLower(value)
		}
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, "\x00")
}

func (s *Server) issueReviewToken(values map[string]string, kind string, honeypot bool) string {
	issued, nonce, flags := time.Now().Unix(), randomContactNonce(), "-"
	if honeypot {
		flags = "d"
	}
	payload := fmt.Sprintf("%d|%s|%s|%x", issued, nonce, flags, sha256.Sum256([]byte(s.normalizedReviewFields(kind, values))))
	return fmt.Sprintf("%d.%s.%s.%s", issued, nonce, flags, hex.EncodeToString(hmacBytes(s.contactKey, "contact-review|"+payload)))
}

func (s *Server) validReviewToken(token string, values map[string]string, kind string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		return false
	}
	var issued int64
	if _, err := fmt.Sscanf(parts[0], "%d", &issued); err != nil {
		return false
	}
	age := time.Since(time.Unix(issued, 0))
	anti := s.config.Server.Pages.Contact.Antispam
	if age < time.Duration(anti.ReviewMinSeconds)*time.Second || age > time.Duration(anti.ReviewMaxSeconds)*time.Second {
		return false
	}
	flags := values["honeypot_flag"]
	if flags == "" {
		flags = "-"
	}
	if flags != parts[2] {
		return false
	}
	payload := fmt.Sprintf("%s|%s|%s|%x", parts[0], parts[1], flags, sha256.Sum256([]byte(s.normalizedReviewFields(kind, values))))
	want, err := hex.DecodeString(parts[3])
	if err != nil || !s.validContactMAC(want, "contact-review|"+payload, time.Now()) {
		return false
	}
	contactUsedMu.Lock()
	defer contactUsedMu.Unlock()
	for key, expiry := range contactUsedTokens {
		if time.Now().After(expiry) {
			delete(contactUsedTokens, key)
		}
	}
	if _, used := contactUsedTokens["review:"+parts[1]]; used {
		return false
	}
	if len(contactUsedTokens) >= 50000 {
		oldestKey := ""
		var oldest time.Time
		for key, expiry := range contactUsedTokens {
			if oldestKey == "" || expiry.Before(oldest) {
				oldestKey, oldest = key, expiry
			}
		}
		delete(contactUsedTokens, oldestKey)
	}
	contactUsedTokens["review:"+parts[1]] = time.Now().Add(time.Duration(anti.ReviewMaxSeconds) * time.Second)
	return true
}

func returnContactSuccess(w http.ResponseWriter, r *http.Request, s *Server, formToken string) {
	if isContactAPI(r) {
		s.jsonResponse(w, map[string]interface{}{"ok": true, "data": map[string]interface{}{"form_token": s.issueContactFormToken(), "message": s.config.Server.Pages.Contact.SuccessMessage}})
		return
	}
	http.Redirect(w, r, "/server/contact?sent=1", http.StatusSeeOther)
	_ = formToken
}

func (s *Server) renderContactReview(w http.ResponseWriter, r *http.Request, kind string, values map[string]string, honeypot bool, message string) {
	data := contactPageData{Request: r, AppName: s.appName(), Lang: s.languageForRequest(w, r), Dir: i18n.Direction(s.languageForRequest(w, r)), Theme: s.themeForRequest(r), Title: "Review your report", Description: "Review the information before sending.", Type: kind, Values: values, Review: true, Token: s.issueReviewToken(values, kind, honeypot), CSRFToken: csrfToken(w, r, strings.HasPrefix(s.reportOrigin(r), "https://")), Error: message, Path: r.URL.Path, URLPrefix: s.requestURLPrefix(r), HasConsentCookie: s.hasConsentCookie(r)}
	if isContactAPI(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "data": map[string]interface{}{"review": map[string]string{"type": kind, "email": values["email"], "subject": values["subject"]}, "review_token": data.Token, "form_token": values["form_token"], "confirm_after_seconds": s.config.Server.Pages.Contact.Antispam.ReviewMinSeconds}})
		return
	}
	s.renderTemplateResponse(w, r, "contact.html", data, http.StatusOK)
}

func contactClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func isContactOverlayRequest(r *http.Request) bool {
	host := strings.ToLower(r.Host)
	return strings.Contains(host, ".onion") || strings.Contains(host, ".i2p")
}

func contactWindow(cfg config.ContactAntispamConfig) time.Duration {
	if duration, err := time.ParseDuration(cfg.Window); err == nil && duration > 0 {
		return duration
	}
	return time.Hour
}

func isContactBanned(r *http.Request) bool {
	if isContactOverlayRequest(r) {
		return false
	}
	contactRateMu.Lock()
	defer contactRateMu.Unlock()
	now := time.Now()
	for key, until := range contactBans {
		if !now.Before(until) {
			delete(contactBans, key)
		}
	}
	until := contactBans[contactClientIP(r)]
	return now.Before(until)
}

func contactStrike(r *http.Request, cfg config.ContactAntispamConfig) {
	if cfg.Strikes < 1 || isContactOverlayRequest(r) {
		return
	}
	ip := contactClientIP(r)
	now := time.Now()
	window := contactWindow(cfg)
	contactRateMu.Lock()
	defer contactRateMu.Unlock()
	if len(contactStrikes) >= 50000 {
		oldestKey := ""
		var oldest time.Time
		for key, existing := range contactStrikes {
			last := time.Time{}
			if len(existing) > 0 {
				last = existing[len(existing)-1]
			}
			if last.IsZero() || now.Sub(last) >= window {
				delete(contactStrikes, key)
				break
			}
			if oldestKey == "" || last.Before(oldest) {
				oldestKey, oldest = key, last
			}
		}
		if len(contactStrikes) >= 50000 && oldestKey != "" {
			delete(contactStrikes, oldestKey)
		}
	}
	events := contactStrikes[ip][:0]
	for _, at := range contactStrikes[ip] {
		if now.Sub(at) < window {
			events = append(events, at)
		}
	}
	events = append(events, now)
	contactStrikes[ip] = events
	if len(events) >= cfg.Strikes {
		hours := cfg.BanHours
		if hours < 1 {
			hours = 24
		}
		if len(contactBans) >= 50000 {
			for key := range contactBans {
				delete(contactBans, key)
				break
			}
		}
		contactBans[ip] = now.Add(time.Duration(hours) * time.Hour)
	}
}

func (s *Server) allowContactRate(r *http.Request, email string, count bool) (bool, string) {
	cfg := s.config.Server.Pages.Contact.Antispam
	now := time.Now()
	window := contactWindow(cfg)
	ip := contactClientIP(r)
	if strings.Contains(r.Host, ".onion") || strings.Contains(r.Host, ".i2p") {
		ip = ""
	}
	keys := []struct {
		key    string
		limit  int
		reason string
	}{{"global", cfg.Global, "rate_global"}, {"email:" + strings.ToLower(email), cfg.PerEmail, "rate_email"}}
	if ip != "" {
		keys = append(keys, struct {
			key    string
			limit  int
			reason string
		}{"ip:" + ip, cfg.PerIP, "rate_ip"})
	}
	contactRateMu.Lock()
	defer contactRateMu.Unlock()
	for key, events := range contactRateEvents {
		live := events[:0]
		for _, at := range events {
			if now.Sub(at) < window {
				live = append(live, at)
			}
		}
		if len(live) == 0 {
			delete(contactRateEvents, key)
		} else {
			contactRateEvents[key] = live
		}
	}
	for _, item := range keys {
		if item.limit <= 0 {
			continue
		}
		live := contactRateEvents[item.key][:0]
		for _, at := range contactRateEvents[item.key] {
			if now.Sub(at) < window {
				live = append(live, at)
			}
		}
		contactRateEvents[item.key] = live
		if len(live) >= item.limit {
			return false, item.reason
		}
	}
	if count {
		for _, item := range keys {
			if item.limit > 0 {
				if len(contactRateEvents) >= 50000 {
					oldestKey := ""
					var oldest time.Time
					for key, events := range contactRateEvents {
						if len(events) > 0 && (oldestKey == "" || events[0].Before(oldest)) {
							oldestKey, oldest = key, events[0]
						}
					}
					if oldestKey != "" {
						delete(contactRateEvents, oldestKey)
					}
				}
				contactRateEvents[item.key] = append(contactRateEvents[item.key], now)
			}
		}
	}
	return true, ""
}

func (s *Server) isContactDuplicate(values map[string]string) bool {
	window := time.Duration(s.config.Server.Pages.Contact.Antispam.DuplicateWindowHours) * time.Hour
	if window <= 0 {
		return false
	}
	normalize := func(v string) string { return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(v)), " ")) }
	sum := sha256.Sum256([]byte(normalize(values["email"]) + "\x00" + normalize(values["subject"]) + "\x00" + normalize(values["message"])))
	key := hex.EncodeToString(sum[:])
	now := time.Now()
	contactRateMu.Lock()
	defer contactRateMu.Unlock()
	for old, expiry := range contactDuplicates {
		if now.After(expiry) {
			delete(contactDuplicates, old)
		}
	}
	if expiry, ok := contactDuplicates[key]; ok && now.Before(expiry) {
		return true
	}
	return false
}

func (s *Server) rememberContactDuplicate(values map[string]string) {
	window := time.Duration(s.config.Server.Pages.Contact.Antispam.DuplicateWindowHours) * time.Hour
	if window <= 0 {
		return
	}
	normalize := func(v string) string { return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(v)), " ")) }
	sum := sha256.Sum256([]byte(normalize(values["email"]) + "\x00" + normalize(values["subject"]) + "\x00" + normalize(values["message"])))
	contactRateMu.Lock()
	if len(contactDuplicates) >= 10000 {
		oldestKey := ""
		var oldest time.Time
		for key, expiry := range contactDuplicates {
			if oldestKey == "" || expiry.Before(oldest) {
				oldestKey, oldest = key, expiry
			}
		}
		if oldestKey != "" {
			delete(contactDuplicates, oldestKey)
		}
	}
	contactDuplicates[hex.EncodeToString(sum[:])] = time.Now().Add(window)
	contactRateMu.Unlock()
}

func (s *Server) validateContactContent(r *http.Request, kind string, values map[string]string) string {
	cfg := s.config.Server.Pages.Contact.Antispam
	emailParts := strings.Split(strings.ToLower(values["email"]), "@")
	for _, blocked := range cfg.BlockedEmailDomains {
		if len(emailParts) == 2 && strings.EqualFold(emailParts[1], strings.TrimPrefix(strings.ToLower(strings.TrimSpace(blocked)), "@")) {
			return "content"
		}
	}
	parts := make([]string, 0, len(values))
	for key, value := range values {
		if !strings.Contains(key, "token") && !strings.Contains(key, "captcha") && key != "security_id" {
			parts = append(parts, value)
		}
	}
	all := strings.Join(parts, "\n")
	for _, word := range cfg.BlockedWords {
		if strings.Contains(strings.ToLower(all), strings.ToLower(strings.TrimSpace(word))) && strings.TrimSpace(word) != "" {
			return "content"
		}
	}
	if cfg.RejectMarkup && contactMarkupPattern.MatchString(html.UnescapeString(all)) {
		return "markup"
	}
	if contactURLPattern.MatchString(values["name"]) || contactURLPattern.MatchString(values["subject"]) {
		return "content"
	}
	withoutEmailAddresses := regexp.MustCompile(`(?i)\b[^\s@]+@[^\s@]+\b`).ReplaceAllString(all, "")
	links := contactURLPattern.FindAllString(withoutEmailAddresses, -1)
	if len(links) > cfg.MaxLinks {
		return "content"
	}
	for _, raw := range links {
		if strings.Contains(raw, "@") {
			continue
		}
		candidate := raw
		if strings.HasPrefix(strings.ToLower(raw), "www.") || !strings.Contains(raw, "://") {
			candidate = "https://" + raw
		}
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Hostname() == "" {
			return "content"
		}
		if !contactHostAllowed(r, parsed.Hostname(), contactRequestFQDN(s, r)) {
			return "content"
		}
	}
	if kind == "abuse" || kind == "dmca" {
		u, err := url.Parse(values["reported_url"])
		if err != nil || u.Path == "" || u.Path == "/" || u.Path == "/server/contact" || strings.HasPrefix(u.Path, "/api/") {
			return "content"
		}
		route := chi.NewRouteContext()
		if !s.router.Match(route, http.MethodGet, u.Path) {
			return "content"
		}
		if strings.HasPrefix(u.Path, "/countries/") {
			code := strings.TrimPrefix(u.Path, "/countries/")
			if code == "" || strings.Contains(code, "/") || s.countriesService.GetByCode(code) == nil {
				return "content"
			}
		}
	}
	return ""
}

func (s *Server) encryptSecurityData(plain []byte) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(s.config.Server.Security.EncryptionKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("security encryption key is unavailable")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return append(nonce, gcm.Seal(nil, nonce, plain, nil)...), nil
}

func (s *Server) encryptSecurityReportBody(plain []byte) ([]byte, string, error) {
	publicKey, _, err := s.readSecurityPublicKey()
	if err != nil {
		if os.IsNotExist(err) {
			body, encryptErr := s.encryptSecurityData(plain)
			return body, "aes-256-gcm", encryptErr
		}
		return nil, "", fmt.Errorf("load security report public key: %w", err)
	}
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(publicKey))
	if err != nil || len(entities) == 0 {
		return nil, "", fmt.Errorf("load security report public key: invalid keyring")
	}
	var ciphertext bytes.Buffer
	writer, err := openpgp.Encrypt(&ciphertext, entities, nil, nil, nil)
	if err != nil {
		return nil, "", fmt.Errorf("encrypt security report with PGP: %w", err)
	}
	if _, err := writer.Write(plain); err != nil {
		return nil, "", fmt.Errorf("encrypt security report with PGP: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("finish PGP security report encryption: %w", err)
	}
	return ciphertext.Bytes(), "pgp", nil
}

func (s *Server) storeSecurityReport(values map[string]string) (string, string, []byte, string, error) {
	if s.db == nil {
		return "", "", nil, "", fmt.Errorf("security report storage unavailable")
	}
	payload := map[string]string{}
	for _, name := range []string{"name", "email", "component", "component_other", "severity", "summary", "endpoint", "steps", "impact", "suggested_fix", "credit_preference", "credit_handle", "cve_requested", "disclosure_days", "researcher_gpg", "app_version", "commit_hash", "timestamp", "request_user_agent"} {
		payload[name] = values[name]
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return "", "", nil, "", err
	}
	// Reserve the SQLite writer before reading the public key so key rotation
	// cannot switch keys between encryption and report insertion.
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", nil, "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE app_secrets SET updated_at = updated_at WHERE name = 'installation_secret'`); err != nil {
		return "", "", nil, "", err
	}
	bodyEnc, bodyAlg, err := s.encryptSecurityReportBody(plain)
	if err != nil {
		return "", "", nil, "", err
	}
	contactData, err := json.Marshal(map[string]string{"email": values["email"], "researcher_gpg": values["researcher_gpg"]})
	if err != nil {
		return "", "", nil, "", err
	}
	contactEnc, err := s.encryptSecurityData(contactData)
	if err != nil {
		return "", "", nil, "", err
	}
	randomID := make([]byte, 8)
	if _, err := rand.Read(randomID); err != nil {
		return "", "", nil, "", err
	}
	trackingID := "sec_" + hex.EncodeToString(randomID)
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return "", "", nil, "", err
	}
	statusToken := base64.RawURLEncoding.EncodeToString(token)
	tokenHash := sha256.Sum256([]byte(statusToken))
	credit := values["credit_preference"]
	creditName := values["credit_handle"]
	if credit == "real_name" {
		credit, creditName = "name", values["name"]
	}
	if credit == "anonymous" {
		credit, creditName = "anon", ""
	}
	if credit == "no" {
		creditName = ""
	}
	disclosureDays := 90
	if raw := values["disclosure_days"]; raw != "" {
		parsed, e := strconv.Atoi(raw)
		if e != nil || parsed < 0 {
			return "", "", nil, "", fmt.Errorf("invalid disclosure timeline")
		}
		disclosureDays = parsed
	}
	disclosureDate := time.Now().UTC().AddDate(0, 0, disclosureDays).Format("2006-01-02")
	idWindow, valid := s.securityIDWindow(values["security_id"], time.Now())
	if !valid {
		return "", "", nil, "", fmt.Errorf("invalid or expired security report identifier")
	}
	_, err = tx.Exec(`INSERT INTO security_reports(tracking_id,id_window,severity,component,body_enc,body_alg,contact_enc,credit_pref,credit_name,disclosure_date,cve_requested,token_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, trackingID, idWindow, values["severity"], values["component"], bodyEnc, bodyAlg, contactEnc, credit, creditName, disclosureDate, values["cve_requested"] == "on", hex.EncodeToString(tokenHash[:]))
	if err != nil {
		return "", "", nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", nil, "", err
	}
	return trackingID, statusToken, bodyEnc, bodyAlg, nil
}

func (s *Server) handleSecurityReportStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if s.db == nil {
		s.errorResponse(w, r, "Page not found", http.StatusNotFound)
		return
	}
	id, token := chi.URLParam(r, "tracking_id"), r.URL.Query().Get("token")
	if len(id) != 20 || !strings.HasPrefix(id, "sec_") || token == "" {
		s.errorResponse(w, r, "Page not found", http.StatusNotFound)
		return
	}
	tokenHash := sha256.Sum256([]byte(token))
	hashText := hex.EncodeToString(tokenHash[:])
	var storedHash, status, comment string
	var disclosure, closed, lastUsed sql.NullString
	err := s.db.QueryRow(`SELECT token_hash,status,researcher_comment,disclosure_date,closed_at,token_last_used FROM security_reports WHERE tracking_id=?`, id).Scan(&storedHash, &status, &comment, &disclosure, &closed, &lastUsed)
	if err != nil || !hmac.Equal([]byte(storedHash), []byte(hashText)) {
		s.errorResponse(w, r, "Page not found", http.StatusNotFound)
		return
	}
	today := time.Now().UTC().Format("2006-01-02")
	if lastUsed.Valid && lastUsed.String >= today {
		s.errorResponse(w, r, "Page not found", http.StatusNotFound)
		return
	}
	if closed.Valid {
		if parsed, err := time.Parse("2006-01-02 15:04:05", closed.String); err == nil && time.Since(parsed) > 30*24*time.Hour {
			s.errorResponse(w, r, "Page not found", http.StatusNotFound)
			return
		}
	}
	result, err := s.db.Exec(`UPDATE security_reports SET token_last_used=? WHERE tracking_id=? AND token_hash=? AND (token_last_used IS NULL OR token_last_used < ?)`, today, id, hashText, today)
	if err != nil {
		s.errorResponse(w, r, "Page not found", http.StatusNotFound)
		return
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		s.errorResponse(w, r, "Page not found", http.StatusNotFound)
		return
	}
	data := s.pageContext(w, r, "/server/security/report")
	data["TrackingID"], data["ReportStatus"], data["ResearcherComment"] = id, status, comment
	if disclosure.Valid {
		data["DisclosureDate"] = disclosure.String
	}
	s.renderTemplateResponse(w, r, "security-report-status.html", data, http.StatusOK)
}

func (s *Server) handleContactSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.config.Server.Pages.Contact.Enabled {
		s.errorResponse(w, r, "Page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	if err := r.ParseForm(); err != nil {
		contactLogBlocked(r, "shape")
		s.errorResponse(w, r, "Please check the submitted fields", http.StatusBadRequest)
		return
	}
	if !isContactAPI(r) && !validContactOrigin(r, s.reportOrigin(r)) {
		contactLogBlocked(r, "origin")
		contactStrike(r, s.config.Server.Pages.Contact.Antispam)
		s.errorResponse(w, r, "Please submit the form from this site", http.StatusBadRequest)
		return
	}
	if !isContactAPI(r) && !validCSRF(r) {
		contactLogBlocked(r, "shape")
		s.errorResponse(w, r, "Please check the submitted fields", http.StatusBadRequest)
		return
	}
	if r.FormValue("step") == "" {
		r.Form.Set("step", "submit")
	}
	kind := r.FormValue("type")
	if !containsString(offeredContactTypes(r), kind) && (kind != "security" || !s.validSecurityID(r.FormValue("security_id"), time.Now())) {
		s.errorResponse(w, r, "Invalid report type", http.StatusBadRequest)
		return
	}
	if !validContactShape(r, kind, isContactAPI(r)) {
		contactLogBlocked(r, "shape")
		s.errorResponse(w, r, "Please check the submitted fields", http.StatusBadRequest)
		return
	}
	if isContactBanned(r) {
		contactLogBlocked(r, "banned")
		retry := s.config.Server.Pages.Contact.Antispam.BanHours * 3600
		if retry < 1 {
			retry = 86400
		}
		w.Header().Set("Retry-After", fmt.Sprint(retry))
		s.errorResponse(w, r, "Too many submissions", http.StatusTooManyRequests)
		return
	}
	honeypot := strings.TrimSpace(r.FormValue("website")) != ""
	if honeypot {
		contactLogBlocked(r, "honeypot")
		contactStrike(r, s.config.Server.Pages.Contact.Antispam)
	}
	if r.FormValue("step") == "submit" && !s.validContactFormToken(r.FormValue("form_token")) {
		reason := "fill_time"
		if contactTokenWasUsed(r.FormValue("form_token")) {
			reason = "replay"
		}
		contactLogBlocked(r, reason)
		if reason == "replay" {
			contactStrike(r, s.config.Server.Pages.Contact.Antispam)
		}
		s.errorResponse(w, r, "Please wait a moment and submit again", http.StatusBadRequest)
		return
	}
	if r.FormValue("step") == "submit" && !s.verifyContactCaptcha(r) {
		contactLogBlocked(r, "captcha")
		contactStrike(r, s.config.Server.Pages.Contact.Antispam)
		s.errorResponse(w, r, "Captcha answer is incorrect", http.StatusBadRequest)
		return
	}
	values := map[string]string{}
	for _, key := range []string{"name", "email", "subject", "message", "reported_url", "reason", "copyrighted_work", "good_faith", "accuracy", "signature", "request_kind", "captcha_answer", "captcha_id", "form_token", "honeypot_flag", "security_id", "component", "component_other", "severity", "summary", "endpoint", "steps", "impact", "suggested_fix", "credit_preference", "credit_handle", "cve_requested", "disclosure_days", "coordinated_disclosure", "researcher_gpg", "app_version", "commit_hash", "timestamp", "request_user_agent"} {
		values[key] = strings.TrimSpace(r.FormValue(key))
	}
	if kind == "security" && r.FormValue("step") == "submit" {
		values["app_version"], values["commit_hash"], values["timestamp"], values["request_user_agent"] = s.version, s.commit, time.Now().UTC().Format(time.RFC3339), r.UserAgent()
	}
	if r.FormValue("step") == "edit" {
		data, err := s.contactEditData(w, r, kind, values)
		if err != nil {
			s.errorResponse(w, r, "Form unavailable", http.StatusServiceUnavailable)
			return
		}
		if isContactAPI(r) {
			s.jsonResponse(w, map[string]interface{}{"ok": true, "data": map[string]interface{}{"form_token": data.Token, "captcha": map[string]string{"provider": "simple", "captcha_id": data.Values["captcha_id"], "question": data.Challenge}}})
			return
		}
		s.renderTemplateResponse(w, r, "contact.html", data, http.StatusOK)
		return
	}
	if kind == "security" {
		values["subject"] = values["summary"]
		values["message"] = "Steps to reproduce:\n" + values["steps"] + "\n\nImpact:\n" + values["impact"] + "\n\nSuggested fix:\n" + values["suggested_fix"]
	}
	for key, value := range values {
		if !utf8.ValidString(value) || containsForbiddenContactChars(value) {
			s.errorResponse(w, r, "Please check the submitted fields", http.StatusBadRequest)
			return
		}
		if (key == "name" || key == "email" || key == "subject") && containsControlChars(value) {
			s.errorResponse(w, r, "Please check the submitted fields", http.StatusBadRequest)
			return
		}
	}
	if contactLength(values["name"]) == 0 || contactLength(values["name"]) > 100 || contactLength(values["email"]) == 0 || contactLength(values["email"]) > 254 {
		s.errorResponse(w, r, "Please check the required fields and length limits", http.StatusBadRequest)
		return
	}
	if kind == "security" {
		gpgMax := 16384
		if strings.HasPrefix(values["researcher_gpg"], "https://") {
			gpgMax = 2048
			parsed, parseErr := url.Parse(values["researcher_gpg"])
			if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" {
				s.errorResponse(w, r, "Researcher key URL must use HTTPS", http.StatusBadRequest)
				return
			}
		} else if values["researcher_gpg"] != "" && !strings.HasPrefix(values["researcher_gpg"], "-----BEGIN PGP PUBLIC KEY BLOCK-----") {
			s.errorResponse(w, r, "Enter an HTTPS key URL or an armored public key", http.StatusBadRequest)
			return
		}
		if values["component"] == "" || contactLength(values["component"]) > 255 || (values["component"] == "other" && strings.TrimSpace(values["component_other"]) == "") || contactLength(values["component_other"]) > 255 || !containsString([]string{"critical", "high", "medium", "low", "informational"}, values["severity"]) || values["summary"] == "" || contactLength(values["summary"]) > 200 || values["steps"] == "" || contactLength(values["steps"]) > 10000 || values["impact"] == "" || contactLength(values["impact"]) > 5000 || contactLength(values["suggested_fix"]) > 5000 || contactLength(values["endpoint"]) > 2048 || contactLength(values["researcher_gpg"]) > gpgMax {
			s.errorResponse(w, r, "Please check the required fields and length limits", http.StatusBadRequest)
			return
		}
	} else if contactLength(values["subject"]) == 0 || contactLength(values["subject"]) > 200 || contactLength(values["message"]) == 0 || contactLength(values["message"]) > 5000 {
		s.errorResponse(w, r, "Please check the required fields and length limits", http.StatusBadRequest)
		return
	}
	addr, err := mail.ParseAddress(values["email"])
	if err != nil || addr.Address != values["email"] || strings.ContainsAny(values["subject"], "\r\n") {
		s.errorResponse(w, r, "Please check the email and subject", http.StatusBadRequest)
		return
	}
	if kind == "abuse" || kind == "dmca" {
		u, e := url.ParseRequestURI(values["reported_url"])
		if e != nil || u.Host == "" || !contactHostAllowed(r, u.Hostname(), contactRequestFQDN(s, r)) {
			s.errorResponse(w, r, "Reported URL must be on this site", http.StatusBadRequest)
			return
		}
	}
	if kind == "abuse" && !containsString([]string{"illegal_content", "terms_violation", "harassment", "malware_phishing", "other"}, values["reason"]) {
		s.errorResponse(w, r, "Choose a report reason", http.StatusBadRequest)
		return
	}
	if kind == "privacy" && !containsString([]string{"access", "export", "deletion", "correction", "opt_out", "other"}, values["request_kind"]) {
		s.errorResponse(w, r, "Choose a request kind", http.StatusBadRequest)
		return
	}
	if kind == "dmca" && (values["copyrighted_work"] == "" || contactLength(values["copyrighted_work"]) > 2000 || values["good_faith"] != "on" || values["accuracy"] != "on" || values["signature"] == "" || contactLength(values["signature"]) > 100) {
		s.errorResponse(w, r, "Complete the DMCA statements", http.StatusBadRequest)
		return
	}
	if kind == "security" && (!s.validSecurityID(values["security_id"], time.Now()) || values["coordinated_disclosure"] != "on" || !containsString([]string{"no", "real_name", "handle", "anonymous"}, values["credit_preference"]) || (values["credit_preference"] == "handle" && values["credit_handle"] == "")) {
		logInvalidSecurityID(r, values["security_id"])
		s.errorResponse(w, r, "Complete the security report fields", http.StatusBadRequest)
		return
	}
	if kind != "security" && r.FormValue("step") == "submit" {
		if reason := s.validateContactContent(r, kind, values); reason != "" {
			contactStrike(r, s.config.Server.Pages.Contact.Antispam)
			contactLogBlocked(r, reason)
			s.errorResponse(w, r, "Your message could not be sent", http.StatusBadRequest)
			return
		}
	}
	if r.FormValue("step") == "submit" {
		if allowed, reason := s.allowContactRate(r, values["email"], false); !allowed {
			contactLogBlocked(r, reason)
			w.Header().Set("Retry-After", fmt.Sprint(int(contactWindow(s.config.Server.Pages.Contact.Antispam).Seconds())))
			s.errorResponse(w, r, "Too many submissions", http.StatusTooManyRequests)
			return
		}
	}
	if r.FormValue("step") == "submit" {
		values["captcha_id"], values["captcha_answer"] = "", ""
		if honeypot {
			values["honeypot_flag"] = "d"
		}
		values["form_token"] = s.issueContactFormToken()
		s.renderContactReview(w, r, kind, values, honeypot, "")
		return
	}
	if r.FormValue("step") != "confirm" || r.FormValue("confirmed") != "on" || !s.validReviewToken(r.FormValue("review_token"), values, kind) {
		contactLogBlocked(r, "review")
		s.renderContactReview(w, r, kind, values, values["honeypot_flag"] == "d", "Please read the details and confirm")
		return
	}
	if values["honeypot_flag"] == "d" {
		returnContactSuccess(w, r, s, values["form_token"])
		return
	}
	if allowed, reason := s.allowContactRate(r, values["email"], true); !allowed {
		contactLogBlocked(r, reason)
		w.Header().Set("Retry-After", fmt.Sprint(int(contactWindow(s.config.Server.Pages.Contact.Antispam).Seconds())))
		s.errorResponse(w, r, "Too many submissions", http.StatusTooManyRequests)
		return
	}
	if kind != "security" && s.isContactDuplicate(values) {
		contactLogBlocked(r, "duplicate")
		contactStrike(r, s.config.Server.Pages.Contact.Antispam)
		returnContactSuccess(w, r, s, values["form_token"])
		return
	}
	if kind == "security" {
		trackingID, statusToken, encryptedBody, bodyAlg, err := s.storeSecurityReport(values)
		if err != nil {
			s.errorResponse(w, r, "Report delivery is unavailable", http.StatusServiceUnavailable)
			return
		}
		component := values["component"]
		if component == "other" {
			component = values["component_other"]
		}
		component = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, component)
		if contactLength(component) > 255 {
			component = string([]rune(component)[:255])
		}
		log.Printf("security.report_received tracking_id=%q severity=%q component=%q", trackingID, values["severity"], component)
		recipient := s.config.Server.Contact.Security.Email
		if recipient == "" {
			recipient = s.config.Server.Contact.Admin.Email
		}
		if recipient != "" {
			kind := "security_encrypted"
			if bodyAlg == "pgp" {
				kind = "security_pgp"
			}
			_ = s.sendContactEmail(recipient, recipient, kind, "Report "+trackingID, base64.StdEncoding.EncodeToString(encryptedBody))
		}
		ackSent := false
		if statusToken != "" {
			statusURL := s.reportOrigin(r) + "/server/security/report/" + trackingID + "?token=" + url.QueryEscape(statusToken)
			if err := s.sendContactEmail(values["email"], recipient, "security_ack", "Your report was received", "Tracking ID: "+trackingID+"\nStatus: "+statusURL); err == nil {
				ackSent = true
			}
		}
		if isContactAPI(r) {
			message := "We received your report. Your tracking ID is " + trackingID + "."
			if ackSent {
				message += " A confirmation email is on its way."
			}
			s.jsonResponse(w, map[string]interface{}{"ok": true, "data": map[string]string{"tracking_id": trackingID, "message": message}})
			return
		}
		message := "Your tracking ID is " + trackingID + "."
		if ackSent {
			message += " A confirmation email is on its way."
		}
		lang := s.languageForRequest(w, r)
		data := contactPageData{Request: r, AppName: s.appName(), Lang: lang, Dir: i18n.Direction(lang), Theme: s.themeForRequest(r), Title: "Security report received", Description: "We received your report.", Success: message, Path: r.URL.Path, URLPrefix: s.requestURLPrefix(r), HasConsentCookie: s.hasConsentCookie(r)}
		s.renderTemplateResponse(w, r, "contact.html", data, http.StatusOK)
		return
	}
	recipient := s.contactRecipient(kind)
	if recipient == "" {
		s.errorResponse(w, r, "Report delivery is unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, err := mail.ParseAddress(recipient); err != nil {
		s.errorResponse(w, r, "Report delivery is unavailable", http.StatusServiceUnavailable)
		return
	}
	body := fmt.Sprintf("Name: %s\nEmail: %s\nType: %s\nReported URL: %s\nReason: %s\nRequest kind: %s\nCopyrighted work: %s\nGood faith: %s\nAccuracy: %s\nSignature: %s\nMessage:\n%s\n", values["name"], values["email"], kind, values["reported_url"], values["reason"], values["request_kind"], values["copyrighted_work"], values["good_faith"], values["accuracy"], values["signature"], values["message"])
	if err := s.sendContactEmail(recipient, values["email"], kind, values["subject"], body); err != nil {
		s.errorResponse(w, r, "Report delivery is unavailable", http.StatusServiceUnavailable)
		return
	}
	s.rememberContactDuplicate(values)
	returnContactSuccess(w, r, s, values["form_token"])
}

func (s *Server) validContactFormToken(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	var issued int64
	if _, err := fmt.Sscanf(parts[0], "%d", &issued); err != nil || len(parts[1]) != 22 {
		return false
	}
	age := time.Since(time.Unix(issued, 0))
	minimum := time.Duration(s.config.Server.Pages.Contact.Antispam.MinFillSeconds) * time.Second
	maximum := time.Duration(s.config.Server.Pages.Contact.Antispam.MaxAgeSeconds) * time.Second
	if maximum == 0 {
		maximum = 12 * time.Hour
	}
	want, err := hex.DecodeString(parts[2])
	if err != nil || age < minimum || age > maximum || !s.validContactMAC(want, "contact-form|"+parts[0]+"|"+parts[1], time.Now()) {
		return false
	}
	contactUsedMu.Lock()
	defer contactUsedMu.Unlock()
	for key, expiry := range contactUsedTokens {
		if time.Now().After(expiry) {
			delete(contactUsedTokens, key)
		}
	}
	if _, exists := contactUsedTokens[parts[1]]; exists {
		return false
	}
	if len(contactUsedTokens) >= 50000 {
		oldestKey := ""
		var oldest time.Time
		for key, expiry := range contactUsedTokens {
			if oldestKey == "" || expiry.Before(oldest) {
				oldestKey, oldest = key, expiry
			}
		}
		delete(contactUsedTokens, oldestKey)
	}
	maximum = time.Duration(s.config.Server.Pages.Contact.Antispam.MaxAgeSeconds) * time.Second
	if maximum <= 0 {
		maximum = 12 * time.Hour
	}
	contactUsedTokens[parts[1]] = time.Now().Add(maximum)
	return true
}

func contactTokenWasUsed(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	contactUsedMu.Lock()
	defer contactUsedMu.Unlock()
	_, exists := contactUsedTokens[parts[1]]
	return exists
}

func (s *Server) handleContactAPI(w http.ResponseWriter, r *http.Request) {
	if !s.config.Server.Pages.Contact.Enabled {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "Page not found")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	token := s.issueContactFormToken()
	captcha, err := s.contactCaptchaDescriptor(w, r)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "Captcha is unavailable")
		return
	}
	types := offeredContactTypes(r)
	if s.validSecurityID(r.URL.Query().Get("security_id"), time.Now()) {
		types = []string{"security"}
	}
	data := map[string]interface{}{"types": types, "security_url": "/server/security", "abuse_email": s.abuseContactEmail(), "form_token": token, "captcha": captcha, "fields": contactFieldDescriptors(types)}
	if containsString(types, "security") {
		data["hidden_fields"] = map[string]string{"security_id": r.URL.Query().Get("security_id"), "app_version": s.version, "commit_hash": s.commit, "timestamp": time.Now().UTC().Format(time.RFC3339), "request_user_agent": r.UserAgent()}
	}
	s.jsonResponse(w, map[string]interface{}{"ok": true, "data": data})
}

func (s *Server) contactCaptchaDescriptor(w http.ResponseWriter, r *http.Request) (map[string]interface{}, error) {
	captchaID, question, err := s.issueSimpleCaptcha()
	if err != nil {
		return nil, err
	}
	question = i18n.Translate(s.languageForRequest(w, r), "contact.captcha.solve") + " " + question
	fallback := map[string]interface{}{"provider": "simple", "captcha_id": captchaID, "question": question}
	provider := s.contactCaptchaProvider(r)
	if provider == "simple" {
		return fallback, nil
	}
	return map[string]interface{}{"provider": provider, "site_key": s.config.Server.Pages.Contact.CaptchaSiteKey, "fallback": fallback}, nil
}

func (s *Server) securityContactData(r *http.Request) map[string]interface{} {
	origin := s.reportOrigin(r)
	contacts := []map[string]string{{"type": "github", "value": "https://github.com/apimgr/countries/security/advisories/new"}}
	if s.config.Server.Pages.Contact.Enabled {
		contacts = append(contacts, map[string]string{"type": "contact_form", "value": origin + "/server/contact?security_id=" + securityIDFor(s.contactKey, time.Now().Unix()/172800)})
	}
	if email := strings.TrimSpace(s.config.Server.Contact.Security.Email); email != "" {
		if parsed, err := mail.ParseAddress(email); err == nil && parsed.Address == email {
			contacts = append(contacts, map[string]string{"type": "email", "value": "mailto:" + email})
		}
	}
	pgp := map[string]interface{}{"available": false, "url": nil, "fingerprint": nil}
	if _, fingerprint, err := s.readSecurityPublicKey(); err == nil {
		pgp = map[string]interface{}{"available": true, "url": s.BuildURL(r, "/.well-known/pgp-key.asc"), "fingerprint": fingerprint}
	}
	result := map[string]interface{}{"contacts": contacts, "expires": time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339), "pgp": pgp, "policy_url": origin + "/server/security/policy", "thanks_url": origin + "/server/security/thanks"}
	if s.config.Server.Pages.Contact.Enabled {
		result["report_url"] = origin + "/server/contact"
	}
	return result
}

func contactFieldDescriptors(types []string) []map[string]interface{} {
	fields := []map[string]interface{}{{"name": "type", "type": "select", "required": true, "max_length": nil, "values": types}, {"name": "name", "type": "text", "required": true, "max_length": 100}, {"name": "email", "type": "email", "required": true, "max_length": 254}}
	if containsString(types, "security") {
		return append(fields,
			map[string]interface{}{"name": "component", "type": "select", "required": true, "max_length": 255},
			map[string]interface{}{"name": "component_other", "type": "text", "required": false, "max_length": 255, "required_when": map[string]interface{}{"field": "component", "equals": "other"}},
			map[string]interface{}{"name": "severity", "type": "select", "required": true, "max_length": nil},
			map[string]interface{}{"name": "summary", "type": "text", "required": true, "max_length": 200},
			map[string]interface{}{"name": "endpoint", "type": "text", "required": false, "max_length": 2048},
			map[string]interface{}{"name": "steps", "type": "textarea", "required": true, "max_length": 10000},
			map[string]interface{}{"name": "impact", "type": "textarea", "required": true, "max_length": 5000},
			map[string]interface{}{"name": "suggested_fix", "type": "textarea", "required": false, "max_length": 5000},
			map[string]interface{}{"name": "researcher_gpg", "type": "textarea", "required": false, "max_length": 16384, "max_length_when": []map[string]interface{}{{"prefix": "https://", "max_length": 2048}}},
			map[string]interface{}{"name": "credit_preference", "type": "select", "required": true, "max_length": nil},
			map[string]interface{}{"name": "credit_handle", "type": "text", "required": false, "max_length": 100, "required_when": map[string]interface{}{"field": "credit_preference", "equals": "handle"}},
			map[string]interface{}{"name": "cve_requested", "type": "checkbox", "required": false, "max_length": nil},
			map[string]interface{}{"name": "disclosure_days", "type": "number", "required": false, "max_length": nil},
			map[string]interface{}{"name": "coordinated_disclosure", "type": "checkbox", "required": true, "max_length": nil},
			map[string]interface{}{"name": "security_id", "type": "hidden", "required": true, "max_length": 64})
	}
	return append(fields,
		map[string]interface{}{"name": "reported_url", "type": "url", "required": false, "max_length": 2048, "required_when": map[string]interface{}{"field": "type", "in": []string{"abuse", "dmca"}}},
		map[string]interface{}{"name": "reason", "type": "select", "required": false, "max_length": nil, "required_when": map[string]interface{}{"field": "type", "equals": "abuse"}},
		map[string]interface{}{"name": "copyrighted_work", "type": "textarea", "required": false, "max_length": 2000, "required_when": map[string]interface{}{"field": "type", "equals": "dmca"}},
		map[string]interface{}{"name": "good_faith", "type": "checkbox", "required": false, "max_length": nil, "required_when": map[string]interface{}{"field": "type", "equals": "dmca"}},
		map[string]interface{}{"name": "accuracy", "type": "checkbox", "required": false, "max_length": nil, "required_when": map[string]interface{}{"field": "type", "equals": "dmca"}},
		map[string]interface{}{"name": "signature", "type": "text", "required": false, "max_length": 100, "required_when": map[string]interface{}{"field": "type", "equals": "dmca"}},
		map[string]interface{}{"name": "request_kind", "type": "select", "required": false, "max_length": nil, "required_when": map[string]interface{}{"field": "type", "equals": "privacy"}},
		map[string]interface{}{"name": "subject", "type": "text", "required": true, "max_length": 200},
		map[string]interface{}{"name": "message", "type": "textarea", "required": true, "max_length": 5000})
}

func (s *Server) handleContactAPISubmit(w http.ResponseWriter, r *http.Request) {
	if !s.config.Server.Pages.Contact.Enabled {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "Page not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var fields map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid contact submission")
		return
	}
	values := make(url.Values, len(fields))
	for key, raw := range fields {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			var flag bool
			if json.Unmarshal(raw, &flag) == nil && flag {
				value = "on"
			} else if err := json.Unmarshal(raw, &flag); err == nil && !flag {
				value = ""
			} else {
				writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid contact submission")
				return
			}
		}
		values.Set(key, value)
	}
	if values.Get("step") == "" {
		values.Set("step", "submit")
	}
	inner := httptest.NewRequest(http.MethodPost, "/server/contact", strings.NewReader(values.Encode()))
	inner.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	inner = inner.WithContext(context.WithValue(inner.Context(), contactAPIContextKey{}, true))
	response := httptest.NewRecorder()
	s.handleContactSubmit(response, inner)
	for key, value := range response.Header() {
		for _, item := range value {
			w.Header().Add(key, item)
		}
	}
	if response.Code >= http.StatusBadRequest {
		field, message, code := "form", "Please check the submitted fields", "VALIDATION_FAILED"
		body := strings.ToLower(response.Body.String())
		switch {
		case strings.Contains(body, "captcha"):
			field, message = "captcha", "Please solve the challenge and try again"
		case strings.Contains(body, "wait a moment") || strings.Contains(body, "form expired"):
			field, message = "form_token", "Please wait a moment and submit again"
		case strings.Contains(body, "too many submissions"):
			code, message = "RATE_LIMITED", "Too many submissions"
		}
		captcha, _ := s.contactCaptchaDescriptor(w, r)
		writeJSON(w, response.Code, map[string]interface{}{"ok": false, "error": code, "message": message, "data": map[string]interface{}{"field": field, "form_token": s.issueContactFormToken(), "captcha": captcha}})
		return
	}
	w.WriteHeader(response.Code)
	_, _ = w.Write(response.Body.Bytes())
}

func wrapBase64Lines(encoded string, width int) []string {
	if width < 1 {
		return []string{encoded}
	}
	lines := make([]string, 0, (len(encoded)+width-1)/width)
	for len(encoded) > width {
		lines = append(lines, encoded[:width])
		encoded = encoded[width:]
	}
	if encoded != "" {
		lines = append(lines, encoded)
	}
	return lines
}

func (s *Server) sendContactEmail(recipient, replyTo, kind, subject, body string) (resultErr error) {
	cfg := s.config.Server.Notifications.Email.SMTP
	if !s.config.Server.Notifications.Email.Enabled || cfg.Host == "" {
		return fmt.Errorf("SMTP is not configured")
	}
	fromName := s.config.Server.Notifications.Email.From.Name
	if fromName == "" {
		fromName = s.config.Server.Branding.Title
	}
	if fromName == "" {
		fromName = "Countries"
	}
	fromEmail := s.config.Server.Notifications.Email.From.Email
	if fromEmail == "" {
		fqdn := strings.TrimSpace(s.config.Server.FQDN)
		if fqdn == "" {
			fqdn = "localhost"
		}
		fromEmail = "no-reply@" + fqdn
	}
	if kind != "security_encrypted" {
		prefix := "[Countries contact]"
		if strings.HasPrefix(kind, "security") {
			prefix = "[Countries security]"
		}
		return (notifications.Sender{Config: notifications.SMTPConfig{Host: cfg.Host, Port: cfg.Port, Username: cfg.Username, Password: cfg.Password, TLS: cfg.TLS}}).Send(notifications.Message{
			FromName: fromName, FromEmail: fromEmail, To: recipient, ReplyTo: replyTo,
			Subject: prefix + " " + subject, Text: body,
			HTML: "<!doctype html><html><body><pre>" + html.EscapeString(body) + "</pre></body></html>", FQDN: s.config.Server.FQDN,
		})
	}
	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	if host == "" {
		host = strings.TrimSpace(cfg.Host)
	}
	port := strings.TrimSpace(os.Getenv("SMTP_PORT"))
	if port == "" && cfg.Port > 0 {
		port = fmt.Sprint(cfg.Port)
	}
	if port == "" {
		if host == "" {
			return fmt.Errorf("SMTP is not configured")
		} else {
			port = "587"
		}
	}
	username := strings.TrimSpace(os.Getenv("SMTP_USERNAME"))
	if username == "" {
		username = cfg.Username
	}
	password := os.Getenv("SMTP_PASSWORD")
	if password == "" {
		password = cfg.Password
	}
	if parsed, err := mail.ParseAddress(fromEmail); err != nil || parsed.Address != fromEmail {
		return fmt.Errorf("invalid sender")
	}
	if parsed, err := mail.ParseAddress(replyTo); err != nil || parsed.Address != replyTo {
		return fmt.Errorf("invalid reply address")
	}
	from := (&mail.Address{Name: fromName, Address: fromEmail}).String()
	prefix := "[Countries contact]"
	if strings.HasPrefix(kind, "security") {
		prefix = "[Countries security]"
	}
	encodedSubject := mime.QEncoding.Encode("utf-8", subject)
	messageText := fmt.Sprintf("From: %s\r\nTo: %s\r\nReply-To: %s\r\nSubject: %s %s\r\nMIME-Version: 1.0\r\n", from, recipient, replyTo, prefix, encodedSubject)
	switch kind {
	case "security_pgp":
		boundary := "countries-" + randomContactNonce()
		wrapped := wrapBase64Lines(body, 76)
		messageText += fmt.Sprintf("Content-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=\"%s\"\r\n\r\n--%s\r\nContent-Type: application/pgp-encrypted\r\n\r\nVersion: 1\r\n--%s\r\nContent-Type: application/octet-stream; name=\"encrypted.asc\"\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: inline; filename=\"encrypted.asc\"\r\n\r\n%s\r\n--%s--\r\n", boundary, boundary, boundary, strings.Join(wrapped, "\r\n"), boundary)
	case "security_encrypted":
		boundary := "countries-" + randomContactNonce()
		wrapped := wrapBase64Lines(body, 76)
		messageText += fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n--%s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nThe attached vulnerability report is encrypted with AES-256-GCM using the deployment's configured encryption key.\r\n--%s\r\nContent-Type: application/octet-stream; name=\"security-report.enc\"\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=\"security-report.enc\"\r\n\r\n%s\r\n--%s--\r\n", boundary, boundary, boundary, strings.Join(wrapped, "\r\n"), boundary)
	default:
		messageText += fmt.Sprintf("Content-Type: text/plain; charset=UTF-8\r\n\r\n%s", body)
	}
	message := []byte(messageText)
	tlsMode := strings.ToLower(strings.TrimSpace(os.Getenv("SMTP_TLS")))
	if tlsMode == "" {
		tlsMode = strings.ToLower(cfg.TLS)
	}
	if tlsMode == "" {
		tlsMode = "auto"
	}
	var client *smtp.Client
	var err error
	if tlsMode == "tls" {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", net.JoinHostPort(host, port), &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return err
		}
		client, err = smtp.NewClient(conn, host)
		if err != nil {
			_ = conn.Close()
			return err
		}
	} else {
		client, err = smtp.Dial(net.JoinHostPort(host, port))
		if err != nil {
			return err
		}
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close SMTP connection: %w", closeErr))
		}
	}()
	if tlsMode == "starttls" || tlsMode == "auto" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		} else if tlsMode == "starttls" {
			return fmt.Errorf("SMTP server does not support STARTTLS")
		}
	}
	if tlsMode != "tls" && tlsMode != "starttls" && tlsMode != "auto" && tlsMode != "none" {
		return fmt.Errorf("invalid SMTP TLS mode")
	}
	if username != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("SMTP authentication unavailable")
		}
		if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
			return err
		}
	}
	if err := client.Mail(fromEmail); err != nil {
		return err
	}
	if err := client.Rcpt(recipient); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(message); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if err := client.Quit(); err != nil {
		return err
	}
	_ = kind
	return nil
}

func validContactOrigin(r *http.Request, origin string) bool {
	if value := r.Header.Get("Sec-Fetch-Site"); value != "" && value != "same-origin" {
		return false
	}
	check := strings.TrimSpace(r.Header.Get("Origin"))
	if check == "" {
		if ref := strings.TrimSpace(r.Header.Get("Referer")); ref != "" {
			parsed, err := url.Parse(ref)
			if err != nil {
				return false
			}
			check = parsed.Scheme + "://" + parsed.Host
		}
	}
	return check != "" && strings.EqualFold(strings.TrimSuffix(check, "/"), strings.TrimSuffix(origin, "/"))
}
