package main

import (
	"encoding/json"
	"github.com/apimgr/countries/src/testutil"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/paths"
	tea "github.com/charmbracelet/bubbletea"
)

func TestRunRemoteCommands(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", testutil.TempDir(t))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		switch r.URL.Path {
		case "/api/v1/countries":
			_, _ = io.WriteString(w, `{"ok":true,"total":1,"data":[{"name":{"common":"Testland","official":"Republic of Testland"},"cca2":"TL","cca3":"TST","capital":["Testville"],"timezones":["Etc/UTC"],"latlng":[10,20]}]}`)
		case "/api/v1/countries/TL":
			_, _ = io.WriteString(w, `{"ok":true,"data":{"name":{"common":"Testland","official":"Republic of Testland"},"cca2":"TL","cca3":"TST","capital":["Testville"],"timezones":["Etc/UTC"],"latlng":[10,20]}}`)
		case "/api/v1/countries/search":
			if r.URL.Query().Get("q") != "Test land" {
				t.Errorf("search query = %q", r.URL.Query().Get("q"))
			}
			_, _ = io.WriteString(w, `{"ok":true,"data":[{"name":{"common":"Testland","official":"Republic of Testland"},"cca2":"TL","capital":["Testville"]}]}`)
		case "/api/v1/coordinates":
			_, _ = io.WriteString(w, `{"ok":true,"data":{"name":{"common":"Testland","official":"Republic of Testland"},"cca2":"TL"}}`)
		case "/api/v1/nearby":
			_, _ = io.WriteString(w, `{"ok":true,"data":[{"name":{"common":"Testland","official":"Republic of Testland"},"cca2":"TL"}]}`)
		case "/api/v1/stats":
			_, _ = io.WriteString(w, `{"ok":true,"data":{"count":1}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"list", []string{"--server", server.URL, "--color=no", "list"}, "Testland"},
		{"get", []string{"--server", server.URL, "--output=plain", "get", "tl"}, "Testland (TL)"},
		{"search", []string{"--server", server.URL, "--output=json", "search", "Test", "land"}, "Testland"},
		{"nearest", []string{"--server", server.URL, "nearest", "10", "20"}, "Testland"},
		{"nearby", []string{"--server", server.URL, "nearby", "10", "20", "100"}, "Testland"},
		{"stats", []string{"--server", server.URL, "stats"}, "count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			if err := run(tc.args, &stdout, &stderr); err != nil {
				t.Fatalf("run() error = %v; stderr=%s", err, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("output = %q, want %q", stdout.String(), tc.want)
			}
		})
	}
}

func TestRunValidationAndResponseErrors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", testutil.TempDir(t))
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing server", []string{"list"}, "Server URL is required"},
		{"bad URL", []string{"--server", "file:///tmp", "list"}, "Server URL"},
		{"bad color", []string{"--server", "https://example.test", "--color=purple", "list"}, "Invalid --color"},
		{"missing command", []string{"--server", "https://example.test"}, "Command required"},
		{"bad coordinates", []string{"--server", "https://example.test", "nearest", "NaN", "20"}, "Latitude"},
		{"bad radius", []string{"--server", "https://example.test", "nearby", "10", "20", "-1"}, "Radius"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			err := run(tc.args, &stdout, &stderr)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunHTTPErrorAndMalformedEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"HTTP error", `{"message":"not found"}`, "not found", http.StatusNotFound},
		{"malformed envelope", `{`, "decode server response", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", testutil.TempDir(t))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			var stdout, stderr strings.Builder
			err := run([]string{"--server", server.URL, "stats"}, &stdout, &stderr)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestClientRequestCommandSendsVersionedAPIRequest(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", testutil.TempDir(t))
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/autodiscover" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/countries/search" || r.URL.Query().Get("q") != "Example" {
			t.Errorf("request = %s %s, want POST /api/v1/countries/search?q=Example", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer request-test-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Accept-Language"); got != "fr" {
			t.Errorf("Accept-Language = %q, want fr", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		requestBody = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"data":{"accepted":true}}`)
	}))
	defer server.Close()
	var stdout, stderr strings.Builder
	err := run([]string{"--server", server.URL, "--token", "request-test-token", "--lang", "fr", "request", "POST", "/api/v1/countries/search?q=Example", `{"query":"two words"}`}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("request command: %v; stderr=%s", err, stderr.String())
	}
	if requestBody != `{"query":"two words"}` {
		t.Fatalf("request body = %q", requestBody)
	}
	if !strings.Contains(stdout.String(), `"accepted":true`) {
		t.Fatalf("response output = %q", stdout.String())
	}
}

