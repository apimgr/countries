package server

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/testutil"
)

// Contact tests share process-wide replay, captcha, rate, strike, ban, and
// duplicate stores. Keep them isolated because those stores model per-node state.
func isolateContactState(t *testing.T) {
	t.Helper()
	contactUsedMu.Lock()
	used, captcha := contactUsedTokens, fallbackCaptchaAnswers
	contactUsedTokens, fallbackCaptchaAnswers = map[string]time.Time{}, map[string]string{}
	contactUsedMu.Unlock()
	contactRateMu.Lock()
	rate, strikes, bans, duplicates := contactRateEvents, contactStrikes, contactBans, contactDuplicates
	contactRateEvents, contactStrikes, contactBans, contactDuplicates = map[string][]time.Time{}, map[string][]time.Time{}, map[string]time.Time{}, map[string]time.Time{}
	contactRateMu.Unlock()
	t.Cleanup(func() {
		contactUsedMu.Lock()
		contactUsedTokens, fallbackCaptchaAnswers = used, captcha
		contactUsedMu.Unlock()
		contactRateMu.Lock()
		contactRateEvents, contactStrikes, contactBans, contactDuplicates = rate, strikes, bans, duplicates
		contactRateMu.Unlock()
	})
}

func TestSecurityIDAcceptsPreviousInstallationSecretDuringGrace(t *testing.T) {
	now := time.Unix(172800*24000+120, 0)
	currentKey := []byte(strings.Repeat("c", 32))
	previousKey := []byte(strings.Repeat("p", 32))
	s := &Server{
		contactKey:           currentKey,
		contactPreviousKey:   previousKey,
		contactPreviousUntil: now.Add(7 * 24 * time.Hour),
	}
	previousID := securityIDFor(previousKey, now.Unix()/172800)
	if !s.validSecurityID(previousID, now) {
		t.Fatal("valid previous-key security ID was rejected during its grace period")
	}
	if s.validSecurityID(previousID, s.contactPreviousUntil) {
		t.Fatal("previous-key security ID was accepted at or after expiry")
	}
}

func TestContactSimpleCaptchaIsSingleUseAndProviderFailsClosed(t *testing.T) {
	isolateContactState(t)
	s := newTestServer(t)
	id, question, err := s.issueSimpleCaptcha()
	if err != nil || len(id) != 32 || question == "" {
		t.Fatalf("issueSimpleCaptcha() = (%q, %q, %v), want challenge", id, question, err)
	}
	answer := fallbackCaptchaAnswers[id]
	if !s.verifySimpleCaptcha(id, answer) {
		t.Fatal("captcha rejected its issued answer")
	}
	if s.verifySimpleCaptcha(id, answer) {
		t.Fatal("captcha accepted a replayed answer")
	}

	cfg := config.DefaultConfig()
	cfg.Server.Pages.Contact.Captcha = "hcaptcha"
	cfg.Server.Pages.Contact.CaptchaSecretKey = ""
	s = newTestServerWithConfig(t, cfg)
	req := httptest.NewRequest("POST", "/server/contact", nil)
	req.Form = url.Values{"captcha_response": {"provider-token"}}
	if s.contactCaptchaProvider(req) != "hcaptcha" {
		t.Fatal("configured provider was not selected")
	}
	if s.verifyContactCaptcha(req) {
		t.Fatal("provider captcha passed without a configured secret")
	}
	req.Host = "hidden-service.onion"
	if got := s.contactCaptchaProvider(req); got != "simple" {
		t.Fatalf("onion captcha provider = %q, want simple", got)
	}
}

func signedFormToken(s *Server, issued time.Time, nonce string) string {
	stamp := fmt.Sprint(issued.Unix())
	mac := hex.EncodeToString(hmacBytes(s.contactKey, "contact-form|"+stamp+"|"+nonce))
	return stamp + "." + nonce + "." + mac
}

func TestContactFormTokenAgeIntegrityAndReplay(t *testing.T) {
	isolateContactState(t)
	cfg := config.DefaultConfig()
	cfg.Server.Pages.Contact.Antispam.MinFillSeconds = 2
	cfg.Server.Pages.Contact.Antispam.MaxAgeSeconds = 60
	s := newTestServerWithConfig(t, cfg)
	if s.validContactFormToken(s.issueContactFormToken()) {
		t.Fatal("form token younger than min_fill_seconds was accepted")
	}
	old := time.Now().Add(-10 * time.Second)
	token := signedFormToken(s, old, "abcdefghijklmnopqrstuv")
	if !s.validContactFormToken(token) {
		t.Fatal("valid aged form token was rejected")
	}
	if !contactTokenWasUsed(token) || s.validContactFormToken(token) {
		t.Fatal("form token replay was not rejected")
	}
	if s.validContactFormToken(signedFormToken(s, old, "abcdefghijklmnopqrstuw") + "x") {
		t.Fatal("tampered form token was accepted")
	}
	expired := signedFormToken(s, time.Now().Add(-2*time.Minute), "abcdefghijklmnopqrstux")
	if s.validContactFormToken(expired) {
		t.Fatal("expired form token was accepted")
	}
}

