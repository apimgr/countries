package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"

	"github.com/apimgr/countries/src/common/api"
)

func apiContentNegotiation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPIRequestPath(r.URL.Path) || isMCPRequestPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		request := r.Clone(r.Context())
		requestURL := *r.URL
		request.URL = &requestURL
		path := request.URL.Path
		txtPath := strings.HasSuffix(path, ".txt")
		explicitTextRoute := isExplicitTextAPIRoute(path)
		if txtPath && !explicitTextRoute {
			request.URL.Path = strings.TrimSuffix(path, ".txt")
			request.URL.RawPath = strings.TrimSuffix(request.URL.RawPath, ".txt")
		}
		wantsText := txtPath || strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/plain")
		if !wantsText && !strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json") {
			wantsText = isHTTPTool(r)
		}
		if !wantsText {
			next.ServeHTTP(w, request)
			return
		}

		recorder := httptest.NewRecorder()
		next.ServeHTTP(recorder, request)
		response := recorder.Result()
		body, err := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if err != nil {
			http.Error(w, "ERROR: INTERNAL_ERROR: Unable to read response\n", http.StatusInternalServerError)
			return
		}
		if closeErr != nil {
			http.Error(w, "ERROR: INTERNAL_ERROR: Unable to read response\n", http.StatusInternalServerError)
			return
		}
		if response.StatusCode < http.StatusBadRequest && isMetricsAPIPath(request.URL.Path) {
			copyAPIResponse(w, response, body)
			return
		}
		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		if !strings.Contains(contentType, "application/json") {
			if strings.Contains(contentType, "text/plain") {
				body = ensureTextNewline(body)
			}
			copyAPIResponse(w, response, body)
			return
		}
		textBody := serializeAPIText(body, response.StatusCode, r.Method, request.URL.Path)
		copyAPIResponseHeaders(w.Header(), response.Header)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Del("Content-Length")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(textBody)
	})
}

func isMCPRequestPath(path string) bool {
	return path == "/mcp" || path == "/api/mcp" || strings.HasSuffix(path, "/server/mcp")
}

