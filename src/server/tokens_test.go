package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"github.com/apimgr/countries/src/testutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
	"github.com/apimgr/countries/src/database"
)

func newTokenTestServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	db, err := database.Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("database.Open(): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.DefaultConfig()
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	service, err := countries.NewService([]byte(fixtureJSON))
	if err != nil {
		t.Fatalf("countries.NewService(): %v", err)
	}
	return New(service, cfg, "0.0.0.0", "8080", "test-version", "2026-11-22", "abc123", db), db
}

func TestSetTokenRevocationClearSiteData(t *testing.T) {
	cfg := config.DefaultConfig()
	s := &Server{config: cfg}
	response := httptest.NewRecorder()
	s.setTokenRevocationClearSiteData(response)
	if got := response.Header().Get("Clear-Site-Data"); got != `"cache", "cookies", "storage"` {
		t.Fatalf("default Clear-Site-Data=%q", got)
	}

	cfg.Web.Headers.ClearSiteData.ExecutionContexts = true
	response = httptest.NewRecorder()
	s.setTokenRevocationClearSiteData(response)
	if got := response.Header().Get("Clear-Site-Data"); got != `"cache", "cookies", "storage", "executionContexts"` {
		t.Fatalf("execution-context Clear-Site-Data=%q", got)
	}

	cfg.Web.Headers.ClearSiteData.OnTokenRevocation = false
	response = httptest.NewRecorder()
	s.setTokenRevocationClearSiteData(response)
	if got := response.Header().Get("Clear-Site-Data"); got != "" {
		t.Fatalf("disabled revocation Clear-Site-Data=%q", got)
	}
}

func TestOwnerTokenPageIssueRestoreAndMasking(t *testing.T) {
	s, _ := newTokenTestServer(t)
	page := httptest.NewRecorder()
	s.router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "restore-token") || strings.Contains(page.Body.String(), "linked-title") {
		t.Fatalf("unauthenticated token page status=%d body=%q", page.Code, page.Body.String())
	}
	var csrf string
	for _, cookie := range page.Result().Cookies() {
		if cookie.Name == "csrf_token" {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("tokens page did not issue CSRF cookie")
	}
	form := url.Values{"csrf_token": []string{csrf}}
	request := httptest.NewRequest(http.MethodPost, "/server/preferences/token/create", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "csrf_token", Value: csrf})
	created := httptest.NewRecorder()
	s.router.ServeHTTP(created, request)
	if created.Code != http.StatusCreated || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("token issue status=%d cache-control=%q body=%q", created.Code, created.Header().Get("Cache-Control"), created.Body.String())
	}
	if !strings.Contains(created.Body.String(), "own_") {
		t.Fatalf("issue response did not reveal token on no-store page: %q", created.Body.String())
	}
	var ownerCookie *http.Cookie
	for _, cookie := range created.Result().Cookies() {
		if cookie.Name == ownerTokenCookieName {
			ownerCookie = cookie
		}
	}
	if ownerCookie == nil || !ownerCookie.HttpOnly || ownerCookie.SameSite != http.SameSiteStrictMode || ownerCookie.Path != "/" || ownerCookie.Value == "" {
		t.Fatalf("owner cookie = %+v, want HttpOnly SameSite=Strict Path=/", ownerCookie)
	}
	if strings.Contains(created.Body.String(), ownerCookie.Value) == false {
		t.Fatal("new primary token was not shown in the issue response")
	}

	request = httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	request.AddCookie(ownerCookie)
	masked := httptest.NewRecorder()
	s.router.ServeHTTP(masked, request)
	if masked.Code != http.StatusOK || strings.Contains(masked.Body.String(), ownerCookie.Value) || !strings.Contains(masked.Body.String(), "••") {
		t.Fatalf("token page did not mask saved token: status=%d body=%q", masked.Code, masked.Body.String())
	}

	restorePage := httptest.NewRecorder()
	s.router.ServeHTTP(restorePage, httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil))
	var restoreCSRF string
	for _, cookie := range restorePage.Result().Cookies() {
		if cookie.Name == "csrf_token" {
			restoreCSRF = cookie.Value
		}
	}
	restoreForm := url.Values{"csrf_token": []string{restoreCSRF}, "primary_token": []string{ownerCookie.Value}}
	restoreRequest := httptest.NewRequest(http.MethodPost, "/server/preferences/token/restore", strings.NewReader(restoreForm.Encode()))
	restoreRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	restoreRequest.AddCookie(&http.Cookie{Name: "csrf_token", Value: restoreCSRF})
	restored := httptest.NewRecorder()
	s.router.ServeHTTP(restored, restoreRequest)
	if restored.Code != http.StatusSeeOther || restored.Header().Get("Location") != "/server/preferences/tokens?status=restored" {
		t.Fatalf("restore response status=%d location=%q", restored.Code, restored.Header().Get("Location"))
	}
	if cookie := findCookie(restored.Result().Cookies(), ownerTokenCookieName); cookie == nil || cookie.Value != ownerCookie.Value {
		t.Fatalf("restore owner cookie = %+v", cookie)
	}
}

