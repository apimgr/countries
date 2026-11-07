package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/common/api"
)

func TestRunRemoteCommands(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		switch {
		case r.URL.Path == "/api/v1/countries":
			_, _ = io.WriteString(w, `{"ok":true,"total":1,"data":[{"name":{"common":"Testland","official":"Republic of Testland"},"cca2":"TL","cca3":"TST","capital":["Testville"],"timezones":["Etc/UTC"],"latlng":[10,20]}]}`)
		case r.URL.Path == "/api/v1/countries/TL":
			_, _ = io.WriteString(w, `{"ok":true,"data":{"name":{"common":"Testland","official":"Republic of Testland"},"cca2":"TL","cca3":"TST","capital":["Testville"],"timezones":["Etc/UTC"],"latlng":[10,20]}}`)
		case r.URL.Path == "/api/v1/countries/search":
			if r.URL.Query().Get("q") != "Test land" {
				t.Errorf("search query = %q", r.URL.Query().Get("q"))
			}
			_, _ = io.WriteString(w, `{"ok":true,"data":[{"name":{"common":"Testland","official":"Republic of Testland"},"cca2":"TL","capital":["Testville"]}]}`)
		case r.URL.Path == "/api/v1/coordinates":
			_, _ = io.WriteString(w, `{"ok":true,"data":{"name":{"common":"Testland","official":"Republic of Testland"},"cca2":"TL"}}`)
		case r.URL.Path == "/api/v1/nearby":
			_, _ = io.WriteString(w, `{"ok":true,"data":[{"name":{"common":"Testland","official":"Republic of Testland"},"cca2":"TL"}]}`)
		case r.URL.Path == "/api/v1/stats":
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing server", []string{"list"}, "server URL is required"},
		{"bad URL", []string{"--server", "file:///tmp", "list"}, "server URL"},
		{"bad color", []string{"--server", "https://example.test", "--color=purple", "list"}, "invalid --color"},
		{"missing command", []string{"--server", "https://example.test"}, "command required"},
		{"unknown command", []string{"--server", "https://example.test", "wat"}, "unknown command"},
		{"bad coordinates", []string{"--server", "https://example.test", "nearest", "NaN", "20"}, "latitude"},
		{"bad radius", []string{"--server", "https://example.test", "nearby", "10", "20", "-1"}, "radius"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			err := run(tc.args, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
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
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			var stdout, stderr strings.Builder
			err := run([]string{"--server", server.URL, "stats"}, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
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
	printHelp(&output)
	if !strings.Contains(output.String(), "countries-cli") || !strings.Contains(output.String(), "Commands:") {
		t.Errorf("help output = %q", output.String())
	}
	output.Reset()
	printVersion(&output)
	if !strings.Contains(output.String(), "OS/Arch:") {
		t.Errorf("version output = %q", output.String())
	}
}

func TestSaveConfigUsesOwnerOnlyPermissions(t *testing.T) {
	configRoot := t.TempDir()
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
	configRoot := t.TempDir()
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
	configRoot := t.TempDir()
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
	input := strings.NewReader(server.URL + "\n\n\n7\n")
	configPath, err := resolveConfigPath("")
	if err != nil {
		t.Fatalf("resolveConfigPath() error = %v", err)
	}
	if err := runInteractive("", &cfg, configPath, input, &output, &stderr); err != nil {
		t.Fatalf("runInteractive() error = %v; stderr=%s", err, stderr.String())
	}
	loaded, err := loadConfig()
	if err != nil || loaded.Server.Primary != server.URL {
		t.Fatalf("saved server = %q, err = %v", loaded.Server.Primary, err)
	}
}
