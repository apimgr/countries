package graphql

import (
	"bytes"
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
		{"method", http.MethodGet, "", "", http.StatusMethodNotAllowed},
		{"media type", http.MethodPost, "text/plain", `{}`, http.StatusUnsupportedMediaType},
		{"empty body", http.MethodPost, "application/json", "", http.StatusBadRequest},
		{"invalid JSON", http.MethodPost, "application/json", `{`, http.StatusBadRequest},
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

func quoteJSONString(value string) string {
	var output bytes.Buffer
	if err := json.NewEncoder(&output).Encode(value); err != nil {
		panic(err)
	}
	return strings.TrimSpace(output.String())
}