func TestClientRequestRejectsUnsafeTargetsAndMethods(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body, want string
	}{
		{"unsupported method", "TRACE", "/api/v1/countries", "", "Unsupported API method"},
		{"unversioned path", "GET", "/countries", "", "versioned API path"},
		{"absolute URL", "GET", "https://other.example/api/v1/countries", "", "versioned API path"},
		{"traversal", "GET", "/api/v1/../server/healthz", "", "versioned API path"},
		{"machine MCP endpoint", "POST", "/api/v1/server/mcp", `{}`, "machine-only"},
		{"token check endpoint", "GET", "/api/v1/server/preferences/tokens/check", "", "machine-only"},
		{"invalid body", "POST", "/api/v1/countries", `{`, "valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := executeClientAPIRequest("https://countries.example.test", "", tc.method, tc.target, tc.body, "en")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("request validation error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestClientRequestDoesNotFollowCrossOriginRedirect(t *testing.T) {
	destinationCalled := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/api/v1/countries", http.StatusFound)
	}))
	defer origin.Close()
	_, err := executeClientAPIRequest(origin.URL, "secret", "GET", "/api/v1/countries", "", "en")
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("redirect response error = %v, want HTTP 302", err)
	}
	if destinationCalled {
		t.Fatal("request followed a redirect to another origin")
	}
}

func TestClientPureHelpers(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	if got, err := normalizeBaseURL("https://example.test/"); err != nil || got != "https://example.test" {
		t.Fatalf("normalizeBaseURL = %q, %v", got, err)
	}
	for _, raw := range []string{"relative", "ftp://example.test", "https://user:pass@example.test", "https://example.test/?q=x", "https://example.test/#section"} {
		if _, err := normalizeBaseURL(raw); err == nil {
			t.Errorf("normalizeBaseURL(%q) accepted invalid URL", raw)
		}
	}
	for _, value := range []string{"auto", "yes", "no"} {
		if err := validateColor(value); err != nil {
			t.Errorf("validateColor(%q): %v", value, err)
		}
	}
	if err := validateColor("invalid"); err == nil {
		t.Fatal("validateColor accepted an invalid value")
	}
	if got, err := parseCoordinate("-90", -90, 90); err != nil || got != "-90" {
		t.Fatalf("parseCoordinate = %q, %v", got, err)
	}
	for _, value := range []string{"NaN", "+Inf", "91", "bad"} {
		if _, err := parseCoordinate(value, -90, 90); err == nil {
			t.Errorf("parseCoordinate(%q) accepted invalid coordinate", value)
		}
	}
	var output strings.Builder
	item := country{CCA2: "TL", Capital: []string{"Testville"}, Timezones: []string{"Etc/UTC"}}
	item.Name.Common = "Testland"
	item.Name.Official = "Republic of Testland"
	if err := printCountries(&output, []country{item}, "plain", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Testland (TL)") {
		t.Errorf("plain output = %q", output.String())
	}
	if colorEnabled("auto", &output) || colorEnabled("no", &output) || !colorEnabled("yes", os.Stdout) {
		t.Error("color mode resolution mismatch")
	}
}

