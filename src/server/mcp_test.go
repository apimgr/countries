package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/config"
)

const mcpTestOperatorToken = "adm_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func mcpTestRequest(s *Server, path, method string, body []byte, token, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	if len(body) > 0 {
		var payload map[string]interface{}
		_ = json.Unmarshal(body, &payload)
		request.Header.Set("Mcp-Method", payload["method"].(string))
		request.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	}
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	return response
}

func mcpPayload(method string, params interface{}) []byte {
	payload, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      7,
		"method":  method,
		"params":  params,
		"_meta": map[string]string{
			"io.modelcontextprotocol/protocolVersion": mcpProtocolVersion,
		},
	})
	return payload
}

func TestMCPValidationOrderAndProtocol(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Token = mcpTestOperatorToken
	s := newTestServerWithConfig(t, cfg)
	payload := mcpPayload("tools/list", map[string]interface{}{})

	response := mcpTestRequest(s, "/api/v1/server/mcp", http.MethodPost, payload, "", "https://attacker.example")
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign origin status = %d, want 403", response.Code)
	}
	response = mcpTestRequest(s, "/api/v1/server/mcp", http.MethodPost, payload, "", "")
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("missing bearer response = %d, %q; want 401 and Bearer challenge", response.Code, response.Header().Get("WWW-Authenticate"))
	}
	response = mcpTestRequest(s, "/api/v1/server/mcp", http.MethodPost, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`), mcpTestOperatorToken, "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Unsupported protocol version") {
		t.Fatalf("legacy initialize response = %d %q, want protocol-version error", response.Code, response.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/server/mcp", bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+mcpTestOperatorToken)
	request.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	request.Header.Set("Mcp-Method", "tools/call")
	response = httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "HeaderMismatch") {
		t.Fatalf("mismatched method response = %d %q, want HeaderMismatch", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/server/mcp", bytes.NewReader(mcpPayload("sampling/createMessage", map[string]interface{}{})))
	request.Header.Set("Authorization", "Bearer "+mcpTestOperatorToken)
	request.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	request.Header.Set("Mcp-Method", "sampling/createMessage")
	response = httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":-32601`) {
		t.Fatalf("unknown method response = %d %q, want 404/-32601", response.Code, response.Body.String())
	}
}

func TestMCPAliasesListToolsAndCallInProcess(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Token = mcpTestOperatorToken
	s := newTestServerWithConfig(t, cfg)
	for _, path := range []string{"/api/v1/server/mcp", "/api/mcp", "/mcp"} {
		response := mcpTestRequest(s, path, http.MethodPost, mcpPayload("tools/list", map[string]interface{}{}), mcpTestOperatorToken, "")
		if response.Code != http.StatusOK {
			t.Fatalf("POST %s status = %d, want 200: %s", path, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `"name":"getcountries"`) || strings.Contains(response.Body.String(), `"name":"postserverMcp"`) {
			t.Fatalf("POST %s returned incorrect tool registry: %s", path, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "preferences/tokens") {
			t.Fatalf("POST %s exposed token-management routes as MCP tools: %s", path, response.Body.String())
		}
	}

	call := mcpPayload("tools/call", map[string]interface{}{
		"name":      "getcountriesCode",
		"arguments": map[string]interface{}{"code": "TL"},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/server/mcp", bytes.NewReader(call))
	request.Header.Set("Authorization", "Bearer "+mcpTestOperatorToken)
	request.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	request.Header.Set("Mcp-Method", "tools/call")
	request.Header.Set("Mcp-Name", "getcountriesCode")
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Testland") {
		t.Fatalf("country tool call response = %d %q, want Testland result", response.Code, response.Body.String())
	}
	for key, window := range s.rateLimiter.windows {
		if key.class == "read" && len(window.events) != 0 {
			t.Fatalf("MCP tool dispatch ran API middleware a second time: %+v", key)
		}
	}
}

func TestMCPHelpAPIShape(t *testing.T) {
	s := newTestServer(t)
	response := doRequest(s, http.MethodGet, "/api/v1/server/help/mcp", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("MCP help status = %d, want 200", response.Code)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Enabled bool     `json:"enabled"`
			URL     string   `json:"url"`
			Aliases []string `json:"aliases"`
			Auth    struct {
				Scheme string `json:"scheme"`
			} `json:"auth"`
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
			Project string `json:"project"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("MCP help JSON invalid: %v", err)
	}
	if !envelope.OK || !envelope.Data.Enabled || !strings.HasSuffix(envelope.Data.URL, "/api/v1/server/mcp") || len(envelope.Data.Aliases) != 2 || envelope.Data.Auth.Scheme != "Bearer" || len(envelope.Data.Tools) == 0 || !strings.Contains(envelope.Data.Project, "getCountries") {
		t.Fatalf("MCP help data does not match the documented shape: %+v", envelope)
	}
}
