package securitydata

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/paths"
)

func TestParseIPsSkipsBogonsAndInvalidLines(t *testing.T) {
	got, skipped, err := parseIPs([]byte("# comment\n198.51.100.4\n10.0.0.0/8\nnot-an-ip\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].String() != "198.51.100.4/32" || skipped != 2 {
		t.Fatalf("got %v, skipped %d", got, skipped)
	}
	if _, _, err = parseIPs([]byte("10.0.0.0/8\n"), nil); err == nil {
		t.Fatal("expected all filtered records to reject source")
	}
}

func TestUpdateNVDStoresResponseAndFreshHealth(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("resultsPerPage") != "2000" || r.URL.Query().Get("startIndex") != "0" {
			t.Errorf("NVD query = %s", r.URL.RawQuery)
		}
		if r.Header.Get("apiKey") != "test-key" {
			t.Errorf("NVD API key header missing")
		}
		_, _ = w.Write([]byte(`{"resultsPerPage":1,"startIndex":0,"totalResults":1,"vulnerabilities":[{"cve":{"id":"CVE-2026-1234","descriptions":[]}}]}`))
	}))
	defer server.Close()

	root, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	m, err := New(Options{SecurityDir: root, NVDSource: server.URL, NVDAPIKey: "test-key", HTTPClient: server.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateNVD(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "cve", "nvd.json"))
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	if err := json.Unmarshal(data, &records); err != nil || len(records) != 1 || records[0]["id"] != "CVE-2026-1234" {
		t.Fatalf("NVD records = %s, err=%v", data, err)
	}
	if health := m.Health()["cve_nvd"]; health.Status != "healthy" || !health.LastUpdated.Equal(now) {
		t.Fatalf("NVD health = %+v", health)
	}
}