func isMetricsAPIPath(path string) bool {
	for _, prefix := range []string{api.APIBasePath() + "/server/metrics", "/api/metrics"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func isExplicitTextAPIRoute(path string) bool {
	base := api.APIBasePath()
	switch path {
	case base + "/stats.txt", base + "/count.txt", base + "/server/healthz.txt":
		return true
	default:
		return false
	}
}

func isAPIRequestPath(path string) bool {
	return strings.HasPrefix(path, "/api/") || path == "/mcp"
}

func isHTTPTool(r *http.Request) bool {
	ua := strings.ToLower(r.UserAgent())
	if ua == "" {
		return true
	}
	if strings.HasPrefix(ua, "countries-cli/") || strings.Contains(ua, "lynx/") || strings.Contains(ua, "w3m/") || strings.Contains(ua, "links ") || strings.Contains(ua, "links/") || strings.Contains(ua, "elinks/") || strings.Contains(ua, "browsh/") || strings.Contains(ua, "carbonyl/") || strings.Contains(ua, "netsurf") {
		return false
	}
	for _, tool := range []string{
		"curl/", "wget/", "httpie/", "xh/", "hurl/", "libcurl/", "python-requests/", "python-urllib/", "python-httpx/", "aiohttp/", "urllib3/", "go-http-client/", "go-resty/", "fasthttp", "reqwest/", "ureq/", "hyper/", "axios/", "node-fetch/", "undici", "superagent/", "deno/", "bun/", "okhttp/", "apache-httpclient/", "java/", "guzzlehttp/", "libwww-perl/", "lwp-trivial/", "faraday", "dart/", "windowspowershell/", "postmanruntime/", "insomnia/", "bruno-runtime/", "k6/",
	} {
		if strings.Contains(ua, tool) {
			return true
		}
	}
	return false
}

func serializeAPIText(body []byte, status int, method, path string) []byte {
	if len(bytes.TrimSpace(body)) == 0 {
		if status >= 400 {
			return []byte(fmt.Sprintf("ERROR: %s: %s\n", http.StatusText(status), http.StatusText(status)))
		}
		return []byte("OK\n")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return append(bytes.TrimRight(body, "\r\n"), '\n')
	}
	if status >= 400 {
		return serializeAPIError(value, status)
	}
	root, ok := value.(map[string]interface{})
	if !ok {
		return appendTextValue(nil, value)
	}
	if path == "/api/v1/server/healthz" || path == "/api/healthz" {
		return serializeAPIObject(root, true, "")
	}
	if success, exists := root["ok"].(bool); exists && !success {
		return serializeAPIError(root, status)
	}
	data, hasData := root["data"]
	if !hasData {
		data = root
	}
	message, _ := root["message"].(string)
	if primary := apiPrimaryValue(path); primary != "" {
		if object, ok := data.(map[string]interface{}); ok {
			if result, exists := object[primary]; exists {
				return appendTextValue(nil, result)
			}
		}
	}
	if message == "" {
		message = apiDefaultMessage(method)
	}
	return serializeAPIValue(data, message)
}

func serializeAPIError(value interface{}, status int) []byte {
	code, message := http.StatusText(status), http.StatusText(status)
	if root, ok := value.(map[string]interface{}); ok {
		if text, ok := root["error"].(string); ok && text != "" {
			code = text
		}
		if text, ok := root["message"].(string); ok && text != "" {
			message = text
		}
	}
	return []byte(fmt.Sprintf("ERROR: %s: %s\n", code, message))
}

func serializeAPIValue(value interface{}, message string) []byte {
	var output strings.Builder
	switch data := value.(type) {
	case nil:
		fmt.Fprintf(&output, "OK: %s\n", message)
	case []interface{}:
		fmt.Fprintf(&output, "OK: %d items found\n", len(data))
		for index, item := range data {
			if index > 0 {
				if _, isObject := item.(map[string]interface{}); isObject {
					output.WriteByte('\n')
				}
			}
			if object, ok := item.(map[string]interface{}); ok {
				output.Write(serializeAPIObject(object, true, ""))
			} else {
				output.Write(appendTextValue(nil, item))
			}
		}
	case map[string]interface{}:
		fmt.Fprintf(&output, "OK: %s\n", message)
		output.Write(serializeAPIObject(data, true, ""))
	default:
		output.Write(appendTextValue(nil, value))
	}
	return ensureTextNewline([]byte(output.String()))
}

func serializeAPIObject(value map[string]interface{}, includeKeys bool, prefix string) []byte {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var output strings.Builder
	for _, key := range keys {
		value := value[key]
		name := key
		if prefix != "" {
			name = prefix + "." + key
		}
		if nested, ok := value.(map[string]interface{}); ok {
			output.Write(serializeAPIObject(nested, includeKeys, name))
			continue
		}
		if list, ok := value.([]interface{}); ok {
			if includeKeys {
				fmt.Fprintf(&output, "%s: ", name)
			}
			for index, item := range list {
				if index > 0 {
					output.WriteString(", ")
				}
				output.WriteString(scalarText(item))
			}
			output.WriteByte('\n')
			continue
		}
		if includeKeys {
			fmt.Fprintf(&output, "%s: ", name)
		}
		output.Write(appendTextValue(nil, value))
	}
	return []byte(output.String())
}

func appendTextValue(output []byte, value interface{}) []byte {
	return append(output, []byte(scalarText(value)+"\n")...)
}

func scalarText(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return typed
	case json.Number:
		return typed.String()
	case bool:
		return strconv.FormatBool(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

func apiPrimaryValue(path string) string {
	path = strings.TrimSuffix(path, ".txt")
	switch path {
	case "/api/v1/count", "/api/count":
		return "count"
	default:
		return ""
	}
}

func apiDefaultMessage(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead:
		return "Retrieved successfully"
	case http.MethodPost:
		return "Created successfully"
	case http.MethodPut, http.MethodPatch:
		return "Updated successfully"
	case http.MethodDelete:
		return "Deleted successfully"
	default:
		return "OK"
	}
}

func ensureTextNewline(body []byte) []byte {
	return append(bytes.TrimRight(body, "\r\n"), '\n')
}

func copyAPIResponse(w http.ResponseWriter, response *http.Response, body []byte) {
	copyAPIResponseHeaders(w.Header(), response.Header)
	w.Header().Del("Content-Length")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

func copyAPIResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
