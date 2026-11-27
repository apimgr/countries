package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/swagger"
	"github.com/go-chi/chi/v5"
)

const mcpProtocolVersion = "2026-07-28"

type pageRegistryEntry struct {
	Path           string
	Hub            string
	TitleKey       string
	DescriptionKey string
	Order          int
	Enabled        bool
}

type pageRegistryDefinition struct {
	Path           string
	Hub            string
	TitleKey       string
	DescriptionKey string
	Order          int
	Enabled        func(*config.Config) bool
}

var preferencesHelpPageRegistry = []pageRegistryDefinition{
	{Path: "/server/preferences/tokens", Hub: "/server/preferences", TitleKey: "tokens.title", DescriptionKey: "tokens.description", Order: 10, Enabled: func(*config.Config) bool { return true }},
	{Path: "/server/preferences/sync", Hub: "/server/preferences", TitleKey: "preferences.sync", DescriptionKey: "preferences.sync_description", Order: 20, Enabled: func(*config.Config) bool { return true }},
	{Path: "/server/help/mcp", Hub: "/server/help", TitleKey: "help.mcp.title", DescriptionKey: "help.mcp.description", Order: 10, Enabled: func(*config.Config) bool { return true }},
}

func registeredHubPages(cfg *config.Config) []pageRegistryEntry {
	pages := make([]pageRegistryEntry, 0)
	for _, page := range preferencesHelpPageRegistry {
		if page.Enabled != nil && page.Enabled(cfg) {
			pages = append(pages, pageRegistryEntry{
				Path: page.Path, Hub: page.Hub, TitleKey: page.TitleKey,
				DescriptionKey: page.DescriptionKey, Order: page.Order, Enabled: true,
			})
		}
	}
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Order < pages[j].Order })
	return pages
}

func (s *Server) registeredPages(hub string) []pageRegistryEntry {
	allPages := registeredHubPages(s.config)
	pages := make([]pageRegistryEntry, 0, len(allPages))
	for _, page := range allPages {
		if page.Hub == hub {
			pages = append(pages, page)
		}
	}
	return pages
}

func (s *Server) registeredPage(path string) (pageRegistryEntry, bool) {
	for _, page := range registeredHubPages(s.config) {
		if page.Path == path {
			return page, true
		}
	}
	return pageRegistryEntry{}, false
}

func (s *Server) registerFrontendPageRoutes(r chi.Router) {
	for _, page := range registeredHubPages(s.config) {
		if !page.Enabled {
			continue
		}
		switch page.Path {
		case "/server/preferences/tokens":
			r.Get(page.Path, s.handleTokensPage)
		case "/server/preferences/sync":
			r.Get(page.Path, s.handlePreferencesSyncPage)
		case "/server/help/mcp":
			r.Get(page.Path, s.handleInfoPage)
		}
	}
}

func (s *Server) registerAPIPageRoutes(r chi.Router) {
	for _, page := range registeredHubPages(s.config) {
		if !page.Enabled {
			continue
		}
		path := strings.TrimPrefix(page.Path, api.APIBasePath())
		switch page.Path {
		case "/server/preferences/tokens":
			r.Get(path, s.handleTokensAPI)
		case "/server/preferences/sync":
			r.Get(path, s.handlePreferencesSyncPage)
		case "/server/help/mcp":
			r.Get(path, s.handleMCPHelpAPI)
		}
	}
}

func (s *Server) registeredPagePayload(hub, lang string) []map[string]string {
	pages := s.registeredPages(hub)
	result := make([]map[string]string, 0, len(pages))
	for _, page := range pages {
		result = append(result, map[string]string{
			"path":  page.Path,
			"title": i18n.Translate(lang, page.TitleKey),
		})
	}
	return result
}

type mcpTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Annotations map[string]bool        `json:"annotations"`
	Path        string                 `json:"-"`
	Method      string                 `json:"-"`
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Meta    struct {
		ProtocolVersion string `json:"io.modelcontextprotocol/protocolVersion"`
	} `json:"_meta"`
}