func TestUpdateNVDFailurePreservesPreviousData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	root, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dir := filepath.Join(root, "cve")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	old := `[{"id":"CVE-2025-9999"}]`
	if err := os.WriteFile(filepath.Join(dir, "nvd.json"), []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := New(Options{SecurityDir: root, NVDSource: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateNVD(context.Background()); err == nil {
		t.Fatal("UpdateNVD succeeded for HTTP 503")
	}
	data, err := os.ReadFile(filepath.Join(dir, "nvd.json"))
	if err != nil || string(data) != old {
		t.Fatalf("previous NVD data changed: %s, err=%v", data, err)
	}
}

func TestNewRegistersMissingSecurityDatabaseHealth(t *testing.T) {
	root, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	withoutTrivy, err := New(Options{SecurityDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if health := withoutTrivy.Health(); health["cve_nvd"].Status != "error" {
		t.Fatalf("missing NVD health = %+v", health["cve_nvd"])
	}
	if _, ok := withoutTrivy.Health()["trivy_db"]; ok {
		t.Fatal("disabled Trivy check was registered")
	}

	withTrivy, err := New(Options{SecurityDir: root, TrivyEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if health := withTrivy.Health(); health["trivy_db"].Status != "error" {
		t.Fatalf("missing Trivy health = %+v", health["trivy_db"])
	}
}

func TestTrivyUpdateSkipsCurrentDatabaseAndUsesContractKey(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	root, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dir := filepath.Join(root, "trivy")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	metadata := `{"NextUpdate":"2026-01-03T03:04:05Z"}`
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(metadata), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := New(Options{SecurityDir: root, TrivySource: "invalid", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateTrivy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if health := m.Health()["trivy_db"]; health.Status != "healthy" {
		t.Fatalf("Trivy health key/status = %+v", health)
	}
	if _, ok := m.Health()["cve_trivy"]; ok {
		t.Fatal("legacy cve_trivy health key is present")
	}
}

func TestHealthDegradesStaleSecurityDatasets(t *testing.T) {
	now := time.Now().UTC()
	m := &Manager{now: func() time.Time { return now }, health: map[string]Health{
		"blocklist_firehol": {Status: "healthy", LastUpdated: now.Add(-BlocklistMaxAge - time.Second)},
		"cve_nvd":           {Status: "healthy", LastUpdated: now.Add(-BlocklistMaxAge - time.Second)},
		"trivy_db":          {Status: "healthy", LastUpdated: now.Add(-BlocklistMaxAge - time.Second)},
		"fresh":             {Status: "healthy", LastUpdated: now},
	}}
	for _, key := range []string{"blocklist_firehol", "cve_nvd", "trivy_db"} {
		if health := m.Health()[key]; health.Status != "degraded" {
			t.Errorf("%s status = %q, want degraded", key, health.Status)
		}
	}
	if health := m.Health()["fresh"]; health.Status != "healthy" {
		t.Errorf("fresh status = %q, want healthy", health.Status)
	}
}

func TestParseOCIReference(t *testing.T) {
	for _, tc := range []struct {
		ref, registry, repo, tag string
		wantErr                  bool
	}{
		{"ghcr.io/aquasecurity/trivy-db:2", "ghcr.io", "aquasecurity/trivy-db", "2", false},
		{"registry-1.docker.io/library/ubuntu:latest", "registry-1.docker.io", "library/ubuntu", "latest", false},
		{"ghcr.io/org/repo", "", "", "", true},
		{"invalid", "", "", "", true},
	} {
		registry, repo, tag, err := parseOCIRef(tc.ref)
		if (err != nil) != tc.wantErr || registry != tc.registry || repo != tc.repo || tag != tc.tag {
			t.Errorf("parseOCIRef(%q) = (%q, %q, %q, %v)", tc.ref, registry, repo, tag, err)
		}
	}
}

func TestExtractTrivyRequiresAndWritesDatabaseFilesPrivately(t *testing.T) {
	dir, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	layer := makeTrivyLayer(t, map[string]string{
		"db/trivy.db":      "database",
		"db/metadata.json": `{"NextUpdate":"2026-01-03T00:00:00Z"}`,
		"ignored.txt":      "ignored",
	})
	if err := extractTrivy(layer, dir); err != nil {
		t.Fatalf("extractTrivy() error = %v", err)
	}
	for name, want := range map[string]string{
		"trivy.db":      "database",
		"metadata.json": `{"NextUpdate":"2026-01-03T00:00:00Z"}`,
	} {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Errorf("extracted %s = %q, err=%v", name, data, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("stat extracted %s: %v", name, err)
		} else if info.Mode().Perm() != 0600 {
			t.Errorf("extracted %s mode = %04o, want 0600", name, info.Mode().Perm())
		}
	}
}

func TestExtractTrivyRejectsMissingAndInvalidMetadata(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"missing database": {"metadata.json": `{}`},
		"invalid metadata": {"trivy.db": "database", "metadata.json": "not-json"},
	} {
		t.Run(name, func(t *testing.T) {
			dir, err := paths.CreateTempDir("countries")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			if err := extractTrivy(makeTrivyLayer(t, files), dir); err == nil {
				t.Fatal("extractTrivy() succeeded for invalid archive")
			}
		})
	}
}

func makeTrivyLayer(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for name, content := range files {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestCleanLineSkipsCommentsAndInlineMetadata(t *testing.T) {
	for input, want := range map[string]string{
		"  # comment":            "",
		"; comment":              "",
		"  198.51.100.2 comment": "198.51.100.2",
		"198.51.100.2; note":     "198.51.100.2",
		"  \t":                   "",
	} {
		if got := cleanLine(input); got != want {
			t.Errorf("cleanLine(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDomainNormalizationRejectsIPAndMalformedValues(t *testing.T) {
	for _, input := range []string{"127.0.0.1", "[2001:db8::1]:443", "-bad.example", "bad..example", ""} {
		if _, err := normalizeHost(input); err == nil {
			t.Errorf("normalizeHost(%q) succeeded", input)
		}
	}
	got, err := normalizeHost("https://user@BÜCHER.example:443/path")
	if err != nil || !strings.HasPrefix(got, "xn--") {
		t.Errorf("normalizeHost() = %q, %v; want an IDNA A-label", got, err)
	}
}

func TestParseDomainsAndBoundaryMatching(t *testing.T) {
	got, skipped, err := parseDomains([]byte("example.com\n*.example.net\n*.co.uk\nhttps://user@example.org:443/path\n"))
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || len(got) != 3 {
		t.Fatalf("got %v, skipped %d", got, skipped)
	}
	dir, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove test directory: %v", err)
		}
	})
	m, err := New(Options{SecurityDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	m.domainLists["domains"] = got
	for _, tc := range []struct {
		value string
		want  bool
	}{{"EXAMPLE.COM.", true}, {"a.example.net", true}, {"example.net", false}, {"fakeexample.net", false}, {"https://user@example.org:443/path", true}} {
		_, matched := m.MatchDomain(tc.value)
		if matched != tc.want {
			t.Errorf("MatchDomain(%q) = %v, want %v", tc.value, matched, tc.want)
		}
	}
}

func TestAtomicFilePreservesOldFileOnEmpty(t *testing.T) {
	dir, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove test directory: %v", err)
		}
	})
	path := filepath.Join(dir, "list.txt")
	if err = os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = atomicFile(path, nil); err == nil {
		t.Fatal("expected empty update rejection")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "old\n" {
		t.Fatalf("old file changed: %q, %v", b, err)
	}
}

func TestTrustedRangeFiltering(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	got, skipped, err := parseIPs([]byte("198.51.100.2\n203.0.113.0/24\n"), trusted)
	if err != nil || len(got) != 1 || skipped != 1 {
		t.Fatalf("got=%v skipped=%d err=%v", got, skipped, err)
	}
}

func TestIPParserDropsUnspecifiedBogonRange(t *testing.T) {
	got, skipped, err := parseIPs([]byte("0.0.0.0/8\n198.51.100.4\n"), nil)
	if err != nil || len(got) != 1 || skipped != 1 {
		t.Fatalf("got=%v skipped=%d err=%v", got, skipped, err)
	}
}

func TestNewLoadsPersistedBlocklists(t *testing.T) {
	root, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove test directory: %v", err)
		}
	})
	securityDir := filepath.Join(root, "security")
	blocklistDir := filepath.Join(securityDir, "blocklists")
	if err = os.MkdirAll(blocklistDir, 0700); err != nil {
		t.Fatal(err)
	}
	updated := time.Now().UTC().Format(time.RFC3339) + "\n"
	if err = os.WriteFile(filepath.Join(blocklistDir, ".last_updated"), []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(blocklistDir, "persisted.txt"), []byte("198.51.100.9/32\n"), 0600); err != nil {
		t.Fatal(err)
	}
	enabled := true
	m, err := New(Options{SecurityDir: securityDir, BlocklistsEnabled: true, ExtraBlocklists: []BlocklistSource{{ID: "persisted", Type: "ip", URL: "https://example.test/list", Enabled: &enabled}}})
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := m.MatchIP(netip.MustParseAddr("198.51.100.9")); !ok || id != "persisted" {
		t.Fatalf("persisted source match = %q, %v", id, ok)
	}
	if health := m.Health()["blocklist_persisted"]; health.Status != "healthy" {
		t.Fatalf("persisted health = %q", health.Status)
	}
}