func TestHelpAndVersion(t *testing.T) {
	var output strings.Builder
	if err := printHelp(&output); err != nil {
		t.Fatalf("print help: %v", err)
	}
	if !strings.Contains(output.String(), filepath.Base(os.Args[0])) || !strings.Contains(output.String(), "Commands:") || !strings.Contains(output.String(), "request METHOD /api/v1/PATH") {
		t.Errorf("help output = %q", output.String())
	}
	output.Reset()
	if err := printVersion(&output); err != nil {
		t.Fatalf("print version: %v", err)
	}
	if !strings.Contains(output.String(), "OS/Arch:") {
		t.Errorf("version output = %q", output.String())
	}
}

func TestClientLanguageSelectionAndLocalizedHelp(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LANG", "fr_FR.UTF-8")
	if got := selectLanguage("", "auto"); got != "de" {
		t.Fatalf("selectLanguage(auto) = %q, want LC_ALL language de", got)
	}
	if got := selectLanguage("it", "es"); got != "it" {
		t.Fatalf("selectLanguage(flag, config) = %q, want it", got)
	}
	if got := selectLanguage("unsupported", "es"); got != "en" {
		t.Fatalf("selectLanguage(unsupported flag) = %q, want en", got)
	}
	if got := selectLanguage("", "unsupported"); got != "en" {
		t.Fatalf("selectLanguage(unsupported config) = %q, want en", got)
	}

	t.Setenv("XDG_CONFIG_HOME", testutil.TempDir(t))
	var output strings.Builder
	if err := run([]string{"--lang", "es", "--help"}, &output, io.Discard); err != nil {
		t.Fatalf("run localized help: %v", err)
	}
	if !strings.Contains(output.String(), "Uso:") || !strings.Contains(output.String(), "Listar registros de países") {
		t.Fatalf("Spanish help output = %q", output.String())
	}
}

func TestLocalizedFlagParserErrors(t *testing.T) {
	var stdout, stderr strings.Builder
	err := run([]string{"--lang", "fr", "--unknown-option"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "Option inconnue") {
		t.Fatalf("localized unknown flag error = %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	err = run([]string{"--lang", "es", "--color", "purple"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no válido") {
		t.Fatalf("localized invalid flag value error = %v", err)
	}
}

func TestClientSendsLanguageAndLocalizesTable(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "")
	t.Setenv("XDG_CONFIG_HOME", testutil.TempDir(t))
	var languageHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/autodiscover" {
			http.NotFound(w, r)
			return
		}
		languageHeader = r.Header.Get("Accept-Language")
		_, _ = io.WriteString(w, `{"ok":true,"data":[]}`)
	}))
	defer server.Close()
	var output strings.Builder
	if err := run([]string{"--server", server.URL, "--lang", "es", "list"}, &output, io.Discard); err != nil {
		t.Fatalf("run localized list: %v", err)
	}
	if languageHeader != "es" {
		t.Fatalf("Accept-Language = %q, want es", languageHeader)
	}
	if !strings.Contains(output.String(), "PAÍS") {
		t.Fatalf("Spanish table headers missing: %q", output.String())
	}
}

func TestSaveConfigUsesOwnerOnlyPermissions(t *testing.T) {
	configRoot := testutil.TempDir(t)
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	var cfg cliConfig
	cfg.Server.Primary = "https://countries.example.test"
	cfg.Server.Token = "test-token"
	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	path := filepath.Join(configRoot, internalOrg, internalName, "cli.yml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("CLI config permissions = %04o, want 0600", info.Mode().Perm())
	}
	loaded, err := loadConfig()
	if err != nil || loaded.Server.Token != "test-token" {
		t.Fatalf("loadConfig() = (%+v, %v), want saved token", loaded.Server, err)
	}
}

func TestClientSendsConfiguredBearerToken(t *testing.T) {
	configRoot := testutil.TempDir(t)
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	var cfg cliConfig
	cfg.Server.Token = "owner-test-token"
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer owner-test-token" {
			t.Errorf("Authorization = %q, want configured bearer token", got)
		}
		_, _ = io.WriteString(w, `{"ok":true,"data":{"count":1}}`)
	}))
	defer server.Close()
	var stdout, stderr strings.Builder
	if err := run([]string{"--server", server.URL, "stats"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v; stderr=%s", err, stderr.String())
	}
}