func (s *Server) mcpHelpData(r *http.Request) MCPHelpData {
	basePath := api.APIBasePath()
	project := strings.TrimSpace(s.config.Server.Pages.Help.MCP)
	if project == "" {
		project = config.DefaultMCPHelpProjectContent()
	}
	return MCPHelpData{
		Enabled:          true,
		URL:              s.BuildURL(r, basePath+"/server/mcp"),
		Aliases:          []string{s.BuildURL(r, "/api/mcp"), s.BuildURL(r, "/mcp")},
		AuthScheme:       "Bearer",
		ProtocolVersions: []string{mcpProtocolVersion},
		Tools:            s.mcpToolSummaries(),
		Project:          markdownToHTML(project),
		ProjectMarkdown:  project,
	}
}

func (s *Server) handleMCPHelpAPI(w http.ResponseWriter, r *http.Request) {
	data := s.mcpHelpData(r)
	tools := make([]map[string]string, 0, len(data.Tools))
	for _, tool := range data.Tools {
		tools = append(tools, map[string]string{"name": tool.Name, "description": tool.Description})
	}
	s.jsonResponse(w, map[string]interface{}{
		"ok": true,
		"data": map[string]interface{}{
			"enabled": data.Enabled,
			"url":     data.URL,
			"aliases": data.Aliases,
			"auth":    map[string]string{"scheme": data.AuthScheme},
			"tools":   tools,
			"project": data.ProjectMarkdown,
		},
	})
}

func (s *Server) mcpToolSummaries() []MCPHelpTool {
	tools := s.mcpTools()
	result := make([]MCPHelpTool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, MCPHelpTool{Name: tool.Name, Description: tool.Description})
	}
	return result
}

func (s *Server) mcpTools() []mcpTool {
	spec, err := swagger.Spec(s.version, s.router)
	if err != nil {
		log.Printf("MCP tool registry generation failed: %v", err)
		return nil
	}
	paths, _ := spec["paths"].(map[string]interface{})
	pathNames := make([]string, 0, len(paths))
	for path := range paths {
		pathNames = append(pathNames, path)
	}
	sort.Strings(pathNames)
	var tools []mcpTool
	for _, path := range pathNames {
		if !strings.HasPrefix(path, api.APIBasePath()+"/") || strings.HasSuffix(path, ".txt") || strings.Contains(path, "/server/mcp") || strings.Contains(path, "/server/preferences/token") || strings.Contains(path, "/server/metrics") || strings.Contains(path, "/server/reports/") || strings.Contains(path, "/debug/") {
			continue
		}
		item, _ := paths[path].(map[string]interface{})
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			op, _ := item[method].(map[string]interface{})
			if op == nil {
				continue
			}
			name, _ := op["operationId"].(string)
			if name == "" {
				name = operationID(method, path)
			}
			description, _ := op["description"].(string)
			if description == "" {
				description, _ = op["summary"].(string)
			}
			schema := toolInputSchema(op)
			tools = append(tools, mcpTool{Name: name, Description: description, InputSchema: schema, Annotations: toolAnnotations(method), Path: path, Method: strings.ToUpper(method)})
		}
	}
	return tools
}

func toolAnnotations(method string) map[string]bool {
	annotations := map[string]bool{}
	switch strings.ToUpper(method) {
	case http.MethodGet:
		annotations["readOnlyHint"] = true
	case http.MethodDelete:
		annotations["destructiveHint"] = true
		annotations["idempotentHint"] = true
	case http.MethodPut:
		annotations["readOnlyHint"] = false
		annotations["idempotentHint"] = true
	case http.MethodPost, http.MethodPatch:
		annotations["readOnlyHint"] = false
	}
	return annotations
}

func operationID(method, path string) string {
	parts := strings.FieldsFunc(strings.ToLower(path), func(r rune) bool { return r == '/' || r == '{' || r == '}' || r == '-' || r == '.' })
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "api" || part == "v1" || part == "server" {
			continue
		}
		filtered = append(filtered, part)
	}
	for i := 1; i < len(filtered); i++ {
		filtered[i] = strings.ToUpper(filtered[i][:1]) + filtered[i][1:]
	}
	name := strings.Join(filtered, "")
	if name == "" {
		name = "root"
	}
	return strings.ToLower(method) + name
}