func TestOwnerTokenAPIRoutesEnforcePrimaryAndHideRawValues(t *testing.T) {
	s, _ := newTokenTestServer(t)

	issue := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens/primary", nil)
	issue.Header.Set("Accept", "application/json")
	issued := httptest.NewRecorder()
	s.router.ServeHTTP(issued, issue)
	var primary string
	if issued.Code != http.StatusCreated {
		t.Fatalf("primary issue status=%d body=%q", issued.Code, issued.Body.String())
	}
	var issueBody struct {
		Data struct {
			OwnerToken string `json:"owner_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(issued.Body.Bytes(), &issueBody); err != nil || issueBody.Data.OwnerToken == "" {
		t.Fatalf("decode primary issue response: body=%q error=%v", issued.Body.String(), err)
	}
	primary = issueBody.Data.OwnerToken

	linkedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens", bytes.NewBufferString(`{"name":"build agent","expiry":"7d"}`))
	linkedRequest.Header.Set("Content-Type", "application/json")
	linkedRequest.Header.Set("Accept", "application/json")
	linkedRequest.Header.Set("Authorization", "Bearer "+primary)
	linkedResponse := httptest.NewRecorder()
	s.router.ServeHTTP(linkedResponse, linkedRequest)
	if linkedResponse.Code != http.StatusCreated {
		t.Fatalf("linked issue status=%d body=%q", linkedResponse.Code, linkedResponse.Body.String())
	}
	var linkedBody struct {
		Data struct {
			Token string `json:"token"`
			ID    int64  `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(linkedResponse.Body.Bytes(), &linkedBody); err != nil || linkedBody.Data.Token == "" || linkedBody.Data.ID == 0 {
		t.Fatalf("decode linked issue response: body=%q error=%v", linkedResponse.Body.String(), err)
	}
	linked := linkedBody.Data.Token

	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/tokens", nil)
	listRequest.Header.Set("Accept", "application/json")
	listRequest.Header.Set("Authorization", "Bearer "+primary)
	listed := httptest.NewRecorder()
	s.router.ServeHTTP(listed, listRequest)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), primary) || strings.Contains(listed.Body.String(), linked) || strings.Contains(listed.Body.String(), "token_enc") {
		t.Fatalf("token list leaked credential material: status=%d body=%q", listed.Code, listed.Body.String())
	}

	linkedManagement := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens/"+strconv.FormatInt(linkedBody.Data.ID, 10)+"/reveal", nil)
	linkedManagement.Header.Set("Accept", "application/json")
	linkedManagement.Header.Set("Authorization", "Bearer "+linked)
	denied := httptest.NewRecorder()
	s.router.ServeHTTP(denied, linkedManagement)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("linked token management status=%d body=%q, want 403", denied.Code, denied.Body.String())
	}

	rotateRequest := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens/primary/rotate", bytes.NewBufferString(`{"confirm":true}`))
	rotateRequest.Header.Set("Content-Type", "application/json")
	rotateRequest.Header.Set("Accept", "application/json")
	rotateRequest.Header.Set("Authorization", "Bearer "+primary)
	rotated := httptest.NewRecorder()
	s.router.ServeHTTP(rotated, rotateRequest)
	if rotated.Code != http.StatusOK {
		t.Fatalf("primary rotation status=%d body=%q", rotated.Code, rotated.Body.String())
	}
	var rotatedBody struct {
		Data struct {
			OwnerToken string `json:"owner_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rotated.Body.Bytes(), &rotatedBody); err != nil || rotatedBody.Data.OwnerToken == "" || rotatedBody.Data.OwnerToken == primary {
		t.Fatalf("decode rotated token response: body=%q error=%v", rotated.Body.String(), err)
	}
	invalidOldToken := httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/tokens", nil)
	invalidOldToken.Header.Set("Accept", "application/json")
	invalidOldToken.Header.Set("Authorization", "Bearer "+primary)
	invalidResponse := httptest.NewRecorder()
	s.router.ServeHTTP(invalidResponse, invalidOldToken)
	if invalidResponse.Code != http.StatusUnauthorized {
		t.Fatalf("old primary token status=%d body=%q, want 401", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestOwnerTokenIssueRejectsValidOperatorBearer(t *testing.T) {
	s, _ := newTokenTestServer(t)
	s.config.Server.Token = "adm_0123456789abcdefghijklmnopqrstuvwxyz"
	request := httptest.NewRequest(http.MethodPost, api.APIBasePath()+"/server/preferences/tokens/primary", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+s.config.Server.Token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	recorder := httptest.NewRecorder()
	s.router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("issue with valid operator token status=%d body=%q, want 409", recorder.Code, recorder.Body.String())
	}
}

func TestPrimaryTokenRevocationWebRequiresLastFourAndDeletesOwner(t *testing.T) {
	s, db := newTokenTestServer(t)
	primary, raw, err := database.CreateOwnerToken(context.Background(), db, s.config.Server.Security.EncryptionKey)
	if err != nil {
		t.Fatalf("CreateOwnerToken(): %v", err)
	}
	if _, _, err := database.CreateLinkedOwnerToken(context.Background(), db, primary.OwnerID, "test client", nil, 25, s.config.Server.Security.EncryptionKey, time.Now().UTC()); err != nil {
		t.Fatalf("CreateLinkedOwnerToken(): %v", err)
	}
	pageRequest := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	pageRequest.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: raw})
	page := httptest.NewRecorder()
	s.router.ServeHTTP(page, pageRequest)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "token-revoke-primary") || !strings.Contains(page.Body.String(), primary.Last4) {
		t.Fatalf("primary revoke card missing: status=%d body=%q", page.Code, page.Body.String())
	}
	csrf := findCookie(page.Result().Cookies(), "csrf_token")
	if csrf == nil {
		t.Fatal("tokens page did not issue CSRF cookie")
	}
	post := func(confirm string) *httptest.ResponseRecorder {
		form := url.Values{"csrf_token": []string{csrf.Value}, "confirm": []string{confirm}}
		request := httptest.NewRequest(http.MethodPost, "/server/preferences/token/revoke", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(csrf)
		request.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: raw})
		response := httptest.NewRecorder()
		s.router.ServeHTTP(response, request)
		return response
	}
	wrong := post("nope")
	if wrong.Code != http.StatusBadRequest {
		t.Fatalf("wrong last-four status=%d body=%q, want 400", wrong.Code, wrong.Body.String())
	}
	if _, err := database.LookupOwnerToken(context.Background(), db, raw, time.Now()); err != nil {
		t.Fatalf("wrong confirmation deleted owner token: %v", err)
	}
	deleted := post(primary.Last4)
	if deleted.Code != http.StatusSeeOther || deleted.Header().Get("Location") != "/server/preferences/tokens" || deleted.Header().Get("Clear-Site-Data") != `"storage"` {
		t.Fatalf("revoke response status=%d location=%q clear-site-data=%q body=%q", deleted.Code, deleted.Header().Get("Location"), deleted.Header().Get("Clear-Site-Data"), deleted.Body.String())
	}
	var cookie *http.Cookie
	for _, responseCookie := range deleted.Result().Cookies() {
		if responseCookie.Name == ownerTokenCookieName {
			cookie = responseCookie
		}
	}
	if cookie == nil || cookie.MaxAge >= 0 || cookie.Path != "/" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("owner token clearing cookie=%+v", cookie)
	}
	var owners, tokens, events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM owners WHERE id = ?`, primary.OwnerID).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_tokens WHERE owner_id = ?`, primary.OwnerID).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'token.revoked' AND target_id = ?`, primary.OwnerID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if owners != 0 || tokens != 0 || events != 1 {
		t.Fatalf("revocation results owners=%d tokens=%d audit-events=%d, want 0/0/1", owners, tokens, events)
	}
	var details string
	if err := db.QueryRow(`SELECT details FROM audit_log WHERE action = 'token.revoked' AND target_id = ?`, primary.OwnerID).Scan(&details); err != nil {
		t.Fatal(err)
	}
	var audit map[string]interface{}
	if err := json.Unmarshal([]byte(details), &audit); err != nil || audit["scope"] != "owner" || audit["source"] != "web" || audit["tokens"] != float64(2) || audit["resources"] != float64(0) {
		t.Fatalf("revoke audit details=%q parsed=%v err=%v", details, audit, err)
	}
}

