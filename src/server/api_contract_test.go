package server

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/swagger"
	"github.com/go-chi/chi/v5"
)

var (
	contractPathRE   = regexp.MustCompile("`(/[^`\\s]+)`")
	contractMethodRE = regexp.MustCompile(`^(GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS)$`)
)

func TestAPIRouteContractMatchesSpecAndRouter(t *testing.T) {
	server := newTestServer(t)
	templateRoutes := readContractRoutes(t, filepath.Join("..", "..", "AI.md"), false)
	if !server.config.Server.Healthz.Root.Enabled {
		delete(templateRoutes, "GET /healthz")
	}
	projectRoutes := readContractRoutes(t, filepath.Join("..", "..", "IDEA.md"), true)
	if len(projectRoutes) == 0 {
		t.Fatal("IDEA.md has no ### API routes inventory")
	}
	contract := make(map[string]struct{}, len(templateRoutes)+len(projectRoutes))
	for route := range templateRoutes {
		contract[route] = struct{}{}
	}
	for route := range projectRoutes {
		contract[route] = struct{}{}
	}

	routerRoutes := make(map[string]struct{})
	if err := collectRegisteredRoutes(server.router, routerRoutes); err != nil {
		t.Fatalf("walk registered routes: %v", err)
	}
	spec, err := swagger.Spec("test-version", server.router)
	if err != nil {
		t.Fatalf("build OpenAPI route list: %v", err)
	}
	specRoutes := openAPIRoutes(spec)
	assertRouteSetsMatch(t, "AI.md + IDEA.md inventory", contract, "router", routerRoutes)
	assertRouteSetsMatch(t, "router", routerRoutes, "OpenAPI", specRoutes)
	assertRouteSetsMatch(t, "AI.md + IDEA.md inventory", contract, "OpenAPI", specRoutes)
}

func readContractRoutes(t *testing.T, path string, ideaInventory bool) map[string]struct{} {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open API contract %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()
	routes := make(map[string]struct{})
	insideInventory := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if ideaInventory {
			if strings.HasPrefix(line, "### ") {
				insideInventory = strings.TrimSpace(line) == "### API routes"
				continue
			}
			if !insideInventory {
				continue
			}
		} else {
			if strings.TrimSpace(line) == "## Root-Level Endpoints" {
				insideInventory = true
				continue
			}
			if insideInventory && strings.HasPrefix(strings.TrimSpace(line), "**NOTE:**") {
				insideInventory = false
			}
			if !insideInventory {
				continue
			}
		}
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			continue
		}
		pathCell, methodCell := cells[1], cells[2]
		if !ideaInventory {
			pathCell = strings.TrimSpace(pathCell)
			if len(pathCell) < 3 || pathCell[0] != '`' || pathCell[len(pathCell)-1] != '`' {
				continue
			}
			path := pathCell[1 : len(pathCell)-1]
			if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " `") {
				continue
			}
			pathCell = "`" + path + "`"
		} else {
			pathCell, methodCell = cells[2], cells[1]
		}
		paths := contractPathRE.FindAllStringSubmatch(pathCell, -1)
		if len(paths) == 0 {
			continue
		}
		methods := strings.FieldsFunc(strings.ToUpper(methodCell), func(r rune) bool {
			return r == '/' || r == ',' || r == ' ' || r == '`'
		})
		for _, method := range methods {
			if !contractMethodRE.MatchString(method) {
				continue
			}
			for _, match := range paths {
				path := match[1]
				if strings.Contains(path, "*") {
					continue
				}
				path = strings.ReplaceAll(path, "{api_version}", strings.TrimPrefix(api.APIBasePath(), "/api/"))
				pathsToAdd := []string{path}
				if marker := strings.Index(path, "[/{"); marker >= 0 && strings.HasSuffix(path, "}]") {
					pathsToAdd = []string{path[:marker], path[:marker] + path[marker+1:len(path)-1]}
				}
				for _, routePath := range pathsToAdd {
					if strings.HasPrefix(routePath, "/api/") || routePath == "/mcp" {
						routes[method+" "+routePath] = struct{}{}
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read API contract %s: %v", path, err)
	}
	return routes
}

func collectRegisteredRoutes(routes chi.Routes, result map[string]struct{}) error {
	return chi.Walk(routes, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if (!strings.HasPrefix(path, "/api/") && path != "/mcp") || !contractMethodRE.MatchString(method) {
			return nil
		}
		result[method+" "+path] = struct{}{}
		return nil
	})
}

func openAPIRoutes(spec map[string]interface{}) map[string]struct{} {
	result := make(map[string]struct{})
	paths, _ := spec["paths"].(map[string]interface{})
	for path, rawItem := range paths {
		if !strings.HasPrefix(path, "/api/") && path != "/mcp" {
			continue
		}
		item, _ := rawItem.(map[string]interface{})
		for method := range item {
			upper := strings.ToUpper(method)
			if contractMethodRE.MatchString(upper) {
				result[upper+" "+path] = struct{}{}
			}
		}
	}
	return result
}

func assertRouteSetsMatch(t *testing.T, leftName string, left map[string]struct{}, rightName string, right map[string]struct{}) {
	t.Helper()
	var missing []string
	for route := range left {
		if _, exists := right[route]; !exists {
			missing = append(missing, route)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	t.Errorf("%s routes missing from %s: %s", leftName, rightName, strings.Join(missing, ", "))
}
