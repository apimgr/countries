package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckSelectsChannelAndIgnoresCurrentRelease(t *testing.T) {
	data := `[
{"tag_name":"v1.2.0","published_at":"2026-01-01T00:00:00Z","assets":[]},
{"tag_name":"202601021200-beta","prerelease":true,"published_at":"2026-01-02T00:00:00Z","assets":[]},
{"tag_name":"daily","prerelease":true,"published_at":"2026-01-03T00:00:00Z","assets":[]}
]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, data)
	}))
	defer server.Close()

	for _, test := range []struct {
		branch  string
		current string
		epoch   int64
		want    string
	}{
		{branch: "stable", current: "1.1.0", want: "v1.2.0"},
		{branch: "beta", current: "202601011200-beta", want: "202601021200-beta"},
		{branch: "devel", current: "1.2.0", epoch: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Unix(), want: "daily"},
		{branch: "stable", current: "1.2.0", want: ""},
	} {
		release, err := Check(context.Background(), server.Client(), server.URL, test.branch, test.current, test.epoch)
		if err != nil {
			t.Fatalf("Check(%q) error = %v", test.branch, err)
		}
		got := ""
		if release != nil {
			got = release.TagName
		}
		if got != test.want {
			t.Errorf("Check(%q, %q) = %q, want %q", test.branch, test.current, got, test.want)
		}
	}
}

func TestCheckTreatsReleaseNotFoundAsNoUpdate(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	release, err := Check(context.Background(), server.Client(), server.URL, "stable", "1.0.0", 0)
	if err != nil || release != nil {
		t.Fatalf("Check() = (%+v, %v), want no release and no error", release, err)
	}
}

func TestInstallVerifiesChecksumBeforeReplacing(t *testing.T) {
	binaryName := BinaryName("countries", "")
	binary := []byte("replacement binary")
	digest := sha256.Sum256(binary)
	checksum := hex.EncodeToString(digest[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sha256.txt":
			_, _ = fmt.Fprintf(w, "%s  %s\n", checksum, binaryName)
		case "/sha256-bad.txt":
			_, _ = fmt.Fprintf(w, "%s  %s\n", strings.Repeat("0", 64), binaryName)
		case "/binary":
			_, _ = w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	release := &Release{TagName: "v2.0.0", Assets: []Asset{
		{Name: "sha256.txt", URL: server.URL + "/sha256.txt"},
		{Name: binaryName, URL: server.URL + "/binary"},
	}}
	currentPath := filepath.Join(t.TempDir(), binaryName)
	if err := os.WriteFile(currentPath, []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Install(context.Background(), server.Client(), release, binaryName, currentPath); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	updated, err := os.ReadFile(currentPath)
	if err != nil || string(updated) != string(binary) {
		t.Fatalf("installed binary = %q, err = %v", updated, err)
	}

	badRelease := &Release{TagName: "v3.0.0", Assets: []Asset{
		{Name: "sha256.txt", URL: server.URL + "/sha256-bad.txt"},
		{Name: binaryName, URL: server.URL + "/binary"},
	}}
	if err := Install(context.Background(), server.Client(), badRelease, binaryName, currentPath); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Install() error = %v, want checksum mismatch", err)
	}
	unchanged, err := os.ReadFile(currentPath)
	if err != nil || string(unchanged) != string(binary) {
		t.Fatalf("binary after checksum failure = %q, err = %v", unchanged, err)
	}
}

func TestNormalizeBranchAliases(t *testing.T) {
	for alias, want := range map[string]string{"daily": "devel", "rolling": "devel", "dev": "devel", "development": "devel", "beta": "beta", "stable": "stable"} {
		if got := normalizeBranch(alias); got != want {
			t.Errorf("normalizeBranch(%q) = %q, want %q", alias, got, want)
		}
	}
}

func TestCompareVersionsHonorsPrereleaseOrdering(t *testing.T) {
	if got := compareVersions("v1.2.3-beta", "1.2.3"); got >= 0 {
		t.Errorf("beta compared as %d against stable release, want less than zero", got)
	}
	if got := compareVersions("v1.2.4", "1.2.3"); got <= 0 {
		t.Errorf("newer stable compared as %d, want greater than zero", got)
	}
}
