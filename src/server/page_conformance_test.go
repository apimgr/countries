package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/testutil"
	"github.com/go-chi/chi/v5"
	"golang.org/x/net/html"
)

func TestRegisteredPagesConformToHTMLRules(t *testing.T) {
	cfg := config.DefaultConfig()
	delete(cfg.Server.Branding.Content, "header")
	cfg.Server.GeoIP.Enabled = false
	cfg.Server.Healthz.Root.Enabled = true
	cfg.Server.RateLimit.Read.Requests = 1000
	cfg.Server.RateLimit.GlobalBurst = 2000
	service, err := countries.NewService([]byte(fixtureJSON))
	if err != nil {
		t.Fatalf("countries.NewService() error = %v", err)
	}
	db, err := database.Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	statusToken := "page-conformance-status-token"
	statusHash := sha256.Sum256([]byte(statusToken))
	if _, err := db.Exec(`INSERT INTO security_reports(tracking_id,severity,component,body_enc,body_alg,token_hash) VALUES(?,?,?,?,?,?)`, "sec_0123456789abcdef", "high", "api", []byte("encrypted"), "aes-256-gcm", hex.EncodeToString(statusHash[:])); err != nil {
		t.Fatalf("insert security report for rendered status page: %v", err)
	}
	server := New(service, cfg, "0.0.0.0", "8080", "test-version", "2024-01-01", "abc123", db)
	server.SetMetricsDataDir(testutil.TempDir(t))
	server.SetSchedulerRunning(true)
	pages := []string{
		"/", "/search", "/coordinates", "/server/healthz", "/healthz", "/server/about",
		"/server/help", "/server/privacy", "/server/terms", "/server/security",
		"/server/security/policy", "/server/security/thanks", "/server/contact",
		"/server/dpo", "/server/preferences", "/server/docs/swagger",
		"/server/docs/graphql", "/server/security/report/sec_0123456789abcdef?token=" + url.QueryEscape(statusToken),
	}
	registeredPages := registeredHubPages(server.config)
	for _, page := range registeredPages {
		pages = append(pages, page.Path)
	}
	pages = uniqueStrings(pages)
	for _, path := range pages {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Accept", "text/html")
			response := httptest.NewRecorder()
			server.router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("GET %s returned %d, want 200", path, response.Code)
			}
			document, err := html.Parse(strings.NewReader(response.Body.String()))
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			assertPageHTMLConformance(t, server, path, document)
		})
	}
	assertRegisteredPagesInHubsAndHelpAPI(t, server, registeredPages)
	assertPreferenceAndHelpRoutesAreRegisteredOrActions(t, server, registeredPages)
	assertOperatorHeaderContentIsAppended(t, server)

	for _, status := range []int{
		http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/error", nil)
			request.Header.Set("Accept", "text/html")
			response := httptest.NewRecorder()
			server.errorResponse(response, request, http.StatusText(status), status)
			document, err := html.Parse(strings.NewReader(response.Body.String()))
			if err != nil {
				t.Fatalf("parse %s error page: %v", http.StatusText(status), err)
			}
			if response.Code != status {
				t.Errorf("error renderer returned %d, want %d", response.Code, status)
			}
			assertPageHTMLConformance(t, server, "/error", document)
		})
	}
}