func TestContactReviewTokenBindsFieldsAgeAndUse(t *testing.T) {
	isolateContactState(t)
	cfg := config.DefaultConfig()
	cfg.Server.Pages.Contact.Antispam.ReviewMinSeconds = 0
	cfg.Server.Pages.Contact.Antispam.ReviewMaxSeconds = 60
	s := newTestServerWithConfig(t, cfg)
	values := map[string]string{"name": "Reporter", "email": "r@example.test", "subject": "Issue", "message": "Details"}
	s.config.Server.Pages.Contact.Antispam.ReviewMinSeconds = 2
	tooYoung := s.issueReviewToken(values, "abuse", false)
	if s.validReviewToken(tooYoung, values, "abuse") {
		t.Fatal("review token younger than review_min_seconds was accepted")
	}
	s.config.Server.Pages.Contact.Antispam.ReviewMinSeconds = 0
	token := s.issueReviewToken(values, "abuse", false)
	if !s.validReviewToken(token, values, "abuse") {
		t.Fatal("valid review token was rejected")
	}
	if s.validReviewToken(token, values, "abuse") {
		t.Fatal("review token replay was accepted")
	}
	values["message"] = "Changed after review"
	if s.validReviewToken(s.issueReviewToken(map[string]string{"name": "Reporter", "email": "r@example.test", "subject": "Issue", "message": "Details"}, "abuse", false), values, "abuse") {
		t.Fatal("review token accepted changed fields")
	}
	values["message"] = "Details"
	changed := strings.Split(s.issueReviewToken(values, "abuse", false), ".")
	changed[3] = strings.Repeat("0", len(changed[3]))
	if s.validReviewToken(strings.Join(changed, "."), values, "abuse") {
		t.Fatal("review token accepted a modified signature")
	}
}

func TestContactShapeOriginHostAndContentValidation(t *testing.T) {
	s := newTestServer(t)
	form := url.Values{"type": {"abuse"}, "name": {"Reporter"}, "email": {"r@example.test"}, "reported_url": {"https://countries.example.test/country/US"}, "reason": {"terms_violation"}, "subject": {"Report"}, "message": {"Details"}, "step": {"submit"}, "form_token": {"token"}}
	req := httptest.NewRequest("POST", "/server/contact", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatal(err)
	}
	if !validContactShape(req, "abuse", false) {
		t.Fatal("valid abuse form shape was rejected")
	}
	req.Form.Set("unexpected", "value")
	if validContactShape(req, "abuse", false) {
		t.Fatal("unknown field was accepted")
	}
	for name, headers := range map[string]map[string]string{
		"missing origin metadata": {},
		"cross origin":            {"Origin": "https://attacker.test"},
		"cross site fetch":        {"Origin": "https://countries.example.test", "Sec-Fetch-Site": "cross-site"},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/server/contact", nil)
			for key, value := range headers {
				r.Header.Set(key, value)
			}
			if validContactOrigin(r, "https://countries.example.test") {
				t.Fatal("origin check accepted request")
			}
		})
	}
	referer := httptest.NewRequest("POST", "/server/contact", nil)
	referer.Header.Set("Referer", "https://countries.example.test/server/contact")
	if !validContactOrigin(referer, "https://countries.example.test") {
		t.Fatal("same-site Referer was rejected")
	}
	if !contactHostAllowed(httptest.NewRequest("GET", "/", nil), "countries.example.test", "countries.example.test") || contactHostAllowed(httptest.NewRequest("GET", "/", nil), "attacker.test", "countries.example.test") {
		t.Fatal("contact host allowlist did not distinguish first-party from external host")
	}
	if got := s.validateContactContent(httptest.NewRequest("POST", "/", nil), "privacy", map[string]string{"email": "r@example.test", "subject": "hello", "message": "<a href='https://evil.test'>link</a>"}); got != "markup" {
		t.Fatalf("markup rejection reason = %q, want markup", got)
	}
	if got := s.validateContactContent(httptest.NewRequest("POST", "/", nil), "privacy", map[string]string{"email": "r@example.test", "subject": "hello", "message": "see https://attacker.test/path"}); got != "content" {
		t.Fatalf("external link rejection reason = %q, want content", got)
	}
}

