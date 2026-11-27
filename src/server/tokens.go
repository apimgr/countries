package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/database"
	"github.com/go-chi/chi/v5"
)

const ownerTokenCookieName = "countries_owner_token_gId2fHet"

type tokenPageItem struct {
	Record      database.TokenRecord
	Masked      string
	Status      string
	Created     string
	LastUsed    string
	Expires     string
	ExpiresDate string
}

func (s *Server) handleTokensPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.templates == nil {
		renderFallbackError(w, r, http.StatusInternalServerError)
		return
	}
	data, _, ok := s.tokensPageData(w, r, false)
	if !ok {
		return
	}
	s.renderTemplateResponse(w, r, "tokens-page.html", data, http.StatusOK)
}

func (s *Server) handleTokensAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	identity, err := s.tokenIdentity(r, true, w)
	if err != nil {
		s.tokenAPIError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "A valid primary owner token is required")
		return
	}
	if identity.Kind != "primary" {
		s.tokenAPIError(w, http.StatusForbidden, "PRIMARY_TOKEN_REQUIRED", "Token management requires the primary owner token")
		return
	}
	items, err := database.ListOwnerTokens(r.Context(), s.db, identity.OwnerID)
	if err != nil {
		s.tokenAPIError(w, http.StatusServiceUnavailable, "TOKEN_STORE_UNAVAILABLE", "Token data is temporarily unavailable")
		return
	}
	s.jsonTokenList(w, items, time.Now())
}

func (s *Server) handleIssueOwnerToken(w http.ResponseWriter, r *http.Request) {
	isAPI := isTokenAPIRequest(r)
	w.Header().Set("Cache-Control", "no-store")
	if !s.takeStrictTokenLimit(w, r, "") {
		return
	}
	if !isAPI {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if err := r.ParseForm(); err != nil || !validCSRF(r) {
			s.errorResponse(w, r, "Invalid CSRF token", http.StatusForbidden)
			return
		}
	}
	if s.db == nil {
		s.tokenUnavailable(w, r, isAPI)
		return
	}
	if bearer := bearerValue(r); bearer != "" {
		if s.validOperatorToken(bearer) {
			if isAPI {
				s.tokenAPIError(w, http.StatusConflict, "OWNER_TOKEN_EXISTS", "This caller already has a valid token")
			} else {
				s.errorResponse(w, r, "A valid token is already active in this browser", http.StatusConflict)
			}
			return
		}
		if _, err := database.LookupOwnerToken(r.Context(), s.db, bearer, time.Now()); err == nil {
			if isAPI {
				s.tokenAPIError(w, http.StatusConflict, "OWNER_TOKEN_EXISTS", "This caller already has a valid owner token")
			} else {
				s.errorResponse(w, r, "An owner token is already active in this browser", http.StatusConflict)
			}
			return
		}
	}
	if !isAPI {
		if _, err := s.tokenIdentity(r, false, w); err == nil {
			s.errorResponse(w, r, "A valid token is already active in this browser", http.StatusConflict)
			return
		}
	}
	if s.db == nil {
		s.tokenUnavailable(w, r, isAPI)
		return
	}
	item, raw, err := database.CreateOwnerToken(r.Context(), s.db, s.config.Server.Security.EncryptionKey)
	if err != nil {
		s.tokenUnavailable(w, r, isAPI)
		return
	}
	s.setOwnerTokenCookie(w, r, raw)
	s.auditTokenEvent(r, "token.created", item.OwnerID, item.Prefix, "issue")
	if isAPI {
		writeJSON(w, http.StatusCreated, map[string]interface{}{"ok": true, "data": map[string]interface{}{"owner_token": raw}})
		return
	}
	data, _, ok := s.tokensPageDataForIdentity(w, r, database.TokenIdentity{OwnerID: item.OwnerID, Kind: "primary"})
	if !ok {
		return
	}
	data["RevealedTokenID"] = item.ID
	data["RevealedToken"] = raw
	data["NoticeKey"] = "tokens.create_success"
	s.renderTokenPage(w, r, data, http.StatusCreated)
}

func (s *Server) validOperatorToken(raw string) bool {
	valid := false
	if s.config == nil || s.config.Server.Token == "" || raw == "" {
		if s.metrics != nil {
			s.metrics.RecordAuth("bearer", "failed")
		}
		return false
	}
	actual := sha256.Sum256([]byte(raw))
	expected := sha256.Sum256([]byte(s.config.Server.Token))
	valid = subtle.ConstantTimeCompare(actual[:], expected[:]) == 1
	if s.metrics != nil {
		status := "failed"
		if valid {
			status = "success"
		}
		s.metrics.RecordAuth("bearer", status)
	}
	return valid
}

