package swagger

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestSpecIncludesVersionAndCoreRoutes(t *testing.T) {
	router := chi.NewRouter()
	for _, route := range []string{"/api/v1/countries", "/api/v1/countries/{code}", "/api/v1/countries/search", "/api/v1/coordinates", "/api/v1/nearby", "/api/v1/stats", "/api/v1/stats.txt", "/api/v1/count", "/api/v1/count.txt", "/api/v1/server/healthz", "/api/v1/server/about", "/api/v1/server/help", "/api/v1/server/help/mcp", "/api/v1/server/privacy", "/api/v1/server/terms", "/api/v1/server/security", "/api/v1/server/contact", "/api/v1/server/dpo", "/api/v1/server/preferences", "/api/v1/server/preferences/sync", "/api/v1/server/preferences/sync/import", "/api/v1/server/preferences/tokens/primary", "/api/v1/server/swagger", "/api/v1/server/graphql", "/api/v1/server/reports/{report}", "/api/healthz", "/api/swagger", "/api/graphql"} {
		if route == "/api/v1/server/preferences/tokens/primary" {
			router.Post(route, http.NotFound)
			router.Delete(route, http.NotFound)
			continue
		}
		if route == "/api/v1/server/graphql" || route == "/api/v1/server/reports/{report}" || route == "/api/graphql" {
			if route == "/api/v1/server/graphql" || route == "/api/graphql" {
				router.Get(route, http.NotFound)
			}
			router.Post(route, http.NotFound)
			continue
		}
		router.Get(route, http.NotFound)
	}
	spec, err := Spec("1.2.3", router)
	if err != nil {
		t.Fatal(err)
	}
	if spec["openapi"] != "3.1.0" {
		t.Fatalf("OpenAPI version = %v", spec["openapi"])
	}
	info, ok := spec["info"].(map[string]interface{})
	if !ok || info["version"] != "1.2.3" {
		t.Fatalf("info = %#v", spec["info"])
	}
	paths, ok := spec["paths"].(map[string]interface{})
	if !ok {
		t.Fatalf("paths has type %T", spec["paths"])
	}
	for _, route := range []string{"/api/v1/countries", "/api/v1/countries/{code}", "/api/v1/countries/search", "/api/v1/coordinates", "/api/v1/nearby", "/api/v1/stats", "/api/v1/stats.txt", "/api/v1/count", "/api/v1/count.txt", "/api/v1/server/healthz", "/api/v1/server/about", "/api/v1/server/help", "/api/v1/server/privacy", "/api/v1/server/terms", "/api/v1/server/security", "/api/v1/server/contact", "/api/v1/server/dpo", "/api/v1/server/preferences", "/api/v1/server/preferences/sync", "/api/v1/server/preferences/sync/import", "/api/v1/server/swagger", "/api/v1/server/graphql", "/api/v1/server/reports/{report}", "/api/healthz", "/api/swagger", "/api/graphql"} {
		if _, exists := paths[route]; !exists {
			t.Errorf("OpenAPI route %q is missing", route)
		}
	}
	graphqlPath := paths["/api/v1/server/graphql"].(map[string]interface{})
	if graphqlPath["get"] == nil || graphqlPath["post"] == nil {
		t.Fatalf("GraphQL operations = %#v, want GET and POST", graphqlPath)
	}
	graphqlAlias := paths["/api/graphql"].(map[string]interface{})
	if graphqlAlias["get"] == nil || graphqlAlias["post"] == nil {
		t.Fatalf("GraphQL alias operations = %#v, want GET and POST", graphqlAlias)
	}
}

func TestSpecDocumentsPrimaryTokenRevocation(t *testing.T) {
	router := chi.NewRouter()
	router.Post("/api/v1/server/preferences/tokens/primary", http.NotFound)
	router.Delete("/api/v1/server/preferences/tokens/primary", http.NotFound)
	spec, err := Spec("1.0.0", router)
	if err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]interface{})
	path := paths["/api/v1/server/preferences/tokens/primary"].(map[string]interface{})
	operation := path["delete"].(map[string]interface{})
	body := operation["requestBody"].(map[string]interface{})
	if body["required"] != true {
		t.Fatal("primary-token revocation request body must be required")
	}
	content := body["content"].(map[string]interface{})
	schema := content["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	if got := schema["required"].([]string); len(got) != 1 || got[0] != "confirm" {
		t.Fatalf("required confirmation properties = %#v", schema["required"])
	}
	responses := operation["responses"].(map[string]interface{})
	for _, code := range []string{"200", "400", "401", "403"} {
		if _, exists := responses[code]; !exists {
			t.Errorf("primary-token revocation response %s is missing", code)
		}
	}
}

func TestSpecPathsComeFromRegisteredRoutes(t *testing.T) {
	router := chi.NewRouter()
	router.Get("/api/v1/countries", http.NotFound)
	router.Post("/api/v1/coordinates", http.NotFound)
	router.Get("/api/healthz", http.NotFound)
	router.Handle("/mcp", http.HandlerFunc(http.NotFound))
	router.Get("/api/v1/server/metrics/prometheus", http.NotFound)
	router.Get("/api/metrics/prometheus", http.NotFound)
	spec, err := Spec("1.2.3", router)
	if err != nil {
		t.Fatal(err)
	}
	paths, ok := spec["paths"].(map[string]interface{})
	if !ok {
		t.Fatalf("paths has type %T", spec["paths"])
	}
	for _, route := range []string{"/api/v1/countries", "/api/v1/countries.txt", "/api/v1/coordinates", "/api/healthz", "/mcp", "/api/v1/server/metrics/prometheus", "/api/metrics/prometheus"} {
		if _, exists := paths[route]; !exists {
			t.Errorf("registered route %q is missing from generated OpenAPI", route)
		}
	}
	mcp := paths["/mcp"].(map[string]interface{})
	if len(mcp) != 1 || mcp["post"] == nil {
		t.Fatalf("MCP alias methods = %#v, want POST only", mcp)
	}
	if _, exists := paths["/api/v1/server/preferences/tokens"]; exists {
		t.Fatal("unregistered annotated route leaked into generated OpenAPI")
	}
	countries := paths["/api/v1/countries"].(map[string]interface{})
	if _, exists := countries["get"]; !exists {
		t.Fatal("GET operation is missing")
	}
	if _, exists := countries["post"]; exists {
		t.Fatal("unregistered POST operation was generated")
	}
	for _, route := range []string{"/api/v1/server/metrics/prometheus", "/api/metrics/prometheus"} {
		item := paths[route].(map[string]interface{})
		operation := item["get"].(map[string]interface{})
		if len(operation["security"].([]interface{})) == 0 {
			t.Errorf("%s does not declare bearer authentication", route)
		}
	}
}
