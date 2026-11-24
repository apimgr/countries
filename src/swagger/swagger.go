// Package swagger defines the generated OpenAPI document for the public REST API.
package swagger

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/apimgr/countries/src/common/api"
	"github.com/go-chi/chi/v5"
)

// Spec builds paths from the registered router and uses operation annotations
// only to describe routes that are present in that router.
func Spec(version string, router chi.Routes) (map[string]interface{}, error) {
	country := map[string]interface{}{
		"type":     "object",
		"required": []string{"name", "cca2", "cca3", "ccn3", "capital", "region", "subregion", "currencies", "languages", "area", "flag", "flag_svg_url", "latlng", "borders", "timezones", "tld"},
		"properties": map[string]interface{}{
			"name":            map[string]interface{}{"type": "object", "required": []string{"common", "official"}, "properties": map[string]interface{}{"common": map[string]interface{}{"type": "string"}, "official": map[string]interface{}{"type": "string"}}},
			"cca2":            map[string]interface{}{"type": "string", "minLength": 2, "maxLength": 2},
			"cca3":            map[string]interface{}{"type": "string", "minLength": 3, "maxLength": 3},
			"ccn3":            map[string]interface{}{"type": "string", "minLength": 3, "maxLength": 3},
			"capital":         map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"region":          map[string]interface{}{"type": "string"},
			"subregion":       map[string]interface{}{"type": "string"},
			"currencies":      map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "object", "required": []string{"name", "symbol"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "symbol": map[string]interface{}{"type": "string"}}}},
			"languages":       map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}},
			"population":      map[string]interface{}{"type": []string{"integer", "null"}, "minimum": 0},
			"population_year": map[string]interface{}{"type": "integer", "minimum": 2019, "maximum": 2024},
			"area":            map[string]interface{}{"type": "number", "minimum": 0},
			"flag":            map[string]interface{}{"type": "string"},
			"flag_svg_url":    map[string]interface{}{"type": "string", "format": "uri"},
			"timezones":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"latlng":          map[string]interface{}{"type": "array", "minItems": 2, "maxItems": 2, "items": map[string]interface{}{"type": "number"}},
			"borders":         map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string", "minLength": 3, "maxLength": 3}},
			"tld":             map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		},
	}
	jsonResponse := func(description string) map[string]interface{} {
		return map[string]interface{}{"description": description, "content": map[string]interface{}{
			"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object"}},
			"text/plain":       map[string]interface{}{"schema": map[string]interface{}{"type": "string"}},
		}}
	}
	countryResponse := func(description string) map[string]interface{} {
		return map[string]interface{}{"description": description, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"$ref": "#/components/schemas/Country"}}}}
	}
	simpleGet := func(summary string) map[string]interface{} {
		return map[string]interface{}{"get": map[string]interface{}{"summary": summary, "responses": map[string]interface{}{"200": jsonResponse(summary)}}}
	}
	basePath := api.APIBasePath()
	annotations := map[string]interface{}{
		basePath + "/countries": map[string]interface{}{"get": map[string]interface{}{"summary": "List and filter countries", "parameters": []interface{}{
			map[string]interface{}{"name": "page", "in": "query", "schema": map[string]interface{}{"type": "integer", "minimum": 1, "default": 1}},
			map[string]interface{}{"name": "limit", "in": "query", "schema": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 1000, "default": 250}},
			map[string]interface{}{"name": "q", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
			map[string]interface{}{"name": "code", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
			map[string]interface{}{"name": "region", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
			map[string]interface{}{"name": "currency", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
			map[string]interface{}{"name": "language", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
		}, "responses": map[string]interface{}{"200": jsonResponse("Paginated country records"), "400": jsonResponse("Invalid filters or pagination")}}},
		basePath + "/countries/{code}": map[string]interface{}{"get": map[string]interface{}{"summary": "Read a country by ISO alpha-2, alpha-3, or numeric code", "parameters": []interface{}{map[string]interface{}{"name": "code", "in": "path", "required": true, "schema": map[string]interface{}{"type": "string", "pattern": "^([A-Za-z]{2}|[A-Za-z]{3}|[0-9]{3})$"}}}, "responses": map[string]interface{}{"200": countryResponse("Country record"), "404": jsonResponse("Country code not found")}}},
		basePath + "/countries/search": map[string]interface{}{"get": map[string]interface{}{"summary": "Search countries by any supported field", "parameters": []interface{}{
			map[string]interface{}{"name": "q", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
			map[string]interface{}{"name": "code", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
			map[string]interface{}{"name": "region", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
			map[string]interface{}{"name": "currency", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
			map[string]interface{}{"name": "language", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
		}, "responses": map[string]interface{}{"200": jsonResponse("Matching countries"), "400": jsonResponse("At least one valid filter is required")}}},
		basePath + "/coordinates": map[string]interface{}{
			"get":  map[string]interface{}{"summary": "Find nearest country", "parameters": []interface{}{map[string]interface{}{"name": "lat", "in": "query", "required": true, "schema": map[string]interface{}{"type": "number", "minimum": -90, "maximum": 90}}, map[string]interface{}{"name": "lon", "in": "query", "required": true, "schema": map[string]interface{}{"type": "number", "minimum": -180, "maximum": 180}}}, "responses": map[string]interface{}{"200": jsonResponse("Nearest country and distance"), "400": jsonResponse("Invalid coordinates")}},
			"post": map[string]interface{}{"summary": "Find nearest country from JSON coordinates", "requestBody": map[string]interface{}{"required": true, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object", "required": []string{"lat", "lon"}, "properties": map[string]interface{}{"lat": map[string]interface{}{"type": "number", "minimum": -90, "maximum": 90}, "lon": map[string]interface{}{"type": "number", "minimum": -180, "maximum": 180}}}}}}, "responses": map[string]interface{}{"200": jsonResponse("Nearest country and distance"), "400": jsonResponse("Invalid coordinates")}},
		},
		basePath + "/nearby":             map[string]interface{}{"get": map[string]interface{}{"summary": "Find country centroids within a radius", "responses": map[string]interface{}{"200": jsonResponse("Nearby countries"), "400": jsonResponse("Invalid coordinates or radius")}}},
		basePath + "/stats":              map[string]interface{}{"get": map[string]interface{}{"summary": "Read dataset statistics", "responses": map[string]interface{}{"200": jsonResponse("Dataset statistics")}}},
		basePath + "/server/healthz":     map[string]interface{}{"get": map[string]interface{}{"summary": "Read service health", "responses": map[string]interface{}{"200": jsonResponse("Service health")}}},
		basePath + "/server/healthz.txt": map[string]interface{}{"get": map[string]interface{}{"summary": "Read plain-text service health", "responses": map[string]interface{}{"200": map[string]interface{}{"description": "Flattened service health", "content": map[string]interface{}{"text/plain": map[string]interface{}{"schema": map[string]interface{}{"type": "string"}}}}}}},
		basePath + "/server/about":       simpleGet("Read service information"),
		basePath + "/server/help":        simpleGet("Read API usage help"),
		basePath + "/server/help/mcp":    simpleGet("Read MCP connection help"),
		basePath + "/server/mcp":         map[string]interface{}{"post": map[string]interface{}{"summary": "Call the Model Context Protocol endpoint", "responses": map[string]interface{}{"200": jsonResponse("MCP JSON-RPC response"), "400": jsonResponse("Invalid protocol version or headers"), "401": jsonResponse("Bearer token required"), "403": jsonResponse("Origin rejected"), "404": jsonResponse("MCP method not found"), "405": jsonResponse("POST required")}}},
		basePath + "/server/privacy":     simpleGet("Read the privacy notice"),
		basePath + "/server/terms":       simpleGet("Read the service terms"),
		basePath + "/server/security":    simpleGet("Read security reporting information"),
		basePath + "/server/contact": map[string]interface{}{
			"get":  map[string]interface{}{"summary": "Read the public report form descriptor", "parameters": []interface{}{map[string]interface{}{"name": "security_id", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 64}}}, "responses": map[string]interface{}{"200": jsonResponse("Contact/report fields, CAPTCHA challenge and single-use form token"), "404": jsonResponse("Report form is disabled")}},
			"post": map[string]interface{}{"summary": "Submit or confirm a contact/security report", "requestBody": map[string]interface{}{"required": true, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object", "required": []string{"type", "form_token"}, "properties": map[string]interface{}{"type": map[string]interface{}{"type": "string", "enum": []string{"abuse", "dmca", "privacy", "security"}}, "form_token": map[string]interface{}{"type": "string"}, "step": map[string]interface{}{"type": "string", "enum": []string{"submit", "confirm", "edit"}}, "review_token": map[string]interface{}{"type": "string"}, "confirmed": map[string]interface{}{"type": "boolean"}, "captcha_id": map[string]interface{}{"type": "string"}, "captcha_answer": map[string]interface{}{"type": "string"}, "captcha_response": map[string]interface{}{"type": "string"}, "name": map[string]interface{}{"type": "string", "maxLength": 100}, "email": map[string]interface{}{"type": "string", "format": "email", "maxLength": 254}, "subject": map[string]interface{}{"type": "string", "maxLength": 200}, "message": map[string]interface{}{"type": "string", "maxLength": 5000}}}}}}, "responses": map[string]interface{}{"200": jsonResponse("Review or accepted report result"), "400": jsonResponse("Invalid form or CAPTCHA proof"), "404": jsonResponse("Report form is disabled"), "429": jsonResponse("Report rate limit exceeded")}},
		},
		basePath + "/server/dpo":                     simpleGet("Read data protection contact information"),
		basePath + "/server/preferences":             map[string]interface{}{"get": map[string]interface{}{"summary": "Read browser preferences", "responses": map[string]interface{}{"200": jsonResponse("Effective theme, language, and guest preferences")}}, "post": map[string]interface{}{"summary": "Save browser preferences", "responses": map[string]interface{}{"200": jsonResponse("Applied preferences"), "400": jsonResponse("No supplied preference value was valid")}}},
		basePath + "/server/preferences/sync/import": map[string]interface{}{"get": map[string]interface{}{"summary": "Import guest preferences from a share link", "responses": map[string]interface{}{"200": jsonResponse("Applied and ignored preference names")}}},
		basePath + "/server/preferences/sync": map[string]interface{}{
			"get":  map[string]interface{}{"summary": "Export guest preferences", "responses": map[string]interface{}{"200": jsonResponse("Current preference state, share URL, and short code")}},
			"post": map[string]interface{}{"summary": "Import guest preferences from a code", "requestBody": map[string]interface{}{"required": true, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"code": map[string]interface{}{"type": "string", "maxLength": 2048}, "theme": map[string]interface{}{"type": "string", "enum": []string{"dark", "light", "auto"}}, "lang": map[string]interface{}{"type": "string"}}}}, "application/x-www-form-urlencoded": map[string]interface{}{"schema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"code": map[string]interface{}{"type": "string", "maxLength": 2048}, "theme": map[string]interface{}{"type": "string", "enum": []string{"dark", "light", "auto"}}, "lang": map[string]interface{}{"type": "string"}}}}}}, "responses": map[string]interface{}{"200": jsonResponse("Applied preferences"), "400": jsonResponse("No supported preference value was supplied")}},
		},
		basePath + "/server/preferences/tokens": map[string]interface{}{
			"get":  map[string]interface{}{"summary": "List masked owner tokens", "responses": map[string]interface{}{"200": jsonResponse("Masked token metadata"), "401": jsonResponse("Primary owner bearer token required")}},
			"post": map[string]interface{}{"summary": "Create a linked owner token", "responses": map[string]interface{}{"201": jsonResponse("Created linked token"), "400": jsonResponse("Invalid token request"), "401": jsonResponse("Primary owner bearer token required")}},
		},
		basePath + "/server/preferences/tokens/primary": map[string]interface{}{
			"post":   map[string]interface{}{"summary": "Issue the primary owner token", "responses": map[string]interface{}{"200": jsonResponse("Issued primary token")}},
			"delete": map[string]interface{}{"summary": "Revoke the primary owner token and delete the owner, linked tokens, and resources", "requestBody": map[string]interface{}{"required": true, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object", "required": []string{"confirm"}, "properties": map[string]interface{}{"confirm": map[string]interface{}{"type": "string", "minLength": 4, "maxLength": 4, "description": "The last four characters of the primary token"}}}}}}, "responses": map[string]interface{}{"200": jsonResponse("Revoked primary owner and deleted its tokens and resources"), "400": jsonResponse("Confirmation must match the last four characters of the primary token"), "401": jsonResponse("Primary owner bearer token required"), "403": jsonResponse("Primary owner token required; linked tokens cannot manage tokens")}},
		},
		basePath + "/server/preferences/tokens/primary/reveal": map[string]interface{}{"post": map[string]interface{}{"summary": "Reveal the primary owner token", "responses": map[string]interface{}{"200": jsonResponse("Primary token"), "401": jsonResponse("Primary owner bearer token required")}}},
		basePath + "/server/preferences/tokens/primary/rotate": map[string]interface{}{"post": map[string]interface{}{"summary": "Rotate the primary owner token", "responses": map[string]interface{}{"200": jsonResponse("Rotated primary token"), "401": jsonResponse("Primary owner bearer token required")}}},
		basePath + "/server/preferences/tokens/{id}": map[string]interface{}{
			"patch":  map[string]interface{}{"summary": "Update a linked token", "parameters": []interface{}{map[string]interface{}{"name": "id", "in": "path", "required": true, "schema": map[string]interface{}{"type": "string"}}}, "responses": map[string]interface{}{"200": jsonResponse("Updated token"), "401": jsonResponse("Primary owner bearer token required"), "404": jsonResponse("Token not found")}},
			"delete": map[string]interface{}{"summary": "Revoke a linked token", "parameters": []interface{}{map[string]interface{}{"name": "id", "in": "path", "required": true, "schema": map[string]interface{}{"type": "string"}}}, "responses": map[string]interface{}{"200": jsonResponse("Revoked token"), "401": jsonResponse("Primary owner bearer token required"), "404": jsonResponse("Token not found")}},
		},
		basePath + "/server/preferences/tokens/{id}/reveal": map[string]interface{}{"post": map[string]interface{}{"summary": "Reveal a linked token", "parameters": []interface{}{map[string]interface{}{"name": "id", "in": "path", "required": true, "schema": map[string]interface{}{"type": "string"}}}, "responses": map[string]interface{}{"200": jsonResponse("Linked token"), "401": jsonResponse("Primary owner bearer token required"), "404": jsonResponse("Token not found")}}},
		basePath + "/server/preferences/tokens/{id}/rotate": map[string]interface{}{"post": map[string]interface{}{"summary": "Rotate a linked token", "parameters": []interface{}{map[string]interface{}{"name": "id", "in": "path", "required": true, "schema": map[string]interface{}{"type": "string"}}}, "responses": map[string]interface{}{"200": jsonResponse("Rotated linked token"), "401": jsonResponse("Primary owner bearer token required"), "404": jsonResponse("Token not found")}}},
		basePath + "/server/swagger":                        simpleGet("Read the OpenAPI document"),
		basePath + "/stats.txt":                             map[string]interface{}{"get": map[string]interface{}{"summary": "Read plain-text dataset statistics", "responses": map[string]interface{}{"200": map[string]interface{}{"description": "Dataset statistics", "content": map[string]interface{}{"text/plain": map[string]interface{}{"schema": map[string]interface{}{"type": "string"}}}}}}},
		basePath + "/count":                                 simpleGet("Read the country count"),
		basePath + "/count.txt":                             map[string]interface{}{"get": map[string]interface{}{"summary": "Read the plain-text country count", "responses": map[string]interface{}{"200": map[string]interface{}{"description": "Country count", "content": map[string]interface{}{"text/plain": map[string]interface{}{"schema": map[string]interface{}{"type": "string"}}}}}}},
		basePath + "/server/reports/{report}":               map[string]interface{}{"post": map[string]interface{}{"summary": "Submit a browser security report", "parameters": []interface{}{map[string]interface{}{"name": "report", "in": "path", "required": true, "schema": map[string]interface{}{"type": "string", "enum": []string{"default", "csp", "nel"}}}}, "requestBody": map[string]interface{}{"required": true, "content": map[string]interface{}{"application/csp-report": map[string]interface{}{"schema": map[string]interface{}{"type": "object"}}, "application/reports+json": map[string]interface{}{"schema": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object"}}}}}, "responses": map[string]interface{}{"204": map[string]interface{}{"description": "Report accepted"}, "413": jsonResponse("Report body is too large"), "415": jsonResponse("Unsupported report media type")}}},
		basePath + "/server/graphql":                        map[string]interface{}{"post": map[string]interface{}{"summary": "Execute a read-only GraphQL query", "responses": map[string]interface{}{"200": jsonResponse("GraphQL result"), "400": jsonResponse("Invalid GraphQL request")}}},
		"/api/healthz":                                      map[string]interface{}{"get": map[string]interface{}{"summary": "Health check compatibility alias", "deprecated": true, "responses": map[string]interface{}{"200": jsonResponse("Service health")}}},
		"/api/healthz.txt":                                  map[string]interface{}{"get": map[string]interface{}{"summary": "Plain-text health compatibility alias", "deprecated": true, "responses": map[string]interface{}{"200": map[string]interface{}{"description": "Flattened service health", "content": map[string]interface{}{"text/plain": map[string]interface{}{"schema": map[string]interface{}{"type": "string"}}}}}}},
		"/api/swagger":                                      map[string]interface{}{"get": map[string]interface{}{"summary": "OpenAPI discovery alias", "deprecated": true, "responses": map[string]interface{}{"200": jsonResponse("OpenAPI document")}}},
		"/api/graphql":                                      map[string]interface{}{"post": map[string]interface{}{"summary": "GraphQL discovery alias", "deprecated": true, "responses": map[string]interface{}{"200": jsonResponse("GraphQL result")}}},
		"/api/mcp":                                          map[string]interface{}{"post": map[string]interface{}{"summary": "MCP discovery alias", "deprecated": true, "responses": map[string]interface{}{"200": jsonResponse("MCP response")}}},
		"/mcp":                                              map[string]interface{}{"post": map[string]interface{}{"summary": "MCP root alias", "deprecated": true, "responses": map[string]interface{}{"200": jsonResponse("MCP response")}}},
		"/api/autodiscover":                                 map[string]interface{}{"get": map[string]interface{}{"summary": "Discover API and CLI versions", "responses": map[string]interface{}{"200": jsonResponse("API and CLI discovery metadata")}}},
	}
	for _, prefix := range []string{"/server/metrics", basePath + "/server/metrics", "/api/metrics", "/metrics"} {
		addMetricsAnnotations(annotations, prefix)
	}
	paths, err := generatedPaths([]chi.Routes{router}, annotations)
	if err != nil {
		return nil, err
	}
	usedOperationIDs := make(map[string]bool)
	pathNames := make([]string, 0, len(paths))
	for path := range paths {
		pathNames = append(pathNames, path)
	}
	sort.Strings(pathNames)
	for _, path := range pathNames {
		rawItem := paths[path]
		item, _ := rawItem.(map[string]interface{})
		methods := make([]string, 0, len(item))
		for method := range item {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		for _, method := range methods {
			rawOperation := item[method]
			operation, _ := rawOperation.(map[string]interface{})
			if operation == nil {
				continue
			}
			if responses, ok := operation["responses"].(map[string]interface{}); ok {
				for status, rawResponse := range responses {
					code, err := strconv.Atoi(status)
					response, valid := rawResponse.(map[string]interface{})
					content, hasContent := response["content"].(map[string]interface{})
					if err != nil || code < 400 || !valid || !hasContent {
						continue
					}
					if jsonContent, ok := content["application/json"].(map[string]interface{}); ok {
						jsonContent["schema"] = map[string]interface{}{"$ref": "#/components/schemas/Error"}
					}
				}
			}
			operationID, _ := operation["operationId"].(string)
			if operationID == "" {
				operationID = makeOperationID(method, path)
			}
			if usedOperationIDs[operationID] {
				operationID = makeOperationID(method, path) + "At" + strings.NewReplacer("/", "", "{", "", "}", "", ".", "Text").Replace(path)
			}
			for usedOperationIDs[operationID] {
				operationID += "Route"
			}
			usedOperationIDs[operationID] = true
			operation["operationId"] = operationID
			if requiresBearer(path) {
				operation["security"] = []interface{}{map[string]interface{}{"BearerAuth": []string{}}}
			}
		}
	}
	return map[string]interface{}{
		"openapi": "3.1.0",
		"info":    map[string]interface{}{"title": "Countries API", "version": version, "description": "Country data REST API and server operations. JSON is the default; API GET routes also support plain text using Accept: text/plain or a .txt suffix."},
		"servers": []interface{}{map[string]interface{}{"url": "/"}},
		"paths":   paths,
		"components": map[string]interface{}{
			"securitySchemes": map[string]interface{}{"BearerAuth": map[string]interface{}{"type": "http", "scheme": "bearer", "description": "Use the owner bearer token or operator bearer token required by the operation."}},
			"schemas": map[string]interface{}{
				"Country": country,
				"Error": map[string]interface{}{
					"type": "object", "required": []string{"ok", "error", "message"},
					"properties": map[string]interface{}{
						"ok":      map[string]interface{}{"type": "boolean", "const": false},
						"error":   map[string]interface{}{"type": "string", "pattern": "^[A-Z][A-Z0-9_]*$"},
						"message": map[string]interface{}{"type": "string"},
						"details": map[string]interface{}{"type": "object", "additionalProperties": true},
					},
				},
			},
		},
	}, nil
}

func addMetricsAnnotations(annotations map[string]interface{}, prefix string) {
	for suffix, service := range map[string]string{"": "Prometheus metrics", "/prometheus": "Prometheus metrics", "/grafana": "Grafana dashboard JSON", "/loki": "Loki streams JSON"} {
		contentType := "text/plain"
		schemaType := "string"
		if suffix == "/grafana" || suffix == "/loki" {
			contentType = "application/json"
			schemaType = "object"
		}
		path := prefix + suffix
		annotations[path] = map[string]interface{}{"get": map[string]interface{}{
			"summary": service,
			"responses": map[string]interface{}{
				"200": map[string]interface{}{"description": service, "content": map[string]interface{}{contentType: map[string]interface{}{"schema": map[string]interface{}{"type": schemaType}}}},
				"401": map[string]interface{}{"description": "Metrics bearer token required", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"$ref": "#/components/schemas/Error"}}}},
				"403": map[string]interface{}{"description": "Metrics service unavailable", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"$ref": "#/components/schemas/Error"}}}},
			},
		}}
	}
}

func generatedPaths(routers []chi.Routes, annotations map[string]interface{}) (map[string]interface{}, error) {
	paths := make(map[string]interface{})
	for _, router := range routers {
		if err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			if !isDocumentedAPIPath(route) {
				return nil
			}
			route = strings.ReplaceAll(route, "*", "{path}")
			method = strings.ToLower(method)
			if method == "*" {
				if documented, ok := annotations[route].(map[string]interface{}); ok {
					for documentedMethod := range documented {
						if isOpenAPIMethod(documentedMethod) {
							addGeneratedOperation(paths, annotations, route, documentedMethod)
						}
					}
					return nil
				}
				addGeneratedOperation(paths, annotations, route, "get")
				return nil
			}
			if documented, ok := annotations[route].(map[string]interface{}); ok {
				if _, exists := documented[method]; !exists {
					return nil
				}
			}
			if isOpenAPIMethod(method) {
				addGeneratedOperation(paths, annotations, route, method)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	for path, rawItem := range paths {
		item, _ := rawItem.(map[string]interface{})
		if operation, ok := item["get"]; ok && strings.HasPrefix(path, "/api/") && !strings.Contains(path, "/metrics") && !strings.HasSuffix(path, ".txt") {
			textPath := path + ".txt"
			if _, exists := paths[textPath]; !exists {
				paths[textPath] = map[string]interface{}{"get": cloneOperation(operation, "Plain-text response")}
			}
		}
	}
	return paths, nil
}

func isDocumentedAPIPath(route string) bool {
	return strings.HasPrefix(route, "/api/") || route == "/mcp" || route == "/metrics" || strings.HasPrefix(route, "/metrics/") || strings.HasPrefix(route, "/server/metrics") || strings.HasPrefix(route, "/debug/")
}

func addGeneratedOperation(paths, annotations map[string]interface{}, path, method string) {
	item, _ := paths[path].(map[string]interface{})
	if item == nil {
		item = make(map[string]interface{})
		paths[path] = item
	}
	if _, exists := item[method]; exists {
		return
	}
	if annotated, ok := annotations[path].(map[string]interface{}); ok {
		if operation, exists := annotated[method]; exists {
			item[method] = operation
			return
		}
	}
	errorResponse := func(description string) map[string]interface{} {
		return map[string]interface{}{
			"description": description,
			"content": map[string]interface{}{
				"application/json": map[string]interface{}{
					"schema": map[string]interface{}{"$ref": "#/components/schemas/Error"},
				},
			},
		}
	}
	operation := map[string]interface{}{
		"summary": "API operation",
		"responses": map[string]interface{}{
			"200": map[string]interface{}{
				"description": "API response",
				"content": map[string]interface{}{
					"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object"}},
					"text/plain":       map[string]interface{}{"schema": map[string]interface{}{"type": "string"}},
				},
			},
			"401": errorResponse("Authentication required"),
			"404": errorResponse("Resource not found"),
			"500": errorResponse("Internal error"),
		},
		"operationId": makeOperationID(method, path),
	}
	if strings.Contains(path, "{path}") {
		operation["parameters"] = []interface{}{map[string]interface{}{"name": "path", "in": "path", "required": true, "schema": map[string]interface{}{"type": "string"}}}
	}
	item[method] = operation
}

func cloneOperation(operation interface{}, summary string) map[string]interface{} {
	cloned := map[string]interface{}{"summary": summary, "responses": map[string]interface{}{"200": map[string]interface{}{"description": summary, "content": map[string]interface{}{"text/plain": map[string]interface{}{"schema": map[string]interface{}{"type": "string"}}}}}}
	if source, ok := operation.(map[string]interface{}); ok {
		if operationID, exists := source["operationId"]; exists {
			cloned["operationId"] = fmt.Sprint(operationID) + "Text"
		}
	}
	return cloned
}

func isOpenAPIMethod(method string) bool {
	switch method {
	case "get", "post", "put", "patch", "delete", "head", "options", "trace":
		return true
	default:
		return false
	}
}

func requiresBearer(path string) bool {
	if strings.Contains(path, "/server/preferences/tokens/primary") && !strings.HasSuffix(path, "/server/preferences/tokens/primary") {
		return true
	}
	if strings.Contains(path, "/server/preferences/tokens/{id}") || strings.HasSuffix(path, "/server/preferences/tokens") || strings.Contains(path, "/server/mcp") || path == "/api/mcp" || path == "/mcp" {
		return true
	}
	return strings.Contains(path, "/metrics") || strings.Contains(path, "/debug/")
}

func makeOperationID(method, path string) string {
	parts := strings.FieldsFunc(strings.ToLower(path), func(r rune) bool {
		return r == '/' || r == '{' || r == '}' || r == '-' || r == '.'
	})
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "api" || part == "v1" || part == "server" {
			continue
		}
		filtered = append(filtered, part)
	}
	for index := 1; index < len(filtered); index++ {
		filtered[index] = strings.ToUpper(filtered[index][:1]) + filtered[index][1:]
	}
	return strings.ToLower(method) + strings.Join(filtered, "")
}