func TestInteractiveSetupValidatesAndSavesServer(t *testing.T) {
	configRoot, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(configRoot) })
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.APIPath("server/healthz") {
			t.Errorf("setup checked %q, want health endpoint", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var cfg cliConfig
	var output, stderr strings.Builder
	configPath := filepath.Join(configRoot, "cli.yml")
	model := newClientTUI("", &cfg, configPath, &output, &stderr)
	model.input = server.URL
	updated, _ := model.updateInput(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(clientTUI)
	if model.screen != "setup-token" || model.server != server.URL {
		t.Fatalf("setup state = (%q, %q), want token screen and checked server", model.screen, model.server)
	}
	updated, _ = model.updateInput(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(clientTUI)
	loaded, err := loadConfigAt(configPath)
	if err != nil || loaded.Server.Primary != server.URL {
		t.Fatalf("saved server = %q, err = %v", loaded.Server.Primary, err)
	}
}

func TestClientScreenInventoryMatchesCommands(t *testing.T) {
	model := newClientTUI("https://countries.example.test", &cliConfig{}, "", io.Discard, io.Discard)
	if got := model.menu.Items(); len(got) != len(clientCommands) {
		t.Fatalf("TUI screen count = %d, command count = %d", len(got), len(clientCommands))
	}
	for i, item := range model.menu.Items() {
		entry, ok := item.(tuiItem)
		if !ok || entry.command != clientCommands[i] {
			t.Fatalf("TUI screen %d = %#v, want command %q", i, item, clientCommands[i])
		}
	}
	if mode, err := clientDisplayMode("auto", false, io.Discard); err != nil || mode != "cli" {
		t.Fatalf("command mode = %q, %v; want cli", mode, err)
	}
}

func TestClientRequestHasOneDedicatedTUIScreenAndGUIForm(t *testing.T) {
	if got := clientCommandFormScreen("request"); got != "request" {
		t.Fatalf("request form screen = %q, want dedicated request screen", got)
	}
	model := newClientTUI("https://countries.example.test", &cliConfig{}, "", io.Discard, io.Discard)
	updated, _ := model.selectCommand("request")
	model = updated.(clientTUI)
	if model.screen != "request" || len(model.fields) != 3 {
		t.Fatalf("request TUI state = screen %q with %d fields", model.screen, len(model.fields))
	}
	if !strings.Contains(model.View(), "HTTP method") || !strings.Contains(model.View(), "Versioned API path") {
		t.Fatalf("request TUI view does not show localized request fields: %q", model.View())
	}
}

func TestClientUpdateDiscoveryCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/autodiscover" {
			t.Errorf("autodiscover path = %q", r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("autodiscover Accept = %q", r.Header.Get("Accept"))
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
			"data": map[string]interface{}{
				"cli_versions": map[string]clientVersion{runtime.GOOS + "-" + runtime.GOARCH: {Version: "9999.0.0"}},
			},
		})
	}))
	defer server.Close()
	var output strings.Builder
	if err := checkAndApplyClientUpdate(server.URL, "", "check", false, false, strings.NewReader(""), &output, io.Discard); err != nil {
		t.Fatalf("checkAndApplyClientUpdate() error = %v", err)
	}
	if !strings.Contains(output.String(), "CLI update available: 9999.0.0") {
		t.Fatalf("update check output = %q", output.String())
	}
}

func TestCompareClientVersions(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        int
	}{{"1.2.0", "1.1.9", 1}, {"v2.0.0", "2.0.0", 0}, {"1.0.0", "1.0.1", -1}} {
		if got := compareClientVersions(tc.left, tc.right); got != tc.want {
			t.Errorf("compareClientVersions(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
	}
}
