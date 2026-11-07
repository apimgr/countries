package swagger

import "testing"

func TestSpecIncludesVersionAndCoreRoutes(t *testing.T) {
	spec := Spec("1.2.3")
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
	for _, route := range []string{"/api/v1/countries", "/api/v1/countries/{code}", "/api/v1/countries/search", "/api/v1/coordinates", "/api/v1/nearby", "/api/v1/stats", "/api/v1/stats.txt", "/api/v1/count", "/api/v1/count.txt", "/api/v1/server/healthz", "/api/v1/server/about", "/api/v1/server/help", "/api/v1/server/privacy", "/api/v1/server/terms", "/api/v1/server/security", "/api/v1/server/contact", "/api/v1/server/swagger", "/api/v1/server/graphql", "/api/v1/server/reports/{report}", "/api/healthz", "/api/swagger", "/api/graphql"} {
		if _, exists := paths[route]; !exists {
			t.Errorf("OpenAPI route %q is missing", route)
		}
	}
}