func TestPrimaryTokenRevocationAPIUsesCanonicalEnvelopeAndCounts(t *testing.T) {
	s, db := newTokenTestServer(t)
	primary, raw, err := database.CreateOwnerToken(context.Background(), db, s.config.Server.Security.EncryptionKey)
	if err != nil {
		t.Fatalf("CreateOwnerToken(): %v", err)
	}
	_, linked, err := database.CreateLinkedOwnerToken(context.Background(), db, primary.OwnerID, "test client", nil, 25, s.config.Server.Security.EncryptionKey, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateLinkedOwnerToken(): %v", err)
	}
	request := func(token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/server/preferences/tokens/primary", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, r)
		return w
	}
	wrong := request(raw, `{"confirm":"xxxx"}`)
	var wrongEnvelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(wrong.Body.Bytes(), &wrongEnvelope); err != nil {
		t.Fatalf("wrong confirmation error response is not JSON: %v", err)
	}
	if wrong.Code != http.StatusBadRequest || wrongEnvelope.OK || wrongEnvelope.Error != "VALIDATION_FAILED" {
		t.Fatalf("wrong confirmation status=%d body=%q", wrong.Code, wrong.Body.String())
	}
	linkedDenied := request(linked, `{"confirm":"`+primary.Last4+`"}`)
	if linkedDenied.Code != http.StatusForbidden {
		t.Fatalf("linked token revoke status=%d body=%q, want 403", linkedDenied.Code, linkedDenied.Body.String())
	}
	deleted := request(raw, `{"confirm":"`+primary.Last4+`"}`)
	var deletedEnvelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Tokens    int64 `json:"tokens"`
			Resources int64 `json:"resources"`
		} `json:"data"`
	}
	if err := json.Unmarshal(deleted.Body.Bytes(), &deletedEnvelope); err != nil {
		t.Fatalf("valid revocation response is not JSON: %v", err)
	}
	if deleted.Code != http.StatusOK || !deletedEnvelope.OK || deletedEnvelope.Data.Tokens != 2 || deletedEnvelope.Data.Resources != 0 {
		t.Fatalf("valid API revocation status=%d body=%q", deleted.Code, deleted.Body.String())
	}
	if _, err := database.LookupOwnerToken(context.Background(), db, raw, time.Now()); err == nil {
		t.Fatal("revoked primary token still resolves")
	}
	var events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'token.revoked' AND target_id = ?`, primary.OwnerID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("API revocation audit event count=%d err=%v", events, err)
	}
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