func (s *Server) handleRestoreOwnerToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.takeStrictTokenLimit(w, r, "") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := r.ParseForm(); err != nil || !validCSRF(r) {
		s.errorResponse(w, r, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	if s.db == nil {
		s.tokenUnavailable(w, r, false)
		return
	}
	raw := strings.TrimSpace(r.FormValue("primary_token"))
	identity, err := database.LookupOwnerToken(r.Context(), s.db, raw, time.Now())
	if err != nil || identity.Kind != "primary" {
		s.clearOwnerTokenCookie(w, r)
		s.tokenPageRedirect(w, r, "error=invalid")
		return
	}
	s.setOwnerTokenCookie(w, r, raw)
	s.auditTokenEvent(r, "token.restored", identity.OwnerID, identity.Prefix, "restore")
	s.tokenPageRedirect(w, r, "status=restored")
}

func (s *Server) handleRevealPrimaryToken(w http.ResponseWriter, r *http.Request) {
	s.handleRevealToken(w, r, "", true)
}

func (s *Server) handleRevealLinkedToken(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTokenID(chi.URLParam(r, "id"))
	if !ok {
		s.tokenNotFound(w, r, isTokenAPIRequest(r))
		return
	}
	s.handleRevealToken(w, r, id, false)
}

func (s *Server) handleRevealToken(w http.ResponseWriter, r *http.Request, tokenID string, primary bool) {
	isAPI := isTokenAPIRequest(r)
	w.Header().Set("Cache-Control", "no-store")
	if !s.takeStrictTokenLimit(w, r, "") {
		return
	}
	if !s.requireWebCSRF(w, r, isAPI) {
		return
	}
	identity, ok := s.requirePrimaryToken(w, r, isAPI)
	if !ok {
		return
	}
	if primary {
		items, err := database.ListOwnerTokens(r.Context(), s.db, identity.OwnerID)
		if err != nil || len(items) == 0 || items[0].Kind != "primary" {
			s.tokenNotFound(w, r, isAPI)
			return
		}
		tokenID = items[0].ID
	}
	raw, item, err := database.RevealOwnerToken(r.Context(), s.db, identity.OwnerID, tokenID, s.config.Server.Security.EncryptionKey)
	if err != nil || primary && item.Kind != "primary" || !primary && item.Kind != "linked" {
		s.tokenNotFound(w, r, isAPI)
		return
	}
	s.auditTokenEvent(r, "token.revealed", identity.OwnerID, item.Prefix, "reveal")
	if isAPI {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": map[string]interface{}{"token": raw, "id": item.ID}})
		return
	}
	data, _, ok := s.tokensPageDataForIdentity(w, r, identity)
	if !ok {
		return
	}
	data["RevealedTokenID"] = item.ID
	data["RevealedToken"] = raw
	s.renderTokenPage(w, r, data, http.StatusOK)
}

func (s *Server) handleRotatePrimaryToken(w http.ResponseWriter, r *http.Request) {
	s.handleRotateToken(w, r, "", true)
}

func (s *Server) handleRotateLinkedToken(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTokenID(chi.URLParam(r, "id"))
	if !ok {
		s.tokenNotFound(w, r, isTokenAPIRequest(r))
		return
	}
	s.handleRotateToken(w, r, id, false)
}

func (s *Server) handleRotateToken(w http.ResponseWriter, r *http.Request, tokenID string, primary bool) {
	isAPI := isTokenAPIRequest(r)
	w.Header().Set("Cache-Control", "no-store")
	if !s.takeStrictTokenLimit(w, r, "") {
		return
	}
	if !s.requireWebCSRF(w, r, isAPI) {
		return
	}
	if !confirmTokenAction(w, r, isAPI) {
		s.tokenActionError(w, r, isAPI, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "Confirm token rotation")
		return
	}
	identity, ok := s.requirePrimaryToken(w, r, isAPI)
	if !ok {
		return
	}
	if primary {
		items, err := database.ListOwnerTokens(r.Context(), s.db, identity.OwnerID)
		if err != nil || len(items) == 0 || items[0].Kind != "primary" {
			s.tokenNotFound(w, r, isAPI)
			return
		}
		tokenID = items[0].ID
	}
	item, raw, err := database.RotateOwnerToken(r.Context(), s.db, identity.OwnerID, tokenID, s.config.Server.Security.EncryptionKey, time.Now().UTC())
	if err != nil || primary && item.Kind != "primary" || !primary && item.Kind != "linked" {
		s.tokenNotFound(w, r, isAPI)
		return
	}
	s.auditTokenEvent(r, "token.rotated", identity.OwnerID, item.Prefix, "rotate")
	if primary {
		s.setOwnerTokenCookie(w, r, raw)
	}
	if isAPI {
		field := "token"
		if primary {
			field = "owner_token"
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": map[string]interface{}{field: raw, "id": item.ID}})
		return
	}
	data, _, ok := s.tokensPageDataForIdentity(w, r, identity)
	if !ok {
		return
	}
	data["RevealedTokenID"] = item.ID
	data["RevealedToken"] = raw
	data["NoticeKey"] = "tokens.rotate_success"
	s.renderTokenPage(w, r, data, http.StatusOK)
}

func (s *Server) handleCreateLinkedToken(w http.ResponseWriter, r *http.Request) {
	isAPI := isTokenAPIRequest(r)
	w.Header().Set("Cache-Control", "no-store")
	if !s.requireWebCSRF(w, r, isAPI) {
		return
	}
	identity, ok := s.requirePrimaryToken(w, r, isAPI)
	if !ok {
		return
	}
	name, expiry, err := s.readLinkedTokenInput(w, r, isAPI)
	if err != nil {
		s.tokenActionError(w, r, isAPI, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid linked token name or expiry")
		return
	}
	item, raw, err := database.CreateLinkedOwnerToken(r.Context(), s.db, identity.OwnerID, name, expiry, s.config.Server.Tokens.MaxLinked, s.config.Server.Security.EncryptionKey, time.Now().UTC())
	if errors.Is(err, database.ErrTokenLimit) {
		s.tokenActionError(w, r, isAPI, http.StatusConflict, "TOKEN_LIMIT_REACHED", "The linked token limit has been reached")
		return
	}
	if errors.Is(err, database.ErrTokenName) {
		s.tokenActionError(w, r, isAPI, http.StatusConflict, "TOKEN_NAME_EXISTS", "A linked token already uses that name")
		return
	}
	if err != nil {
		s.tokenUnavailable(w, r, isAPI)
		return
	}
	s.auditTokenEvent(r, "token.created", identity.OwnerID, item.Prefix, "linked")
	if isAPI {
		writeJSON(w, http.StatusCreated, map[string]interface{}{"ok": true, "data": map[string]interface{}{"token": raw, "id": item.ID}})
		return
	}
	data, _, ok := s.tokensPageData(w, r, false)
	if !ok {
		return
	}
	data["RevealedTokenID"] = item.ID
	data["RevealedToken"] = raw
	data["NoticeKey"] = "tokens.create_success"
	s.renderTokenPage(w, r, data, http.StatusCreated)
}

func (s *Server) handleUpdateLinkedTokenWeb(w http.ResponseWriter, r *http.Request) {
	s.updateLinkedToken(w, r, false)
}

func (s *Server) handleUpdateLinkedTokenAPI(w http.ResponseWriter, r *http.Request) {
	s.updateLinkedToken(w, r, true)
}

func (s *Server) updateLinkedToken(w http.ResponseWriter, r *http.Request, isAPI bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.requireWebCSRF(w, r, isAPI) {
		return
	}
	identity, ok := s.requirePrimaryToken(w, r, isAPI)
	if !ok {
		return
	}
	id, ok := parseTokenID(chi.URLParam(r, "id"))
	if !ok {
		s.tokenNotFound(w, r, isAPI)
		return
	}
	name, expiry, err := s.readLinkedTokenInput(w, r, isAPI)
	if err != nil {
		s.tokenActionError(w, r, isAPI, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid linked token name or expiry")
		return
	}
	if err := database.UpdateLinkedOwnerToken(r.Context(), s.db, identity.OwnerID, id, name, expiry, time.Now()); err != nil {
		if errors.Is(err, database.ErrTokenNotFound) {
			s.tokenNotFound(w, r, isAPI)
			return
		}
		if errors.Is(err, database.ErrTokenName) || strings.Contains(err.Error(), "unique") {
			s.tokenActionError(w, r, isAPI, http.StatusConflict, "TOKEN_NAME_EXISTS", "A linked token already uses that name")
			return
		}
		s.tokenUnavailable(w, r, isAPI)
		return
	}
	s.auditTokenEvent(r, "token.updated", identity.OwnerID, "", "linked")
	if isAPI {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": map[string]interface{}{"id": id, "name": name, "expires_at": expiry}})
		return
	}
	s.tokenPageRedirect(w, r, "status=renamed#token-"+id)
}

func (s *Server) handleRevokeLinkedTokenWeb(w http.ResponseWriter, r *http.Request) {
	s.revokeLinkedToken(w, r, false)
}

func (s *Server) handleRevokeLinkedTokenAPI(w http.ResponseWriter, r *http.Request) {
	s.revokeLinkedToken(w, r, true)
}

func (s *Server) handleRevokePrimaryTokenWeb(w http.ResponseWriter, r *http.Request) {
	s.revokePrimaryToken(w, r, false)
}

func (s *Server) handleRevokePrimaryTokenAPI(w http.ResponseWriter, r *http.Request) {
	s.revokePrimaryToken(w, r, true)
}

func (s *Server) revokePrimaryToken(w http.ResponseWriter, r *http.Request, isAPI bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.takeStrictTokenLimit(w, r, "") || !s.requireWebCSRF(w, r, isAPI) {
		return
	}
	identity, ok := s.requirePrimaryToken(w, r, isAPI)
	if !ok {
		return
	}
	confirm := ""
	if isAPI {
		var body struct {
			Confirm string `json:"confirm"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&body); err != nil {
			s.tokenActionError(w, r, true, http.StatusBadRequest, "VALIDATION_FAILED", "Type the last four characters of the primary token to confirm")
			return
		}
		var extra interface{}
		if err := decoder.Decode(&extra); err != io.EOF {
			s.tokenActionError(w, r, true, http.StatusBadRequest, "VALIDATION_FAILED", "Type the last four characters of the primary token to confirm")
			return
		}
		confirm = body.Confirm
	} else {
		confirm = r.FormValue("confirm")
	}
	items, err := database.ListOwnerTokens(r.Context(), s.db, identity.OwnerID)
	if err != nil {
		s.tokenUnavailable(w, r, isAPI)
		return
	}
	var last4 string
	for _, item := range items {
		if item.Kind == "primary" {
			last4 = item.Last4
			break
		}
	}
	if len(confirm) != 4 || len(last4) != 4 || subtle.ConstantTimeCompare([]byte(confirm), []byte(last4)) != 1 {
		s.tokenActionError(w, r, isAPI, http.StatusBadRequest, "VALIDATION_FAILED", "Type the last four characters of the primary token to confirm")
		return
	}
	counts, err := revokePrimaryOwner(r.Context(), s.db, identity.OwnerID)
	if err != nil {
		log.Printf("primary token revocation failed: owner=%s error=%v", identity.OwnerID, err)
		s.tokenUnavailable(w, r, isAPI)
		return
	}
	s.recordAudit(r, "token", "token.revoked", "owner", identity.OwnerID, map[string]any{"owner_id": identity.OwnerID, "scope": "owner", "revoked_count": counts.tokens, "resources": counts.resources, "source": sourceForTokenRequest(isAPI)}, true)
	if isAPI {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": map[string]interface{}{"tokens": counts.tokens, "resources": counts.resources}})
		return
	}
	s.clearOwnerTokenCookie(w, r)
	w.Header().Set("Clear-Site-Data", `"storage"`)
	s.tokenPageRedirect(w, r, "")
}

type ownerRevocationCounts struct {
	tokens    int64
	resources int64
}

type ownerSQL interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func revokePrimaryOwner(ctx context.Context, db *sql.DB, ownerID string) (ownerRevocationCounts, error) {
	var counts ownerRevocationCounts
	err := database.Write(ctx, db, func(tx *sql.Tx) error {
		var err error
		counts, err = countOwnerData(ctx, tx, ownerID)
		if err != nil {
			return err
		}
		if _, err := deleteOwnerResources(ctx, tx, ownerID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM owners WHERE id = ?`, ownerID)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if deleted != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
	if err != nil {
		return ownerRevocationCounts{}, err
	}
	return counts, nil
}

func countOwnerData(ctx context.Context, q ownerSQL, ownerID string) (ownerRevocationCounts, error) {
	var counts ownerRevocationCounts
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ?`, ownerID).Scan(&counts.tokens); err != nil {
		return ownerRevocationCounts{}, err
	}
	_, resources, err := ownerResourceTables(ctx, q, ownerID, false)
	counts.resources = resources
	return counts, err
}

func deleteOwnerResources(ctx context.Context, q ownerSQL, ownerID string) (int64, error) {
	_, deleted, err := ownerResourceTables(ctx, q, ownerID, true)
	return deleted, err
}

func ownerResourceTables(ctx context.Context, q ownerSQL, ownerID string, remove bool) ([]string, int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, 0, err
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, 0, errors.Join(err, rows.Close())
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	var total int64
	var resourceTables []string
	for _, table := range tables {
		if table == "owners" || table == "api_tokens" || table == "audit_log" {
			continue
		}
		columns, err := q.QueryContext(ctx, `PRAGMA table_info("`+strings.ReplaceAll(table, `"`, `""`)+`")`)
		if err != nil {
			return nil, 0, err
		}
		hasOwner := false
		for columns.Next() {
			var cid, notNull, primaryKey int
			var name, kind string
			var defaultValue interface{}
			if err := columns.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
				return nil, 0, errors.Join(err, columns.Close())
			}
			if name == "owner_id" {
				hasOwner = true
			}
		}
		if err := columns.Err(); err != nil {
			return nil, 0, errors.Join(err, columns.Close())
		}
		if err := columns.Close(); err != nil {
			return nil, 0, err
		}
		if !hasOwner {
			continue
		}
		quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		var count int64
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoted+` WHERE owner_id = ?`, ownerID).Scan(&count); err != nil {
			return nil, 0, err
		}
		total += count
		resourceTables = append(resourceTables, table)
		if remove {
			if _, err := q.ExecContext(ctx, `DELETE FROM `+quoted+` WHERE owner_id = ?`, ownerID); err != nil {
				return nil, 0, err
			}
		}
	}
	return resourceTables, total, nil
}

func sourceForTokenRequest(isAPI bool) string {
	if isAPI {
		return "api"
	}
	return "web"
}

func (s *Server) revokeLinkedToken(w http.ResponseWriter, r *http.Request, isAPI bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.requireWebCSRF(w, r, isAPI) {
		return
	}
	identity, ok := s.requirePrimaryToken(w, r, isAPI)
	if !ok {
		return
	}
	id, ok := parseTokenID(chi.URLParam(r, "id"))
	if !ok {
		s.tokenNotFound(w, r, isAPI)
		return
	}
	if !confirmTokenAction(w, r, isAPI) {
		s.tokenActionError(w, r, isAPI, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "Confirm token revocation")
		return
	}
	items, err := database.ListOwnerTokens(r.Context(), s.db, identity.OwnerID)
	if err != nil {
		s.tokenUnavailable(w, r, isAPI)
		return
	}
	var prefix string
	for _, item := range items {
		if item.ID == id && item.Kind == "linked" {
			prefix = item.Prefix
		}
	}
	if prefix == "" {
		s.tokenNotFound(w, r, isAPI)
		return
	}
	if err := database.RevokeLinkedOwnerToken(r.Context(), s.db, identity.OwnerID, id, time.Now()); err != nil {
		s.tokenNotFound(w, r, isAPI)
		return
	}
	s.setTokenRevocationClearSiteData(w)
	s.auditTokenEvent(r, "token.revoked", identity.OwnerID, prefix, "owner_revoked")
	if isAPI {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": map[string]interface{}{"id": id, "revoked": true}})
		return
	}
	s.tokenPageRedirect(w, r, "status=revoked#token-"+id)
}

func (s *Server) setTokenRevocationClearSiteData(w http.ResponseWriter) {
	if !s.config.Web.Headers.ClearSiteData.OnTokenRevocation {
		return
	}
	value := `"cache", "cookies", "storage"`
	if s.config.Web.Headers.ClearSiteData.ExecutionContexts {
		value = `"cache", "cookies", "storage", "executionContexts"`
	}
	w.Header().Set("Clear-Site-Data", value)
}

func (s *Server) handleForgetOwnerToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := r.ParseForm(); err != nil || !validCSRF(r) {
		s.errorResponse(w, r, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	if r.FormValue("confirm") != "yes" {
		s.errorResponse(w, r, "Confirm forgetting this browser token", http.StatusBadRequest)
		return
	}
	s.clearOwnerTokenCookie(w, r)
	w.Header().Set("Clear-Site-Data", `"storage"`)
	s.tokenPageRedirect(w, r, "")
}

func (s *Server) tokensPageData(w http.ResponseWriter, r *http.Request, apiRequest bool) (map[string]interface{}, database.TokenIdentity, bool) {
	data := s.pageContext(w, r, "/server/preferences/tokens")
	data["HasOwner"] = false
	data["Primary"] = nil
	data["Linked"] = []tokenPageItem{}
	data["TokenStatus"] = ""
	data["NoticeKey"] = ""
	data["TokenError"] = ""
	data["PrimaryLast4"] = ""
	data["PrimaryLinkedCount"] = 0
	data["PrimaryResourceCount"] = int64(0)
	data["ResourceLabelPlural"] = "items"
	if s.db == nil {
		return data, database.TokenIdentity{}, true
	}
	identity, err := s.tokenIdentity(r, apiRequest, w)
	if err != nil || identity.Kind != "primary" {
		return data, database.TokenIdentity{}, true
	}
	return s.populateTokensPageData(w, r, data, identity)
}

func (s *Server) tokensPageDataForIdentity(w http.ResponseWriter, r *http.Request, identity database.TokenIdentity) (map[string]interface{}, database.TokenIdentity, bool) {
	data := s.pageContext(w, r, "/server/preferences/tokens")
	data["HasOwner"] = false
	data["Primary"] = nil
	data["Linked"] = []tokenPageItem{}
	data["TokenStatus"] = ""
	data["NoticeKey"] = ""
	data["TokenError"] = ""
	data["PrimaryLast4"] = ""
	data["PrimaryLinkedCount"] = 0
	data["PrimaryResourceCount"] = int64(0)
	data["ResourceLabelPlural"] = "items"
	return s.populateTokensPageData(w, r, data, identity)
}

func (s *Server) populateTokensPageData(w http.ResponseWriter, r *http.Request, data map[string]interface{}, identity database.TokenIdentity) (map[string]interface{}, database.TokenIdentity, bool) {
	if identity.Kind != "primary" || identity.OwnerID == "" || s.db == nil {
		return data, database.TokenIdentity{}, true
	}
	items, err := database.ListOwnerTokens(r.Context(), s.db, identity.OwnerID)
	if err != nil {
		return data, identity, false
	}
	data["HasOwner"] = true
	data["OwnerID"] = identity.OwnerID
	data["TokenStatus"] = r.URL.Query().Get("status")
	data["TokenError"] = r.URL.Query().Get("error")
	var linkedCount int
	switch r.URL.Query().Get("status") {
	case "restored":
		data["NoticeKey"] = "tokens.restore_success"
	case "renamed":
		data["NoticeKey"] = "tokens.rename_success"
	case "revoked":
		data["NoticeKey"] = "tokens.revoke_success"
	}
	for _, record := range items {
		view := tokenView(record, time.Now())
		if record.Kind == "primary" {
			data["Primary"] = view
			data["PrimaryLast4"] = record.Last4
		} else {
			linkedCount++
			linked := data["Linked"].([]tokenPageItem)
			data["Linked"] = append(linked, view)
		}
	}
	counts, err := countOwnerData(r.Context(), s.db, identity.OwnerID)
	if err != nil {
		return data, identity, false
	}
	data["PrimaryLinkedCount"] = linkedCount
	data["PrimaryResourceCount"] = counts.resources
	return data, identity, true
}

func tokenView(item database.TokenRecord, now time.Time) tokenPageItem {
	status := "active"
	if item.RevokedAt != nil {
		status = "revoked"
	} else if item.ExpiresAt != nil && !item.ExpiresAt.After(now) {
		status = "expired"
	}
	formatTime := func(value *time.Time) string {
		if value == nil {
			return ""
		}
		return value.UTC().Format(time.RFC3339)
	}
	expiresDate := ""
	if item.ExpiresAt != nil {
		expiresDate = item.ExpiresAt.UTC().Format("2006-01-02")
	}
	return tokenPageItem{Record: item, Masked: "own_••••••••••••••••••••••••" + item.Last4, Status: status, Created: item.CreatedAt.Format(time.RFC3339), LastUsed: formatTime(item.LastUsed), Expires: formatTime(item.ExpiresAt), ExpiresDate: expiresDate}
}

func (s *Server) renderTokenPage(w http.ResponseWriter, r *http.Request, data map[string]interface{}, status int) {
	w.Header().Set("Cache-Control", "no-store")
	s.renderTemplateResponse(w, r, "tokens-page.html", data, status)
}

func (s *Server) tokenIdentity(r *http.Request, apiRequest bool, w http.ResponseWriter) (database.TokenIdentity, error) {
	method := "owner_token"
	if apiRequest {
		method = "api_token"
	}
	if s.db == nil {
		if s.metrics != nil {
			s.metrics.RecordAuth(method, "failed")
		}
		return database.TokenIdentity{}, sql.ErrConnDone
	}
	raw := ""
	if apiRequest {
		raw = bearerValue(r)
	} else if cookie, err := r.Cookie(ownerTokenCookieName); err == nil {
		raw = cookie.Value
	}
	identity, err := database.LookupOwnerToken(r.Context(), s.db, raw, time.Now())
	if err != nil {
		if s.metrics != nil {
			s.metrics.RecordAuth(method, "failed")
		}
		if !apiRequest && raw != "" {
			s.clearOwnerTokenCookie(w, r)
		}
		return database.TokenIdentity{}, err
	}
	if s.metrics != nil {
		s.metrics.RecordAuth(method, "success")
	}
	if !apiRequest {
		if identity.Kind != "primary" {
			s.clearOwnerTokenCookie(w, r)
			return database.TokenIdentity{}, database.ErrTokenNotFound
		}
		s.setOwnerTokenCookie(w, r, raw)
	}
	return identity, nil
}

func (s *Server) requirePrimaryToken(w http.ResponseWriter, r *http.Request, isAPI bool) (database.TokenIdentity, bool) {
	identity, err := s.tokenIdentity(r, isAPI, w)
	if err != nil {
		s.tokenActionError(w, r, isAPI, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "A valid primary owner token is required")
		return database.TokenIdentity{}, false
	}
	if identity.Kind != "primary" {
		s.tokenActionError(w, r, isAPI, http.StatusForbidden, "PRIMARY_TOKEN_REQUIRED", "Token management requires the primary owner token")
		return database.TokenIdentity{}, false
	}
	return identity, true
}

func (s *Server) setOwnerTokenCookie(w http.ResponseWriter, r *http.Request, raw string) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, &http.Cookie{Name: ownerTokenCookieName, Value: raw, Path: "/", MaxAge: s.config.Server.Tokens.CookieMaxAgeSeconds(), HttpOnly: true, Secure: strings.HasPrefix(s.reportOrigin(r), "https://"), SameSite: http.SameSiteStrictMode})
}

func (s *Server) clearOwnerTokenCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: ownerTokenCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(s.reportOrigin(r), "https://"), SameSite: http.SameSiteStrictMode})
}

