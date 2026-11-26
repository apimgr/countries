package graphql

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/apimgr/countries/src/countries"
	graphqlgo "github.com/graph-gophers/graphql-go"
)

const maxRequestBytes = 1 << 20

type request struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName"`
	Variables     map[string]interface{} `json:"variables"`
}

type Handler struct {
	schema   *graphqlgo.Schema
	resolver *queryResolver
}

// APIReader serves one allowlisted read-only API request and returns its JSON
// response for the GraphQL JSON scalar.
type APIReader func(context.Context, *http.Request, string) (json.RawMessage, error)

type requestContextKey struct{}

func NewHandler(service *countries.Service, version string) (*Handler, error) {
	resolver := &queryResolver{service: service, version: version}
	schema, err := graphqlgo.ParseSchema(schemaDefinition, resolver)
	if err != nil {
		return nil, err
	}
	return &Handler{schema: schema, resolver: resolver}, nil
}

// SetAPIReader wires the query-only bridge to the server's mounted API router.
func (h *Handler) SetAPIReader(reader APIReader) {
	h.resolver.reader = reader
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
		return
	}
	var input request
	if r.Method == http.MethodGet {
		query := r.URL.Query()
		input.Query = query.Get("query")
		input.OperationName = query.Get("operationName")
		if rawVariables := query.Get("variables"); rawVariables != "" {
			if err := json.Unmarshal([]byte(rawVariables), &input.Variables); err != nil {
				writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid GraphQL variables")
				return
			}
		}
	} else {
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json")
			return
		}
		defer func() {
			if err := r.Body.Close(); err != nil {
				log.Printf("close GraphQL request body: %v", err)
			}
		}()
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
		if err := decoder.Decode(&input); err != nil {
			status := http.StatusBadRequest
			if err == io.EOF {
				writeError(w, status, "BAD_REQUEST", "A GraphQL request body is required")
				return
			}
			writeError(w, status, "BAD_REQUEST", "Invalid GraphQL request body")
			return
		}
		var extra interface{}
		if err := decoder.Decode(&extra); err != io.EOF {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid GraphQL request body")
			return
		}
	}
	if strings.TrimSpace(input.Query) == "" {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			if _, err := io.WriteString(w, "GraphQL usage: provide a read-only query with the query parameter.\n"); err != nil {
				log.Printf("write GraphQL usage response: %v", err)
			}
			return
		}
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "A GraphQL query is required")
		return
	}
	if len(input.Query) > maxRequestBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "GraphQL query exceeds the request limit")
		return
	}
	if r.Method == http.MethodGet && len(r.URL.RawQuery) > maxRequestBytes {
		writeError(w, http.StatusRequestURITooLong, "REQUEST_TOO_LARGE", "GraphQL query exceeds the request limit")
		return
	}
	ctx := context.WithValue(r.Context(), requestContextKey{}, r)
	result := h.schema.Exec(ctx, input.Query, input.OperationName, input.Variables)
	if len(result.Errors) > 0 {
		if len(result.Data) == 0 {
			result.Data = json.RawMessage("null")
		}
		for _, queryError := range result.Errors {
			if queryError.Extensions == nil {
				queryError.Extensions = make(map[string]interface{})
			}
			if _, exists := queryError.Extensions["code"]; !exists {
				queryError.Extensions["code"] = "GRAPHQL_EXECUTION_ERROR"
			}
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func requestFromContext(ctx context.Context) *http.Request {
	request, _ := ctx.Value(requestContextKey{}).(*http.Request)
	return request
}

func validateReadPath(rawPath string) (string, error) {
	parsed, err := url.ParseRequestURI(rawPath)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" || parsed.Path == "" {
		return "", http.ErrNotSupported
	}
	path := parsed.Path
	if path != "/api/autodiscover" && !strings.HasPrefix(path, "/api/v1/") {
		return "", http.ErrNotSupported
	}
	if strings.Contains(path, "..") || strings.Contains(path, "//") || strings.HasSuffix(path, ".txt") {
		return "", http.ErrNotSupported
	}
	allowed := map[string]bool{
		"/api/autodiscover": true,
		"/api/v1/countries": true, "/api/v1/countries/search": true,
		"/api/v1/coordinates": true, "/api/v1/nearby": true, "/api/v1/stats": true, "/api/v1/count": true,
		"/api/v1/server/healthz": true, "/api/v1/server/about": true, "/api/v1/server/help": true,
		"/api/v1/server/help/mcp": true, "/api/v1/server/privacy": true, "/api/v1/server/terms": true,
		"/api/v1/server/security": true, "/api/v1/server/security/policy": true,
		"/api/v1/server/security/thanks": true, "/api/v1/server/contact": true,
		"/api/v1/server/dpo": true, "/api/v1/server/preferences": true,
		"/api/v1/server/preferences/sync": true, "/api/v1/server/preferences/tokens": true,
	}
	countryPath := strings.TrimPrefix(path, "/api/v1/countries/")
	isCountryItem := strings.HasPrefix(path, "/api/v1/countries/") && len(countryPath) > 0 && !strings.Contains(countryPath, "/")
	if !allowed[path] && !isCountryItem {
		return "", http.ErrNotSupported
	}
	if parsed.Query().Has("token") || parsed.Query().Has("access_token") || parsed.Query().Has("authorization") {
		return "", http.ErrNotSupported
	}
	return parsed.RequestURI(), nil
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		log.Printf("marshal GraphQL JSON response: %v", err)
		http.Error(w, "Unable to encode GraphQL response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(append(body, '\n')); err != nil {
		log.Printf("write GraphQL JSON response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]interface{}{
		"ok": false, "error": code, "message": message,
	})
}
