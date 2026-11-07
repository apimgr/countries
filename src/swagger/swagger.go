// Package swagger defines the generated OpenAPI document for the public REST API.
package swagger

import "github.com/apimgr/countries/src/common/api"

func Spec(version string) map[string]interface{} {
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
		return map[string]interface{}{"description": description, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object"}}}}
	}
	countryResponse := func(description string) map[string]interface{} {
		return map[string]interface{}{"description": description, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"$ref": "#/components/schemas/Country"}}}}
	}
	simpleGet := func(summary string) map[string]interface{} {
		return map[string]interface{}{"get": map[string]interface{}{"summary": summary, "responses": map[string]interface{}{"200": jsonResponse(summary)}}}
	}
	basePath := api.APIBasePath()
	paths := map[string]interface{}{
		basePath + "/countries": map[string]interface{}{"get": map[string]interface{}{"summary": "List and filter countries", "parameters": []interface{}{
			map[string]interface{}{"name": "page", "in": "query", "schema": map[string]interface{}{"type": "integer", "minimum": 1, "default": 1}},
			map[string]interface{}{"name": "limit", "in": "query", "schema": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 1000, "default": 250}},
			map[string]interface{}{"name": "q", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
			map[string]interface{}{"name": "code", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
			map[string]interface{}{"name": "region", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
			map[string]interface{}{"name": "currency", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
			map[string]interface{}{"name": "language", "in": "query", "schema": map[string]interface{}{"type": "string", "maxLength": 256}},
		}, "responses": map[string]interface{}{"200": jsonResponse("Paginated country records"), "400": jsonResponse("Invalid filters or pagination")}}},
		basePath + "/countries/{code}": map[string]interface{}{"get": map[string]interface{}{"summary": "Read a country by ISO alpha-2 code", "parameters": []interface{}{map[string]interface{}{"name": "code", "in": "path", "required": true, "schema": map[string]interface{}{"type": "string", "minLength": 2, "maxLength": 2}}}, "responses": map[string]interface{}{"200": countryResponse("Country record"), "404": jsonResponse("Country code not found")}}},
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
		basePath + "/nearby":                  map[string]interface{}{"get": map[string]interface{}{"summary": "Find country centroids within a radius", "responses": map[string]interface{}{"200": jsonResponse("Nearby countries"), "400": jsonResponse("Invalid coordinates or radius")}}},
		basePath + "/stats":                   map[string]interface{}{"get": map[string]interface{}{"summary": "Read dataset statistics", "responses": map[string]interface{}{"200": jsonResponse("Dataset statistics")}}},
		basePath + "/server/healthz":          map[string]interface{}{"get": map[string]interface{}{"summary": "Read service health", "responses": map[string]interface{}{"200": jsonResponse("Service health")}}},
		basePath + "/server/about":            simpleGet("Read service information"),
		basePath + "/server/help":             simpleGet("Read API usage help"),
		basePath + "/server/privacy":          simpleGet("Read the privacy notice"),
		basePath + "/server/terms":            simpleGet("Read the service terms"),
		basePath + "/server/security":         simpleGet("Read security reporting information"),
		basePath + "/server/contact":          simpleGet("Read project contact information"),
		basePath + "/server/swagger":          simpleGet("Read the OpenAPI document"),
		basePath + "/stats.txt":               map[string]interface{}{"get": map[string]interface{}{"summary": "Read plain-text dataset statistics", "responses": map[string]interface{}{"200": map[string]interface{}{"description": "Dataset statistics", "content": map[string]interface{}{"text/plain": map[string]interface{}{"schema": map[string]interface{}{"type": "string"}}}}}}},
		basePath + "/count":                   simpleGet("Read the country count"),
		basePath + "/count.txt":               map[string]interface{}{"get": map[string]interface{}{"summary": "Read the plain-text country count", "responses": map[string]interface{}{"200": map[string]interface{}{"description": "Country count", "content": map[string]interface{}{"text/plain": map[string]interface{}{"schema": map[string]interface{}{"type": "string"}}}}}}},
		basePath + "/server/reports/{report}": map[string]interface{}{"post": map[string]interface{}{"summary": "Submit a browser security report", "parameters": []interface{}{map[string]interface{}{"name": "report", "in": "path", "required": true, "schema": map[string]interface{}{"type": "string", "enum": []string{"default", "csp", "nel"}}}}, "requestBody": map[string]interface{}{"required": true, "content": map[string]interface{}{"application/csp-report": map[string]interface{}{"schema": map[string]interface{}{"type": "object"}}, "application/reports+json": map[string]interface{}{"schema": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object"}}}}}, "responses": map[string]interface{}{"204": map[string]interface{}{"description": "Report accepted"}, "413": jsonResponse("Report body is too large"), "415": jsonResponse("Unsupported report media type")}}},
		basePath + "/server/graphql":          map[string]interface{}{"post": map[string]interface{}{"summary": "Execute a read-only GraphQL query", "responses": map[string]interface{}{"200": jsonResponse("GraphQL result"), "400": jsonResponse("Invalid GraphQL request")}}},
		"/api/healthz":                    map[string]interface{}{"get": map[string]interface{}{"summary": "Health check compatibility alias", "deprecated": true, "responses": map[string]interface{}{"200": jsonResponse("Service health")}}},
		"/api/swagger":                    map[string]interface{}{"get": map[string]interface{}{"summary": "OpenAPI discovery alias", "deprecated": true, "responses": map[string]interface{}{"200": jsonResponse("OpenAPI document")}}},
		"/api/graphql":                    map[string]interface{}{"post": map[string]interface{}{"summary": "GraphQL discovery alias", "deprecated": true, "responses": map[string]interface{}{"200": jsonResponse("GraphQL result")}}},
	}
	return map[string]interface{}{
		"openapi":    "3.1.0",
		"info":       map[string]interface{}{"title": "Countries API", "version": version, "description": "Read-only API for the embedded country reference dataset."},
		"servers":    []interface{}{map[string]interface{}{"url": "/"}},
		"paths":      paths,
		"components": map[string]interface{}{"schemas": map[string]interface{}{"Country": country}},
	}
}