func (s *Server) takeStrictTokenLimit(w http.ResponseWriter, r *http.Request, ownerID string) bool {
	if s.rateLimiter == nil {
		return true
	}
	if ownerID == "" && s.db != nil {
		raw := bearerValue(r)
		if raw == "" && !isTokenAPIRequest(r) {
			if cookie, err := r.Cookie(ownerTokenCookieName); err == nil {
				raw = cookie.Value
			}
		}
		if raw != "" {
			if identity, err := database.LookupOwnerToken(r.Context(), s.db, raw, time.Now()); err == nil {
				ownerID = identity.OwnerID
			}
		}
	}
	client := requestClientIP(r.RemoteAddr)
	if ownerID != "" {
		client = "owner:" + ownerID
	}
	rule := s.config.Server.RateLimit.Strict
	allowed, retry, err := s.rateLimiter.allow(r.Context(), client, "strict", rule)
	if err != nil {
		s.tokenActionError(w, r, isTokenAPIRequest(r), http.StatusServiceUnavailable, "RATE_LIMIT_UNAVAILABLE", "Request limits are temporarily unavailable")
		return false
	}
	if !allowed {
		w.Header().Set("Retry-After", retry)
		s.tokenActionError(w, r, isTokenAPIRequest(r), http.StatusTooManyRequests, "RATE_LIMITED", "Too many token management requests")
		return false
	}
	return true
}