func toolInputSchema(operation map[string]interface{}) map[string]interface{} {
	properties := make(map[string]interface{})
	required := make([]string, 0)
	parameters, _ := operation["parameters"].([]interface{})
	for _, item := range parameters {
		parameter, _ := item.(map[string]interface{})
		name, _ := parameter["name"].(string)
		if name == "" {
			continue
		}
		schema, _ := parameter["schema"].(map[string]interface{})
		properties[name] = schema
		if parameter["required"] == true {
			required = append(required, name)
		}
	}
	if requestBody, ok := operation["requestBody"].(map[string]interface{}); ok {
		content, _ := requestBody["content"].(map[string]interface{})
		jsonContent, _ := content["application/json"].(map[string]interface{})
		bodySchema, _ := jsonContent["schema"].(map[string]interface{})
		properties["body"] = bodySchema
		if requestBody["required"] == true {
			required = append(required, "body")
		}
	}
	return map[string]interface{}{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.mcpError(w, nil, -32000, "Method not allowed", http.StatusMethodNotAllowed, nil)
		return
	}
	if !s.mcpOriginAllowed(r) {
		s.mcpError(w, nil, -32003, "Forbidden origin", http.StatusForbidden, nil)
		return
	}
	_, valid := s.validMCPBearer(r)
	if !valid {
		w.Header().Set("WWW-Authenticate", "Bearer")
		s.mcpError(w, nil, -32001, "Unauthorized", http.StatusUnauthorized, nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.mcpError(w, nil, -32700, "Parse error", http.StatusBadRequest, nil)
		return
	}
	var request mcpRequest
	if json.Unmarshal(body, &request) != nil || request.JSONRPC != "2.0" || request.Method == "" {
		s.mcpError(w, nil, -32700, "Parse error", http.StatusBadRequest, nil)
		return
	}
	s.auditMCPRequest(r, request.Method)
	protocolVersion := r.Header.Get("MCP-Protocol-Version")
	if request.Method == "initialize" || protocolVersion != "" && protocolVersion != mcpProtocolVersion {
		s.mcpError(w, request.ID, -32002, "Unsupported protocol version", http.StatusBadRequest, map[string]interface{}{"supported": []string{mcpProtocolVersion}})
		return
	}
	if r.Header.Get("Mcp-Method") != request.Method || request.Meta.ProtocolVersion != mcpProtocolVersion {
		s.mcpError(w, request.ID, -32020, "HeaderMismatch", http.StatusBadRequest, nil)
		return
	}
	var params map[string]json.RawMessage
	if len(request.Params) > 0 && json.Unmarshal(request.Params, &params) != nil {
		s.mcpError(w, request.ID, -32602, "Invalid params", http.StatusBadRequest, nil)
		return
	}
	if request.Method == "tools/call" {
		var call struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if json.Unmarshal(request.Params, &call) != nil || call.Name == "" {
			s.mcpError(w, request.ID, -32602, "Invalid params", http.StatusBadRequest, nil)
			return
		}
		name, mismatch := decodeMCPName(r.Header.Get("Mcp-Name"))
		if mismatch || name != call.Name {
			s.mcpError(w, request.ID, -32020, "HeaderMismatch", http.StatusBadRequest, nil)
			return
		}
		for _, tool := range s.mcpTools() {
			if tool.Name == call.Name {
				s.dispatchMCPTool(w, r, request.ID, tool, call.Arguments)
				return
			}
		}
		s.mcpError(w, request.ID, -32602, "Unknown tool", http.StatusBadRequest, nil)
		return
	}
	if request.Method == "resources/read" || request.Method == "prompts/get" {
		field := "name"
		if request.Method == "resources/read" {
			field = "uri"
		}
		var name string
		_ = json.Unmarshal(params[field], &name)
		headerName, mismatch := decodeMCPName(r.Header.Get("Mcp-Name"))
		if mismatch || name == "" || headerName != name {
			s.mcpError(w, request.ID, -32020, "HeaderMismatch", http.StatusBadRequest, nil)
			return
		}
	}
	if request.Method == "tools/list" {
		tools := s.mcpTools()
		for _, tool := range tools {
			delete(tool.InputSchema, "additionalProperties")
		}
		result := map[string]interface{}{"tools": tools}
		s.mcpResult(w, request.ID, result)
		return
	}
	s.mcpError(w, request.ID, -32601, "Method not found", http.StatusNotFound, nil)
}

func decodeMCPName(value string) (string, bool) {
	if strings.HasPrefix(value, "=?base64?") && strings.HasSuffix(value, "?=") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(value, "=?base64?"), "?="))
		if err != nil {
			return "", true
		}
		return string(decoded), false
	}
	return value, false
}

func (s *Server) mcpOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	return s.corsOriginAllowed(r)
}

