package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/testutil"

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
	if created.Code != http.StatusSeeOther || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("token issue status=%d cache-control=%q body=%q", created.Code, created.Header().Get("Cache-Control"), created.Body.String())
	}
	if strings.Contains(created.Body.String(), "own_") {
		t.Fatalf("issue redirect exposed token: %q", created.Body.String())
	}
	var ownerCookie *http.Cookie
	var viewCookie *http.Cookie
	for _, cookie := range created.Result().Cookies() {
		switch cookie.Name {
		case ownerTokenCookieName:
			ownerCookie = cookie
		case tokenViewCookieName:
			viewCookie = cookie
		}
	}
	if ownerCookie == nil || !ownerCookie.HttpOnly || ownerCookie.SameSite != http.SameSiteStrictMode || ownerCookie.Path != "/" || ownerCookie.Value == "" {
		t.Fatalf("owner cookie = %+v, want HttpOnly SameSite=Strict Path=/", ownerCookie)
	}
	if viewCookie == nil || viewCookie.Value != "primary" || viewCookie.Path != "/server/preferences/tokens" || viewCookie.MaxAge != 60 {
		t.Fatalf("token view cookie = %+v", viewCookie)
	}

	request = httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	request.AddCookie(ownerCookie)
	request.AddCookie(viewCookie)
	revealed := httptest.NewRecorder()
	s.router.ServeHTTP(revealed, request)
	clearedView := findCookie(revealed.Result().Cookies(), tokenViewCookieName)
	if revealed.Code != http.StatusOK || !strings.Contains(revealed.Body.String(), ownerCookie.Value) || clearedView == nil || clearedView.MaxAge != -1 {
		t.Fatalf("one-use token view response status=%d body=%q cookies=%v", revealed.Code, revealed.Body.String(), revealed.Result().Cookies())
	}

	request = httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	request.AddCookie(ownerCookie)
	masked := httptest.NewRecorder()
	s.router.ServeHTTP(masked, request)
	if masked.Code != http.StatusOK || strings.Contains(masked.Body.String(), ownerCookie.Value) || !strings.Contains(masked.Body.String(), "••") {
		t.Fatalf("token page did not mask saved token: status=%d body=%q", masked.Code, masked.Body.String())
	}
	maskedCSRF := findCookie(masked.Result().Cookies(), "csrf_token")
	if maskedCSRF == nil {
		t.Fatal("authenticated token page did not issue a CSRF cookie")
	}
	revealForm := url.Values{"csrf_token": []string{maskedCSRF.Value}}
	reveal := httptest.NewRequest(http.MethodPost, "/server/preferences/token/reveal", strings.NewReader(revealForm.Encode()))
	reveal.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reveal.AddCookie(ownerCookie)
	reveal.AddCookie(maskedCSRF)
	revealedPrimary := httptest.NewRecorder()
	s.router.ServeHTTP(revealedPrimary, reveal)
	revealView := findCookie(revealedPrimary.Result().Cookies(), tokenViewCookieName)
	if revealedPrimary.Code != http.StatusSeeOther || strings.Contains(revealedPrimary.Body.String(), ownerCookie.Value) || revealView == nil || revealView.Value == "" {
		t.Fatalf("primary reveal did not use one-use redirect: status=%d body=%q cookies=%v", revealedPrimary.Code, revealedPrimary.Body.String(), revealedPrimary.Result().Cookies())
	}
	revealPage := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	revealPage.AddCookie(ownerCookie)
	revealPage.AddCookie(revealView)
	revealedPage := httptest.NewRecorder()
	s.router.ServeHTTP(revealedPage, revealPage)
	if revealedPage.Code != http.StatusOK || !strings.Contains(revealedPage.Body.String(), ownerCookie.Value) {
		t.Fatalf("one-use primary reveal page status=%d body=%q", revealedPage.Code, revealedPage.Body.String())
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

	linkedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens", bytes.NewBufferString(`{"name":"build agent","expires":"7d"}`))
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
			Token string `json:"linked_token"`
			ID    string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(linkedResponse.Body.Bytes(), &linkedBody); err != nil || linkedBody.Data.Token == "" || !database.ValidTokenID(linkedBody.Data.ID) {
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
	if !strings.Contains(listed.Body.String(), linkedBody.Data.ID) || strings.Contains(listed.Body.String(), `"id":2`) {
		t.Fatalf("token list did not use the opaque token ID: %q", listed.Body.String())
	}
	legacyIntegerPath := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens/2/reveal", nil)
	legacyIntegerPath.Header.Set("Accept", "application/json")
	legacyIntegerPath.Header.Set("Authorization", "Bearer "+primary)
	legacyIntegerResponse := httptest.NewRecorder()
	s.router.ServeHTTP(legacyIntegerResponse, legacyIntegerPath)
	if legacyIntegerResponse.Code != http.StatusNotFound {
		t.Fatalf("integer token ID path status=%d body=%q, want 404", legacyIntegerResponse.Code, legacyIntegerResponse.Body.String())
	}

	linkedManagement := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens/"+linkedBody.Data.ID+"/reveal", nil)
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

func TestAuthCheckAcceptsOperatorAndOwnerWithoutUsageMutation(t *testing.T) {
	s, db := newTokenTestServer(t)
	s.config.Server.Token = "adm_0123456789abcdefghijklmnopqrstuvwxyz"
	check := func(token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, api.APIBasePath()+"/server/auth/check", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Accept", "application/json")
		response := httptest.NewRecorder()
		s.router.ServeHTTP(response, request)
		return response
	}
	operator := check(s.config.Server.Token)
	var operatorResult struct {
		Data struct {
			Kind string `json:"kind"`
		} `json:"data"`
	}
	if operator.Code != http.StatusOK || json.Unmarshal(operator.Body.Bytes(), &operatorResult) != nil || operatorResult.Data.Kind != "operator" {
		t.Fatalf("operator check = %d %s", operator.Code, operator.Body.String())
	}
	_, ownerToken, err := database.CreateOwnerToken(context.Background(), db, s.config.Server.Security.EncryptionKey)
	if err != nil {
		t.Fatalf("CreateOwnerToken(): %v", err)
	}
	if _, err := db.Exec(`UPDATE api_tokens SET last_used_at = 123 WHERE token_prefix = ?`, ownerToken[:12]); err != nil {
		t.Fatalf("set last_used_at: %v", err)
	}
	owner := check(ownerToken)
	var ownerResult struct {
		Data struct {
			Kind string `json:"kind"`
		} `json:"data"`
	}
	if owner.Code != http.StatusOK || json.Unmarshal(owner.Body.Bytes(), &ownerResult) != nil || ownerResult.Data.Kind != "owner" {
		t.Fatalf("owner check = %d %s", owner.Code, owner.Body.String())
	}
	var lastUsed int64
	if err := db.QueryRow(`SELECT last_used_at FROM api_tokens WHERE token_prefix = ?`, ownerToken[:12]).Scan(&lastUsed); err != nil || lastUsed != 123 {
		t.Fatalf("last_used_at after auth check = %d, %v", lastUsed, err)
	}
	invalid := check("invalid")
	if invalid.Code != http.StatusUnauthorized || invalid.Header().Get("WWW-Authenticate") != `Bearer realm="countries", error="invalid_token"` || strings.Contains(invalid.Body.String(), "invalid") {
		t.Fatalf("invalid check = %d headers=%v body=%s", invalid.Code, invalid.Header(), invalid.Body.String())
	}
	missingRequest := httptest.NewRequest(http.MethodGet, api.APIBasePath()+"/server/auth/check", nil)
	missing := httptest.NewRecorder()
	s.router.ServeHTTP(missing, missingRequest)
	if missing.Code != http.StatusUnauthorized || missing.Header().Get("WWW-Authenticate") != `Bearer realm="countries"` {
		t.Fatalf("missing token check = %d headers=%v body=%s", missing.Code, missing.Header(), missing.Body.String())
	}
}

func TestLinkedTokenAPIContractAndScope(t *testing.T) {
	s, db := newTokenTestServer(t)
	_, primary, err := database.CreateOwnerToken(context.Background(), db, s.config.Server.Security.EncryptionKey)
	if err != nil {
		t.Fatalf("CreateOwnerToken(): %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, api.APIBasePath()+"/server/preferences/tokens", strings.NewReader(`{"name":"read client","scope":"read","expires_on":"`+time.Now().UTC().Format("2006-01-02")+`"}`))
	request.Header.Set("Authorization", "Bearer "+primary)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	created := httptest.NewRecorder()
	s.router.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create linked token status=%d body=%s", created.Code, created.Body.String())
	}
	var result struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if result.Data["scope"] != "read" || result.Data["kind"] != "linked" || result.Data["linked_token"] == nil || result.Data["token_masked"] == nil {
		t.Fatalf("create response does not match linked-token contract: %#v", result.Data)
	}
	linked := result.Data["linked_token"].(string)
	itemID := result.Data["id"].(string)
	expires := result.Data["expires_at"].(string)
	if strings.Contains(linked, "token_masked") || !strings.Contains(expires, "T00:00:00Z") {
		t.Fatalf("raw token/date expiry mismatch: %s / %s", linked, expires)
	}
	update := httptest.NewRequest(http.MethodPatch, api.APIBasePath()+"/server/preferences/tokens/"+itemID, strings.NewReader(`{"name":"renamed client"}`))
	update.Header.Set("Authorization", "Bearer "+primary)
	update.Header.Set("Content-Type", "application/json")
	update.Header.Set("Accept", "application/json")
	updated := httptest.NewRecorder()
	s.router.ServeHTTP(updated, update)
	var updateResult struct {
		Data map[string]interface{} `json:"data"`
	}
	if updated.Code != http.StatusOK || json.Unmarshal(updated.Body.Bytes(), &updateResult) != nil || updateResult.Data["name"] != "renamed client" || updateResult.Data["expires_at"] != expires || updateResult.Data["scope"] != "read" {
		t.Fatalf("partial linked-token update status=%d body=%s", updated.Code, updated.Body.String())
	}
	list := httptest.NewRequest(http.MethodGet, api.APIBasePath()+"/server/preferences/tokens", nil)
	list.Header.Set("Authorization", "Bearer "+primary)
	list.Header.Set("Accept", "application/json")
	listed := httptest.NewRecorder()
	s.router.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"primary"`) || !strings.Contains(listed.Body.String(), `"linked"`) || !strings.Contains(listed.Body.String(), `"token_masked"`) || strings.Contains(listed.Body.String(), `"tokens":[`) {
		t.Fatalf("list response does not match token-list contract: status=%d body=%s", listed.Code, listed.Body.String())
	}
	denied := httptest.NewRequest(http.MethodGet, api.APIBasePath()+"/server/preferences/tokens", nil)
	denied.Header.Set("Authorization", "Bearer "+linked)
	denied.Header.Set("Accept", "application/json")
	forbidden := httptest.NewRecorder()
	s.router.ServeHTTP(forbidden, denied)
	if forbidden.Code != http.StatusForbidden || forbidden.Header().Get("WWW-Authenticate") != `Bearer error="insufficient_scope"` {
		t.Fatalf("read token management status=%d challenge=%q body=%s", forbidden.Code, forbidden.Header().Get("WWW-Authenticate"), forbidden.Body.String())
	}
	rotate := httptest.NewRequest(http.MethodPost, api.APIBasePath()+"/server/preferences/tokens/"+itemID+"/rotate", strings.NewReader(`{"confirm":true}`))
	rotate.Header.Set("Authorization", "Bearer "+primary)
	rotate.Header.Set("Content-Type", "application/json")
	rotate.Header.Set("Accept", "application/json")
	rotated := httptest.NewRecorder()
	s.router.ServeHTTP(rotated, rotate)
	var rotatedResult struct {
		Data map[string]interface{} `json:"data"`
	}
	if rotated.Code != http.StatusOK || json.Unmarshal(rotated.Body.Bytes(), &rotatedResult) != nil || rotatedResult.Data["linked_token"] == linked {
		t.Fatalf("linked token rotation status=%d body=%s", rotated.Code, rotated.Body.String())
	}
	revoke := httptest.NewRequest(http.MethodDelete, api.APIBasePath()+"/server/preferences/tokens/"+itemID, strings.NewReader(`{"confirm":true}`))
	revoke.Header.Set("Authorization", "Bearer "+primary)
	revoke.Header.Set("Content-Type", "application/json")
	revoke.Header.Set("Accept", "application/json")
	revoked := httptest.NewRecorder()
	s.router.ServeHTTP(revoked, revoke)
	var revokedResult struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if revoked.Code != http.StatusOK || json.Unmarshal(revoked.Body.Bytes(), &revokedResult) != nil || revokedResult.Data.Status != "revoked" {
		t.Fatalf("linked token revocation status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	if itemID == "" {
		t.Fatal("create response omitted token id")
	}
}

func TestLinkedTokenWebUpdateRevokeAndForgetFlows(t *testing.T) {
	s, db := newTokenTestServer(t)
	primary, raw, err := database.CreateOwnerToken(context.Background(), db, s.config.Server.Security.EncryptionKey)
	if err != nil {
		t.Fatalf("CreateOwnerToken(): %v", err)
	}
	linked, _, err := database.CreateLinkedOwnerToken(context.Background(), db, primary.OwnerID, "browser client", "read-write", nil, 25, s.config.Server.Security.EncryptionKey, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateLinkedOwnerToken(): %v", err)
	}
	pageRequest := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	pageRequest.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: raw})
	page := httptest.NewRecorder()
	s.router.ServeHTTP(page, pageRequest)
	csrf := findCookie(page.Result().Cookies(), "csrf_token")
	if page.Code != http.StatusOK || csrf == nil {
		t.Fatalf("owner tokens page status=%d csrf=%+v body=%s", page.Code, csrf, page.Body.String())
	}
	post := func(path string, fields url.Values) *httptest.ResponseRecorder {
		fields.Set("csrf_token", csrf.Value)
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(fields.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(csrf)
		request.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: raw})
		response := httptest.NewRecorder()
		s.router.ServeHTTP(response, request)
		return response
	}
	updated := post("/server/preferences/tokens/"+linked.ID+"/update", url.Values{"name": {"browser client updated"}, "expires": {"never"}})
	if updated.Code != http.StatusSeeOther || updated.Header().Get("Location") != "/server/preferences/tokens?status=renamed#token-"+linked.ID {
		t.Fatalf("web token update status=%d location=%q body=%s", updated.Code, updated.Header().Get("Location"), updated.Body.String())
	}
	revoked := post("/server/preferences/tokens/"+linked.ID+"/revoke", url.Values{"confirm": {"yes"}})
	if revoked.Code != http.StatusSeeOther || revoked.Header().Get("Location") != "/server/preferences/tokens?status=revoked#token-"+linked.ID {
		t.Fatalf("web token revoke status=%d location=%q body=%s", revoked.Code, revoked.Header().Get("Location"), revoked.Body.String())
	}
	forget := post("/server/preferences/token/forget", url.Values{"confirm": {"yes"}})
	if forget.Code != http.StatusSeeOther || forget.Header().Get("Clear-Site-Data") != `"storage"` || findCookie(forget.Result().Cookies(), ownerTokenCookieName) == nil {
		t.Fatalf("forget token status=%d headers=%v cookies=%v body=%s", forget.Code, forget.Header(), forget.Result().Cookies(), forget.Body.String())
	}
}

func TestPrimaryTokenRevocationWebRequiresLastFourAndDeletesOwner(t *testing.T) {
	s, db := newTokenTestServer(t)
	primary, raw, err := database.CreateOwnerToken(context.Background(), db, s.config.Server.Security.EncryptionKey)
	if err != nil {
		t.Fatalf("CreateOwnerToken(): %v", err)
	}
	if _, _, err := database.CreateLinkedOwnerToken(context.Background(), db, primary.OwnerID, "test client", "read-write", nil, 25, s.config.Server.Security.EncryptionKey, time.Now().UTC()); err != nil {
		t.Fatalf("CreateLinkedOwnerToken(): %v", err)
	}
	pageRequest := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	pageRequest.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: raw})
	page := httptest.NewRecorder()
	s.router.ServeHTTP(page, pageRequest)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `id="revoke-primary-title"`) || !strings.Contains(page.Body.String(), primary.Last4) {
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
	if err := json.Unmarshal([]byte(details), &audit); err != nil || audit["scope"] != "owner" || audit["source"] != "web" || audit["revoked_count"] != float64(2) || audit["resources"] != float64(0) {
		t.Fatalf("revoke audit details=%q parsed=%v err=%v", details, audit, err)
	}
}

func TestPrimaryTokenRevocationAPIUsesCanonicalEnvelopeAndCounts(t *testing.T) {
	s, db := newTokenTestServer(t)
	primary, raw, err := database.CreateOwnerToken(context.Background(), db, s.config.Server.Security.EncryptionKey)
	if err != nil {
		t.Fatalf("CreateOwnerToken(): %v", err)
	}
	_, linked, err := database.CreateLinkedOwnerToken(context.Background(), db, primary.OwnerID, "test client", "read-write", nil, 25, s.config.Server.Security.EncryptionKey, time.Now().UTC())
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