func (s *Server) requireWebCSRF(w http.ResponseWriter, r *http.Request, isAPI bool) bool {
	if isAPI {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil || !validCSRF(r) {
		s.errorResponse(w, r, "Invalid CSRF token", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) readLinkedTokenInput(w http.ResponseWriter, r *http.Request, isAPI bool) (string, *time.Time, error) {
	var values map[string]string
	if isAPI {
		values = map[string]string{}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
			return "", nil, err
		}
	} else {
		values = map[string]string{"name": r.FormValue("name"), "expiry": r.FormValue("expiry"), "expires_on": r.FormValue("expires_on")}
	}
	name := strings.TrimSpace(values["name"])
	if len([]rune(name)) < 1 || len([]rune(name)) > 64 {
		return "", nil, fmt.Errorf("invalid token name")
	}
	expiry := strings.TrimSpace(values["expiry"])
	now := time.Now().UTC()
	switch expiry {
	case "", "never":
		return name, nil, nil
	case "7d":
		value := now.AddDate(0, 0, 7)
		return name, &value, nil
	case "30d":
		value := now.AddDate(0, 0, 30)
		return name, &value, nil
	case "90d":
		value := now.AddDate(0, 0, 90)
		return name, &value, nil
	case "1y":
		value := now.AddDate(1, 0, 0)
		return name, &value, nil
	case "custom":
		value, err := time.Parse("2006-01-02", values["expires_on"])
		if err != nil || !value.After(now) {
			return "", nil, fmt.Errorf("custom expiry must be a future date")
		}
		value = value.Add(24*time.Hour - time.Second).UTC()
		return name, &value, nil
	default:
		if value, err := time.Parse(time.RFC3339, expiry); err == nil && value.After(now) {
			return name, &value, nil
		}
		return "", nil, fmt.Errorf("invalid expiry")
	}
}

func confirmTokenAction(w http.ResponseWriter, r *http.Request, isAPI bool) bool {
	if isAPI {
		var body struct {
			Confirm bool `json:"confirm"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return false
		}
		return body.Confirm
	}
	return r.FormValue("confirm") == "yes"
}

func (s *Server) auditTokenEvent(r *http.Request, action, ownerID, prefix, source string) {
	s.recordAudit(r, "token", action, "owner", ownerID, map[string]any{"owner_id": ownerID, "prefix": prefix, "source": source}, true)
}

func (s *Server) tokenPageRedirect(w http.ResponseWriter, r *http.Request, suffix string) {
	location := s.requestURLPrefix(r) + "/server/preferences/tokens"
	if suffix != "" {
		if strings.HasPrefix(suffix, "#") {
			location += suffix
		} else if strings.Contains(suffix, "#") {
			parts := strings.SplitN(suffix, "#", 2)
			location += "?" + parts[0] + "#" + parts[1]
		} else {
			location += "?" + suffix
		}
	}
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func isTokenAPIRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, api.APIBasePath()+"/")
}

func bearerValue(r *http.Request) string {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return ""
}

func parseTokenID(value string) (string, bool) {
	if !database.ValidTokenID(value) {
		return "", false
	}
	return value, true
}

func (s *Server) jsonTokenList(w http.ResponseWriter, items []database.TokenRecord, now time.Time) {
	public := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		view := tokenView(item, now)
		public = append(public, map[string]interface{}{"id": item.ID, "kind": item.Kind, "name": item.Name, "prefix": item.Prefix, "last4": item.Last4, "masked": view.Masked, "created_at": item.CreatedAt, "rotated_at": item.RotatedAt, "expires_at": item.ExpiresAt, "last_used_at": item.LastUsed, "status": view.Status})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": map[string]interface{}{"tokens": public}})
}

func (s *Server) tokenActionError(w http.ResponseWriter, r *http.Request, isAPI bool, status int, code, message string) {
	if isAPI {
		s.tokenAPIError(w, status, code, message)
		return
	}
	s.errorResponse(w, r, message, status)
}

func (s *Server) tokenAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]interface{}{"ok": false, "error": code, "message": message})
}

func (s *Server) tokenUnavailable(w http.ResponseWriter, r *http.Request, isAPI bool) {
	s.tokenActionError(w, r, isAPI, http.StatusServiceUnavailable, "TOKEN_STORE_UNAVAILABLE", "Token storage is temporarily unavailable")
}

func (s *Server) tokenNotFound(w http.ResponseWriter, r *http.Request, isAPI bool) {
	s.tokenActionError(w, r, isAPI, http.StatusNotFound, "NOT_FOUND", "Token not found")
}