func (s *Server) validMCPBearer(r *http.Request) (bool, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return false, false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if token == "" {
		return false, false
	}
	if s.validOperatorToken(token) {
		return true, true
	}
	if s.db == nil {
		return false, false
	}
	_, err := database.LookupOwnerToken(r.Context(), s.db, token, time.Now())
	return false, err == nil
}

func (s *Server) auditMCPRequest(r *http.Request, method string) {
	switch method {
	case "initialize", "tools/list", "tools/call", "resources/read", "prompts/get":
	default:
		method = "unknown"
	}
	s.recordAudit(r, "mcp", "mcp.request", "endpoint", "", map[string]any{"source": "mcp", "method": method}, true)
}

func (s *Server) dispatchMCPTool(w http.ResponseWriter, r *http.Request, id json.RawMessage, tool mcpTool, arguments map[string]interface{}) {
	path := tool.Path
	for key, value := range arguments {
		placeholder := "{" + key + "}"
		if strings.Contains(path, placeholder) {
			path = strings.ReplaceAll(path, placeholder, url.PathEscape(fmt.Sprint(value)))
		}
	}
	if strings.Contains(path, "{") {
		s.mcpError(w, id, -32602, "Missing required path parameter", http.StatusBadRequest, nil)
		return
	}
	spec, err := swagger.Spec(s.version, s.router)
	if err != nil {
		s.mcpError(w, id, -32603, "API registry unavailable", http.StatusInternalServerError, nil)
		return
	}
	operation := operationFor(spec, tool.Path, strings.ToLower(tool.Method))
	query := make(url.Values)
	parameters, _ := operation["parameters"].([]interface{})
	for _, raw := range parameters {
		parameter, _ := raw.(map[string]interface{})
		if parameter["in"] != "query" {
			continue
		}
		key, _ := parameter["name"].(string)
		if value, ok := arguments[key]; ok {
			query.Set(key, fmt.Sprint(value))
		}
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var body io.Reader
	if value, ok := arguments["body"]; ok && value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			s.mcpError(w, id, -32602, "Invalid arguments", http.StatusBadRequest, nil)
			return
		}
		body = bytes.NewReader(encoded)
	} else if tool.Method == http.MethodPost && operation["requestBody"] != nil {
		s.mcpError(w, id, -32602, "Missing request body", http.StatusBadRequest, nil)
		return
	}
	request := httptest.NewRequest(tool.Method, path, body)
	request.Header = r.Header.Clone()
	request.Host = r.Host
	request.RemoteAddr = r.RemoteAddr
	request = request.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, nil))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	if s.apiHandlers == nil {
		s.mcpError(w, id, -32603, "API handler unavailable", http.StatusInternalServerError, nil)
		return
	}
	s.apiHandlers.ServeHTTP(response, request)
	var result interface{}
	if json.Unmarshal(response.Body.Bytes(), &result) != nil {
		result = strings.TrimSpace(response.Body.String())
	}
	if response.Code < 200 || response.Code >= 300 {
		s.mcpResult(w, id, map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": response.Body.String()}}, "isError": true})
		return
	}
	s.mcpResult(w, id, map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": stringifyMCPResult(result)}}})
}

func operationFor(spec map[string]interface{}, path, method string) map[string]interface{} {
	paths, _ := spec["paths"].(map[string]interface{})
	item, _ := paths[path].(map[string]interface{})
	op, _ := item[method].(map[string]interface{})
	return op
}

func stringifyMCPResult(value interface{}) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func (s *Server) mcpResult(w http.ResponseWriter, id json.RawMessage, result interface{}) {
	s.mcpWrite(w, http.StatusOK, map[string]interface{}{"jsonrpc": "2.0", "id": rawJSONValue(id), "result": result})
}

func (s *Server) mcpError(w http.ResponseWriter, id json.RawMessage, code int, message string, status int, data interface{}) {
	errorBody := map[string]interface{}{"code": code, "message": message}
	if data != nil {
		errorBody["data"] = data
	}
	s.mcpWrite(w, status, map[string]interface{}{"jsonrpc": "2.0", "id": rawJSONValue(id), "error": errorBody})
}

func rawJSONValue(value json.RawMessage) interface{} {
	if len(value) == 0 {
		return nil
	}
	var result interface{}
	if json.Unmarshal(value, &result) != nil {
		return nil
	}
	return result
}

func (s *Server) mcpWrite(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