func TestContactRateStrikesBansAndDuplicateNormalization(t *testing.T) {
	isolateContactState(t)
	cfg := config.DefaultConfig()
	cfg.Server.Pages.Contact.Antispam.PerIP = 1
	cfg.Server.Pages.Contact.Antispam.PerEmail = 0
	cfg.Server.Pages.Contact.Antispam.Global = 0
	cfg.Server.Pages.Contact.Antispam.Strikes = 3
	cfg.Server.Pages.Contact.Antispam.BanHours = 1
	s := newTestServerWithConfig(t, cfg)
	r := httptest.NewRequest("POST", "/server/contact", nil)
	r.RemoteAddr = "192.0.2.25:1234"
	if ok, reason := s.allowContactRate(r, "A@example.test", false); !ok || reason != "" {
		t.Fatalf("rate check without counting = (%v, %q), want allowed", ok, reason)
	}
	if ok, _ := s.allowContactRate(r, "A@example.test", false); !ok {
		t.Fatal("non-counting rate check changed the limit")
	}
	if ok, _ := s.allowContactRate(r, "A@example.test", true); !ok {
		t.Fatal("first counted request was rejected")
	}
	if ok, reason := s.allowContactRate(r, "A@example.test", true); ok || reason != "rate_ip" {
		t.Fatalf("over-limit request = (%v, %q), want rate_ip rejection", ok, reason)
	}
	strikeCfg := cfg.Server.Pages.Contact.Antispam
	for i := 0; i < 3; i++ {
		contactStrike(r, strikeCfg)
	}
	if !isContactBanned(r) {
		t.Fatal("third strike did not ban the client")
	}
	onion := httptest.NewRequest("POST", "/", nil)
	onion.Host = "site.onion"
	onion.RemoteAddr = r.RemoteAddr
	if ok, reason := s.allowContactRate(onion, "B@example.test", true); !ok || reason != "" {
		t.Fatalf("onion request was incorrectly limited by shared client IP: (%v, %q)", ok, reason)
	}
	if isContactBanned(onion) {
		t.Fatal("onion client was subject to IP strike ban")
	}
	i2p := httptest.NewRequest("POST", "/", nil)
	i2p.Host = "site.b32.i2p"
	i2p.RemoteAddr = r.RemoteAddr
	if isContactBanned(i2p) {
		t.Fatal("I2P client was subject to IP strike ban")
	}

	values := map[string]string{"email": " R@Example.test ", "subject": "A   subject", "message": "same\nmessage"}
	s.config.Server.Pages.Contact.Antispam.DuplicateWindowHours = 1
	if s.isContactDuplicate(values) {
		t.Fatal("unseen contact was considered a duplicate")
	}
	s.rememberContactDuplicate(values)
	variant := map[string]string{"email": "r@example.test", "subject": "a subject", "message": " same message "}
	if !s.isContactDuplicate(variant) {
		t.Fatal("normalized duplicate was not detected")
	}
}

func TestSecurityDataEncryptionAndStorageValidation(t *testing.T) {
	s := newTestServer(t)
	if _, err := s.encryptSecurityData([]byte("secret")); err == nil {
		t.Fatal("encryption succeeded without a valid key")
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	s.config.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(key)
	plain := []byte(`{"email":"researcher@example.test"}`)
	ciphertext, err := s.encryptSecurityData(plain)
	if err != nil {
		t.Fatalf("encryptSecurityData() error = %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) <= gcm.NonceSize() {
		t.Fatal("encrypted data omitted nonce or ciphertext")
	}
	opened, err := gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
	if err != nil || string(opened) != string(plain) {
		t.Fatalf("decrypted payload = %q, error = %v", opened, err)
	}
	if _, _, _, _, err := s.storeSecurityReport(map[string]string{}); err == nil {
		t.Fatal("security report storage succeeded without a database")
	}
	db, err := database.Open(filepath.Join(testutil.TempDir(t), "security-report.db"))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.db = db
	s.config.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(key)
	s.config.ConfigDir = testutil.TempDir(t)
	s.contactKey = []byte("test installation contact key")
	id := securityIDFor(s.contactKey, time.Now().Unix()/172800)
	values := map[string]string{"name": "Researcher", "email": "researcher@example.test", "component": "api", "severity": "high", "summary": "Private report", "steps": "Reproduction details", "impact": "Private impact", "credit_preference": "anonymous", "security_id": id}
	trackingID, statusToken, stored, algorithm, err := s.storeSecurityReport(values)
	if err != nil {
		t.Fatalf("storeSecurityReport() error = %v", err)
	}
	if len(trackingID) != 20 || !strings.HasPrefix(trackingID, "sec_") || statusToken == "" || algorithm != "aes-256-gcm" || strings.Contains(string(stored), "Private report") {
		t.Fatalf("stored report metadata = (%q, token present=%v, alg=%q, body=%q)", trackingID, statusToken != "", algorithm, stored)
	}
}

func TestExplicitSecurityReportRecipientDoesNotUseRoleFallback(t *testing.T) {
	s := newTestServer(t)
	s.config.Server.Contact.Admin.Email = "admin@example.test"
	if got := s.explicitSecurityReportRecipient(); got != "" {
		t.Fatalf("explicit security recipient = %q, want empty", got)
	}
	s.config.Server.Contact.Security.Email = "security@example.test"
	if got := s.explicitSecurityReportRecipient(); got != "security@example.test" {
		t.Fatalf("explicit security recipient = %q", got)
	}
	s.config.Server.Contact.Security.Email = "invalid address"
	if got := s.explicitSecurityReportRecipient(); got != "" {
		t.Fatalf("invalid explicit security recipient = %q, want empty", got)
	}
}
