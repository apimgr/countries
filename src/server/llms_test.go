package server

import (
	"strings"
	"testing"

	"github.com/apimgr/countries/src/config"
)

func TestHandleLLMSUsesRouteSchemaAndContactConfiguration(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Web.LLMS.IncludeSchemas = true
	cfg.Web.LLMS.CustomSections = []string{"## Operator notes\nUse the documented API limits."}
	cfg.Web.APIContact = "api@example.test"
	cfg.Web.Security.Contact = "security@example.test"
	s := newTestServerWithConfig(t, cfg)

	response := doRequest(s, "GET", "/.well-known/llms.txt", nil)
	if response.Code != 200 {
		t.Fatalf("GET /.well-known/llms.txt status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	for _, expected := range []string{
		"Base URL: http://example.com/api/v1",
		"GET /api/v1/countries — List and filter countries",
		"POST /api/v1/server/preferences/tokens — Create a linked owner token (bearer token required)",
		"\"responses\"",
		"## Operator notes",
		"API issues: api@example.test",
		"Security: security@example.test",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("llms.txt does not contain %q", expected)
		}
	}
	if strings.Contains(body, "/metrics") || strings.Contains(body, "/debug/") {
		t.Error("llms.txt includes an internal metrics or debug route")
	}
}

func TestHandleLLMSCanOmitEndpointInventory(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Web.LLMS.IncludeEndpoints = false
	s := newTestServerWithConfig(t, cfg)

	response := doRequest(s, "GET", "/llms.txt", nil)
	if response.Code != 200 {
		t.Fatalf("GET /llms.txt status = %d, want 200", response.Code)
	}
	if strings.Contains(response.Body.String(), "## Endpoints") {
		t.Error("llms.txt includes endpoints when include_endpoints is false")
	}
}
