package graphql

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	graphqlgo "github.com/graph-gophers/graphql-go"
	"github.com/apimgr/countries/src/countries"
)

const maxRequestBytes = 1 << 20

type request struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName"`
	Variables     map[string]interface{} `json:"variables"`
}

type Handler struct {
	schema *graphqlgo.Schema
}

func NewHandler(service *countries.Service, version string) (*Handler, error) {
	schema, err := graphqlgo.ParseSchema(schemaDefinition, &queryResolver{service: service, version: version})
	if err != nil {
		return nil, err
	}
	return &Handler{schema: schema}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json")
		return
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	var input request
	if err := decoder.Decode(&input); err != nil {
		status := http.StatusBadRequest
		if err == io.EOF {
			writeError(w, status, "BAD_REQUEST", "A GraphQL request body is required")
			return
		}
		writeError(w, status, "BAD_REQUEST", "Invalid GraphQL request body")
		return
	}
	if strings.TrimSpace(input.Query) == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "A GraphQL query is required")
		return
	}
	if len(input.Query) > maxRequestBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "GraphQL query exceeds the request limit")
		return
	}
	result := h.schema.Exec(r.Context(), input.Query, input.OperationName, input.Variables)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		return
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok": false, "error": code, "message": message,
	})
}