func assertRegisteredPagesInHubsAndHelpAPI(t *testing.T, server *Server, pages []pageRegistryEntry) {
	t.Helper()
	helpPagePaths := make([]string, 0)
	for _, page := range pages {
		hubRequest := httptest.NewRequest(http.MethodGet, page.Hub, nil)
		hubRequest.Header.Set("Accept", "text/html")
		hubResponse := httptest.NewRecorder()
		server.router.ServeHTTP(hubResponse, hubRequest)
		if hubResponse.Code != http.StatusOK {
			t.Errorf("registered page hub %s returned %d, want 200", page.Hub, hubResponse.Code)
			continue
		}
		if countHTMLAttributeValue(t, hubResponse.Body.String(), "href", page.Path) != 1 {
			t.Errorf("registered page %s does not appear exactly once on hub %s", page.Path, page.Hub)
		}
		if page.Hub == "/server/help" {
			helpPagePaths = append(helpPagePaths, page.Path)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/server/help", nil)
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/server/help returned %d, want 200", response.Code)
	}
	var envelope struct {
		Data struct {
			Pages []struct {
				Path string `json:"path"`
			} `json:"pages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode help API pages: %v", err)
	}
	if len(envelope.Data.Pages) != len(helpPagePaths) {
		t.Errorf("help API lists %d pages, want exactly %d registered help pages", len(envelope.Data.Pages), len(helpPagePaths))
	}
	for index, expectedPath := range helpPagePaths {
		if index >= len(envelope.Data.Pages) {
			break
		}
		if got := envelope.Data.Pages[index].Path; got != expectedPath {
			t.Errorf("help API page %d = %q, want registered page %q", index, got, expectedPath)
		}
	}
}

func assertPreferenceAndHelpRoutesAreRegisteredOrActions(t *testing.T, server *Server, pages []pageRegistryEntry) {
	t.Helper()
	registered := make(map[string]bool, len(pages))
	for _, page := range pages {
		registered[page.Path] = true
	}
	err := chi.Walk(server.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/server/preferences/") && !strings.HasPrefix(route, "/server/help/") {
			return nil
		}
		if registered[route] || method == http.MethodPost || route == "/server/preferences/sync/import" && method == http.MethodGet {
			return nil
		}
		t.Errorf("unregistered preferences/help route %s %s is not a POST action", method, route)
		return nil
	})
	if err != nil {
		t.Fatalf("walk server routes: %v", err)
	}
	for _, path := range []string{"/server/preferences/unknown", "/server/help/unknown"} {
		response := httptest.NewRecorder()
		server.router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s returned %d, want 404", path, response.Code)
		}
	}
}

func countHTMLAttributeValue(t *testing.T, source, attribute, value string) int {
	t.Helper()
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatalf("parse HTML for attribute count: %v", err)
	}
	count := 0
	walkHTML(document, func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" && htmlAttribute(node, attribute) == value {
			count++
		}
	})
	return count
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func assertPageHTMLConformance(t *testing.T, server *Server, pagePath string, document *html.Node) {
	t.Helper()
	mainCount := countHTMLNodes(document, "main")
	h1Count := countHTMLNodes(document, "h1")
	if mainCount != 1 || h1Count != 1 {
		t.Errorf("page has %d main elements and %d h1 elements, want one each", mainCount, h1Count)
	}
	for _, heading := range findHTMLNodes(document, "h1") {
		if (strings.HasPrefix(pagePath, "/server/") || pagePath == "/error") && !hasHTMLClass(heading, "page-title") {
			t.Error("page h1 does not use .page-title")
		}
	}

	focusable := firstFocusableHTMLNodes(document)
	if len(focusable) < 2 || !hasHTMLClass(focusable[0], "skip-link") || !hasHTMLClass(focusable[1], "skip-link") {
		t.Error("the two skip links are not the first focusable elements")
	}

	var labels []string
	var controls []*html.Node
	var breadcrumbs []*html.Node
	var tables []*html.Node
	var links []*html.Node
	var inlineCSS bool
	walkHTML(document, func(node *html.Node) {
		if node.Type == html.ElementNode {
			if node.Data == "style" || (node.Data == "script" && firstTextChild(node) != "" && !strings.EqualFold(htmlAttribute(node, "type"), "application/ld+json")) {
				inlineCSS = true
			}
			if node.Data == "label" {
				for _, attribute := range node.Attr {
					if attribute.Key == "for" {
						labels = append(labels, attribute.Val)
					}
				}
			}
			switch node.Data {
			case "input", "select", "textarea":
				if !isHiddenHTMLControl(node) {
					controls = append(controls, node)
				}
			case "nav":
				if hasHTMLClass(node, "breadcrumb") {
					breadcrumbs = append(breadcrumbs, node)
				}
			case "table":
				tables = append(tables, node)
			case "a":
				links = append(links, node)
			}
			imageHasAlt := false
			for _, attribute := range node.Attr {
				if strings.EqualFold(attribute.Key, "style") || strings.HasPrefix(strings.ToLower(attribute.Key), "on") {
					inlineCSS = true
				}
				if node.Data == "img" && attribute.Key == "alt" {
					imageHasAlt = true
				}
			}
			if node.Data == "img" && !imageHasAlt {
				t.Error("image has no alt attribute")
			}
		}
	})
	if inlineCSS {
		t.Error("page contains inline CSS, inline JavaScript, or an event-handler attribute")
	}
	for _, control := range controls {
		if !hasAssociatedHTMLLabel(control, labels) {
			t.Errorf("%s control has no associated label", control.Data)
		}
	}
	for _, form := range findHTMLNodes(document, "form") {
		if strings.EqualFold(htmlAttribute(form, "method"), "post") && !formHasNamedControl(form, "csrf_token") {
			t.Error("POST form does not carry csrf_token")
		}
	}
	for _, table := range tables {
		if len(findHTMLNodes(table, "thead")) == 0 {
			t.Error("table has no thead")
		}
		wrapper := table.Parent
		if wrapper == nil || !hasHTMLClass(wrapper, "table-wrapper") {
			t.Error("table is not contained by .table-wrapper")
		}
	}
	for _, breadcrumb := range breadcrumbs {
		if htmlAttribute(breadcrumb, "aria-label") == "" {
			t.Error("breadcrumb has no accessible label")
		}
	}
	assertBreadcrumbCount(t, pagePath, breadcrumbs)
	assertHeaderConformance(t, document, false)
	if countHTMLNodes(document, "footer") != 1 {
		t.Errorf("page has %d footers, want one shared footer", countHTMLNodes(document, "footer"))
	}
	for _, link := range links {
		assertInternalLinkResolves(t, server, pagePath, document, link)
	}
}

func assertBreadcrumbCount(t *testing.T, pagePath string, breadcrumbs []*html.Node) {
	t.Helper()
	want := 0
	if strings.HasPrefix(pagePath, "/server/help/") || strings.HasPrefix(pagePath, "/server/preferences/") {
		want = 1
	}
	if len(breadcrumbs) != want {
		t.Errorf("page has %d breadcrumb navs, want %d", len(breadcrumbs), want)
		return
	}
	if want == 1 {
		hub := "/server/help"
		if strings.HasPrefix(pagePath, "/server/preferences/") {
			hub = "/server/preferences"
		}
		links := findHTMLNodes(breadcrumbs[0], "a")
		if len(links) != 1 || htmlAttribute(links[0], "href") != hub {
			t.Errorf("sub-page breadcrumb must link once to %s", hub)
		}
	}
}

func assertHeaderConformance(t *testing.T, document *html.Node, operatorContent bool) {
	t.Helper()
	headers := findHTMLNodes(document, "header")
	if len(headers) < 1 {
		t.Error("page has no shared header")
		return
	}
	var siteHeader *html.Node
	for _, header := range headers {
		if hasHTMLClass(header, "header") {
			if siteHeader != nil {
				t.Error("page has more than one site header")
			}
			siteHeader = header
		}
	}
	if siteHeader == nil {
		t.Error("page has no .header element")
		return
	}
	headerChildren := elementChildren(siteHeader)
	wantChildren := 4
	if operatorContent {
		wantChildren++
	}
	if len(headerChildren) != wantChildren || headerChildren[0].Data != "a" || !hasHTMLClass(headerChildren[0], "nav-brand") ||
		headerChildren[1].Data != "nav" || !hasHTMLClass(headerChildren[1], "nav-links") ||
		headerChildren[2].Data != "label" || !hasHTMLClass(headerChildren[2], "nav-toggle") ||
		headerChildren[3].Data != "div" || !hasHTMLClass(headerChildren[3], "header-actions") {
		t.Error("shared header elements are not in the required order")
	}
	if operatorContent && len(headerChildren) == wantChildren && htmlAttribute(headerChildren[4], "id") != "operator-header-content" {
		t.Error("operator header fragment is not the final header child")
	}
	actions := findHTMLNodes(siteHeader, "div")
	if len(actions) != 1 || !hasHTMLClass(actions[0], "header-actions") ||
		len(elementChildren(actions[0])) != 2 || elementChildren(actions[0])[0].Data != "a" ||
		elementChildren(actions[0])[1].Data != "form" || !hasHTMLClass(elementChildren(actions[0])[1], "theme-toggle-form") {
		t.Error("shared header does not contain its brand, application navigation, and theme form")
	}
	inputs := findHTMLNodes(siteHeader, "input")
	if len(findHTMLNodes(siteHeader, "select")) != 0 || len(inputs) != 3 {
		t.Error("shared header must omit language controls and keep only the theme form inputs")
	} else {
		for _, input := range inputs {
			name := htmlAttribute(input, "name")
			if !isHiddenHTMLControl(input) || (name != "csrf_token" && name != "theme" && name != "redirect") {
				t.Error("shared header contains an unexpected input control")
			}
		}
	}
}

func assertOperatorHeaderContentIsAppended(t *testing.T, server *Server) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/server/about", nil)
	request.Header.Set("Accept", "text/html")
	baselineResponse := httptest.NewRecorder()
	server.router.ServeHTTP(baselineResponse, request)
	if baselineResponse.Code != http.StatusOK {
		t.Fatalf("baseline about page returned %d, want 200", baselineResponse.Code)
	}
	baseline, err := html.Parse(strings.NewReader(baselineResponse.Body.String()))
	if err != nil {
		t.Fatalf("parse baseline about page: %v", err)
	}
	baselineHeader := findSiteHeader(baseline)
	if baselineHeader == nil {
		t.Fatal("baseline about page has no site header")
	}
	baselineChildren := elementChildren(baselineHeader)

	server.config.Server.Branding.Content["header"] = config.PageContentConfig{
		Format: "html",
		Body:   `<span id="operator-header-content">Operator content</span>`,
	}
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("branded about page returned %d, want 200", response.Code)
	}
	delete(server.config.Server.Branding.Content, "header")
	document, err := html.Parse(strings.NewReader(response.Body.String()))
	if err != nil {
		t.Fatalf("parse branded about page: %v", err)
	}
	assertHeaderConformance(t, document, true)
	brandedHeader := findSiteHeader(document)
	if brandedHeader == nil {
		t.Fatal("branded about page has no site header")
	}
	brandedChildren := elementChildren(brandedHeader)
	if len(brandedChildren) != len(baselineChildren)+1 {
		t.Fatalf("branded header has %d children, want one more than baseline's %d", len(brandedChildren), len(baselineChildren))
	}
	for index, child := range baselineChildren {
		if index == 3 {
			normalizeCSRFInputValues(child)
			normalizeCSRFInputValues(brandedChildren[index])
		}
		var baselineHTML, brandedHTML bytes.Buffer
		if err := html.Render(&baselineHTML, child); err != nil {
			t.Fatalf("render baseline header child: %v", err)
		}
		if err := html.Render(&brandedHTML, brandedChildren[index]); err != nil {
			t.Fatalf("render branded header child: %v", err)
		}
		if baselineHTML.String() != brandedHTML.String() {
			t.Errorf("operator header content changed built-in header child %d", index)
		}
	}
}

func normalizeCSRFInputValues(root *html.Node) {
	walkHTML(root, func(node *html.Node) {
		if node.Type != html.ElementNode || node.Data != "input" || htmlAttribute(node, "name") != "csrf_token" {
			return
		}
		for index := range node.Attr {
			if node.Attr[index].Key == "value" {
				node.Attr[index].Val = "<csrf>"
				return
			}
		}
	})
}

func findSiteHeader(document *html.Node) *html.Node {
	for _, header := range findHTMLNodes(document, "header") {
		if hasHTMLClass(header, "header") {
			return header
		}
	}
	return nil
}

func assertInternalLinkResolves(t *testing.T, server *Server, pagePath string, document *html.Node, link *html.Node) {
	t.Helper()
	href := htmlAttribute(link, "href")
	parsed, err := url.Parse(href)
	if err != nil {
		return
	}
	if parsed.IsAbs() && parsed.Host != "example.com" {
		return
	}
	if parsed.Path == "" {
		if parsed.Fragment != "" && !htmlHasID(document, parsed.Fragment) {
			t.Errorf("internal fragment link %q has no target on %s", href, pagePath)
		}
		return
	}
	if !strings.HasPrefix(parsed.Path, "/") {
		return
	}
	request := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)
	if response.Code < http.StatusOK || response.Code >= http.StatusBadRequest {
		t.Errorf("internal link %q does not resolve (status %d)", href, response.Code)
	}
}

func htmlHasID(document *html.Node, id string) bool {
	found := false
	walkHTML(document, func(node *html.Node) {
		if node.Type == html.ElementNode && htmlAttribute(node, "id") == id {
			found = true
		}
	})
	return found
}

func elementChildren(node *html.Node) []*html.Node {
	children := make([]*html.Node, 0)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode {
			children = append(children, child)
		}
	}
	return children
}

func countHTMLNodes(root *html.Node, name string) int {
	return len(findHTMLNodes(root, name))
}

func findHTMLNodes(root *html.Node, name string) []*html.Node {
	var matches []*html.Node
	walkHTML(root, func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == name {
			matches = append(matches, node)
		}
	})
	return matches
}

func walkHTML(root *html.Node, visit func(*html.Node)) {
	if root == nil {
		return
	}
	visit(root)
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		walkHTML(child, visit)
	}
}

func firstFocusableHTMLNodes(document *html.Node) []*html.Node {
	var focusable []*html.Node
	walkHTML(document, func(node *html.Node) {
		if node.Type != html.ElementNode {
			return
		}
		switch node.Data {
		case "a", "button", "select", "textarea":
			focusable = append(focusable, node)
		case "input":
			if !isHiddenHTMLControl(node) {
				focusable = append(focusable, node)
			}
		}
	})
	return focusable
}

func isHiddenHTMLControl(node *html.Node) bool {
	return node.Data == "input" && strings.EqualFold(htmlAttribute(node, "type"), "hidden")
}

func hasAssociatedHTMLLabel(control *html.Node, labelIDs []string) bool {
	if id := htmlAttribute(control, "id"); id != "" {
		for _, labelID := range labelIDs {
			if labelID == id {
				return true
			}
		}
	}
	for ancestor := control.Parent; ancestor != nil; ancestor = ancestor.Parent {
		if ancestor.Type == html.ElementNode && ancestor.Data == "label" {
			return true
		}
	}
	return false
}

func formHasNamedControl(form *html.Node, name string) bool {
	for _, control := range findHTMLNodes(form, "input") {
		if htmlAttribute(control, "name") == name {
			return true
		}
	}
	return false
}

func firstTextChild(node *html.Node) string {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode && strings.TrimSpace(child.Data) != "" {
			return strings.TrimSpace(child.Data)
		}
	}
	return ""
}

func htmlAttribute(node *html.Node, name string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}

func hasHTMLClass(node *html.Node, class string) bool {
	for _, value := range strings.Fields(htmlAttribute(node, "class")) {
		if value == class {
			return true
		}
	}
	return false
}
