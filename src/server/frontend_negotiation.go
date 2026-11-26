package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/apimgr/countries/src/common/api"
)

func (s *Server) frontendContentNegotiationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) ||
			isAPIRequestPath(r.URL.Path) || isMCPRequestPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		format := frontendResponseFormat(r)
		if r.URL.Path == "/server/healthz" || r.URL.Path == "/healthz" && s.config.Server.Healthz.Root.Enabled {
			next.ServeHTTP(w, r)
			return
		}
		apiPath, mapped := s.frontendAPIPath(r.URL.Path)
		if format == "html" || !mapped {
			next.ServeHTTP(w, r)
			return
		}

		request := r.Clone(r.Context())
		urlCopy := *r.URL
		request.URL = &urlCopy
		request.URL.Path = api.APIPath(apiPath)
		request.URL.RawPath = ""
		request.RequestURI = request.URL.RequestURI()
		if format == "json" {
			request.Header.Set("Accept", "application/json")
		} else {
			request.Header.Set("Accept", "text/plain")
		}
		recorder := httptest.NewRecorder()
		s.apiHandlers.ServeHTTP(recorder, request)
		response := recorder.Result()
		body, err := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if err != nil || closeErr != nil {
			http.Error(w, "Unable to read response\n", http.StatusInternalServerError)
			return
		}
		copyAPIResponseHeaders(w.Header(), response.Header)
		if format == "text" && strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "application/json") {
			body = serializeAPIText(body, response.StatusCode, r.Method, request.URL.Path)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Del("Content-Length")
		} else if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "application/json") {
			body = indentJSON(body)
		}
		w.WriteHeader(response.StatusCode)
		if r.Method != http.MethodHead {
			if _, err := w.Write(body); err != nil {
				log.Printf("write negotiated frontend response: %v", err)
			}
		}
	})
}

func frontendResponseFormat(r *http.Request) string {
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/json") {
		return "json"
	}
	if strings.Contains(accept, "text/html") {
		return "html"
	}
	if strings.Contains(accept, "text/plain") {
		return "text"
	}
	userAgent := strings.ToLower(r.UserAgent())
	if strings.HasPrefix(userAgent, "countries-cli/") {
		return "json"
	}
	for _, browser := range []string{"lynx/", "w3m/", "links/", "elinks/", "browsh/", "carbonyl/", "netsurf"} {
		if strings.Contains(userAgent, browser) {
			return "html"
		}
	}
	if isHTTPTool(r) {
		return "text"
	}
	return "html"
}

func (s *Server) frontendAPIPath(path string) (string, bool) {
	switch path {
	case "/":
		return "countries", true
	case "/search":
		return "countries/search", true
	case "/coordinates":
		return "coordinates", true
	case "/server/healthz":
		return "server/healthz", true
	case "/healthz":
		if s.config.Server.Healthz.Root.Enabled {
			return "server/healthz", true
		}
		return "", false
	case "/server/about":
		return "server/about", true
	case "/server/help":
		return "server/help", true
	case "/server/help/mcp":
		return "server/help/mcp", true
	case "/server/privacy":
		return "server/privacy", true
	case "/server/terms":
		return "server/terms", true
	case "/server/security":
		return "server/security", true
	case "/server/security/policy":
		return "server/security/policy", true
	case "/server/security/thanks":
		return "server/security/thanks", true
	case "/server/contact":
		return "server/contact", true
	case "/server/dpo":
		return "server/dpo", true
	case "/server/preferences":
		return "server/preferences", true
	case "/server/preferences/sync":
		return "server/preferences/sync", true
	default:
		return "", false
	}
}

func indentJSON(body []byte) []byte {
	var value interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return body
	}
	formatted, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return body
	}
	return append(formatted, '\n')
}
