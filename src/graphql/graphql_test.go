package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/countries"
)

func testHandler(t *testing.T) *Handler {
	t.Helper()
	service, err := countries.NewService([]byte(`[
		{"name":{"common":"Testland","official":"Republic of Testland"},"cca2":"TL","cca3":"TST","capital":["Testville"],"latlng":[10,20],"timezones":["Etc/UTC"]},
		{"name":{"common":"Otherland","official":"Republic of Otherland"},"cca2":"OL","cca3":"OTH","capital":["Elsewhere"],"latlng":[30,40],"timezones":["Etc/GMT"]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(service, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestHandlerExecutesReadQueries(t *testing.T) {
	handler := testHandler(t)
	query := `{ countries { name { common official } cca2 capital timezones latlng } country(code: "TL") { name { common } } search(query: "Test") { cca2 } nearest(lat: 10, lon: 20) { distance country { name { common } } } nearby(lat: 10, lon: 20, radiusKm: 100) { distance } stats { totalCountries version } }`
	request := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(`{"query":`+quoteJSONString(query)+`}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{"Testland", "Testville", "cca2", "totalCountries", "test-version"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("response lacks %q: %s", expected, response.Body.String())
		}
	}
}

func TestHandlerRejectsInvalidRequests(t *testing.T) {
	handler := testHandler(t)
	for _, tc := range []struct {
		name, method, contentType, body string
		status                          int
	}{
		{"method", http.MethodPut, "", "", http.StatusMethodNotAllowed},
		{"media type", http.MethodPost, "text/plain", `{}`, http.StatusUnsupportedMediaType},
		{"empty body", http.MethodPost, "application/json", "", http.StatusBadRequest},
		{"invalid JSON", http.MethodPost, "application/json", `{`, http.StatusBadRequest},
		{"trailing JSON", http.MethodPost, "application/json", `{"query":"{ countries { cca2 } }"} {}`, http.StatusBadRequest},
		{"empty query", http.MethodPost, "application/json", `{"query":"  "}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "/api/graphql", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", tc.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status = %d body=%s, want %d", response.Code, response.Body.String(), tc.status)
			}
		})
	}
}

func TestGraphQLExecutionErrorsUseGraphQLShape(t *testing.T) {
	handler := testHandler(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/server/graphql", strings.NewReader(`{"query":`+quoteJSONString(`{ missingField }`)+`}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("execution error status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var result struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string         `json:"message"`
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode GraphQL error response: %v", err)
	}
	if string(result.Data) != "null" || len(result.Errors) == 0 || result.Errors[0].Extensions["code"] == nil {
		t.Fatalf("GraphQL execution error shape = %#v", result)
	}
}

func TestGraphQLGetQuery(t *testing.T) {
	handler := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/api/graphql?query=%7B%20countries%20%7B%20cca2%20%7D%20%7D", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cca2": "TL"`) {
		t.Fatalf("GET query status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.HasSuffix(response.Body.String(), "\n") {
		t.Fatal("GraphQL JSON response must end with one newline")
	}

	usage := httptest.NewRecorder()
	handler.ServeHTTP(usage, httptest.NewRequest(http.MethodGet, "/api/graphql", nil))
	if usage.Code != http.StatusOK || usage.Header().Get("Content-Type") != "text/plain; charset=utf-8" || !strings.Contains(usage.Body.String(), "GraphQL usage:") || !strings.HasSuffix(usage.Body.String(), "\n") {
		t.Fatalf("GET usage response status=%d content-type=%q body=%q", usage.Code, usage.Header().Get("Content-Type"), usage.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodGet, "/api/graphql?query=%7B%20countries%20%7B%20cca2%20%7D%20%7D&variables=%7B", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, invalid)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid GET variables status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGraphQLReadBridgePreservesRequestAndReturnsJSON(t *testing.T) {
	handler := testHandler(t)
	var gotPath, gotAuthorization string
	handler.SetAPIReader(func(_ context.Context, request *http.Request, path string) (json.RawMessage, error) {
		gotPath = path
		gotAuthorization = request.Header.Get("Authorization")
		return json.RawMessage(`{"ok":true,"data":{"title":"About"}}`), nil
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/server/graphql", strings.NewReader(`{"query":`+quoteJSONString(`{ read(path: "/api/v1/server/about") }`)+`}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer preserved-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"title": "About"`) {
		t.Fatalf("read bridge response status=%d body=%s", response.Code, response.Body.String())
	}
	if gotPath != "/api/v1/server/about" || gotAuthorization != "Bearer preserved-token" {
		t.Fatalf("read bridge request = path %q, authorization %q", gotPath, gotAuthorization)
	}
}

func TestGraphQLReadBridgeRejectsStateChangingAndInternalPaths(t *testing.T) {
	for _, path := range []string{"/api/v1/server/preferences/sync/import", "/api/v1/server/graphql", "/api/v1/server/swagger", "/api/v1/server/count.txt", "/api/v1/server/metrics/prometheus", "/api/v1/countries?token=secret"} {
		if _, err := validateReadPath(path); err == nil {
			t.Errorf("validateReadPath(%q) unexpectedly allowed path", path)
		}
	}
}

func TestGraphQLRequestLimit(t *testing.T) {
	handler := testHandler(t)
	body := bytes.NewReader([]byte(`{"query":"` + strings.Repeat("x", maxRequestBytes+1) + `"}`))
	request := httptest.NewRequest(http.MethodPost, "/api/graphql", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized request status = %d, want 400", response.Code)
	}
}

func TestGeneratedSchemaMatchesAnnotations(t *testing.T) {
	if schemaDefinition != SchemaSDL() {
		t.Fatal("generated GraphQL schema is stale; run go generate ./src/graphql")
	}
}

func quoteJSONString(value string) string {
	var output bytes.Buffer
	if err := json.NewEncoder(&output).Encode(value); err != nil {
		panic(err)
	}
	return strings.TrimSpace(output.String())
}
