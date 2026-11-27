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

	"github.com/apimgr/countries/src/testutil"
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

func TestReleaseChecksumFetchesTrustedChecksumForExactTag(t *testing.T) {
	binaryName := BinaryName("countries", "")
	binary := []byte("staged update")
	digest := sha256.Sum256(binary)
	checksum := hex.EncodeToString(digest[:])
	checksumURL := ""
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases":
			_, _ = fmt.Fprintf(w, `[{"tag_name":"v2.0.0","assets":[{"name":"sha256.txt","browser_download_url":%q}]}]`, checksumURL)
		case "/sha256.txt":
			_, _ = fmt.Fprintf(w, "%s  %s\n", checksum, binaryName)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	checksumURL = server.URL + "/sha256.txt"

	got, err := ReleaseChecksum(context.Background(), server.Client(), server.URL+"/releases", "v2.0.0", binaryName)
	if err != nil || got != checksum {
		t.Fatalf("ReleaseChecksum() = (%q, %v), want %q", got, err, checksum)
	}
	if _, err := ReleaseChecksum(context.Background(), server.Client(), server.URL+"/releases", "v1.0.0", binaryName); err == nil {
		t.Fatal("ReleaseChecksum() succeeded for missing tag")
	}
	if _, err := ReleaseChecksum(context.Background(), server.Client(), "http://example.test/releases", "v2.0.0", binaryName); err == nil {
		t.Fatal("ReleaseChecksum() accepted a non-HTTPS metadata URL")
	}
}

func TestCheckWithDeferSelectsNewestMatureReleaseAndIgnoresDevelDeferral(t *testing.T) {
	data := `[
{"tag_name":"v1.2.0","published_at":"2026-09-01T00:00:00Z","assets":[]},
{"tag_name":"v1.3.0","published_at":"2026-11-20T00:00:00Z","assets":[]},
{"tag_name":"devel","prerelease":true,"published_at":"2026-11-21T00:00:00Z","assets":[]}
]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, data)
	}))
	defer server.Close()
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	release, err := CheckWithDefer(context.Background(), server.Client(), server.URL, "stable", "1.1.0", 0, 30, now)
	if err != nil || release == nil || release.TagName != "v1.2.0" {
		t.Fatalf("CheckWithDefer(stable) = (%+v, %v), want v1.2.0", release, err)
	}
	release, err = CheckWithDefer(context.Background(), server.Client(), server.URL, "devel", "1.1.0", 0, 30, now)
	if err != nil || release == nil || release.TagName != "devel" {
		t.Fatalf("CheckWithDefer(devel) = (%+v, %v), want devel", release, err)
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
	currentPath := filepath.Join(testutil.TempDir(t), binaryName)
	if err := os.WriteFile(currentPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(context.Background(), server.Client(), release, binaryName, "countries", currentPath); err != nil {
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
	if err := Install(context.Background(), server.Client(), badRelease, binaryName, "countries", currentPath); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Install() error = %v, want checksum mismatch", err)
	}
	unchanged, err := os.ReadFile(currentPath)
	if err != nil || string(unchanged) != string(binary) {
		t.Fatalf("binary after checksum failure = %q, err = %v", unchanged, err)
	}
}

func TestStageAndApplyVerifiesChecksumAndVersion(t *testing.T) {
	binaryName := BinaryName("countries", "")
	binary := []byte("staged replacement binary")
	digest := sha256.Sum256(binary)
	checksum := hex.EncodeToString(digest[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sha256.txt":
			_, _ = fmt.Fprintf(w, "%s  %s\n", checksum, binaryName)
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
	dataDir := filepath.Join(testutil.TempDir(t), "data")
	if err := Stage(context.Background(), server.Client(), release, binaryName, "countries", dataDir); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	updateDir := filepath.Join(dataDir, "update")
	for name, want := range map[string]string{stagedBinaryName: string(binary), stagedHashName: checksum + "\n", stagedVersion: "v2.0.0\n", stagedPublished: "0001-01-01T00:00:00Z\n"} {
		path := filepath.Join(updateDir, name)
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("staged %s = %q, error = %v, want %q", name, got, err, want)
		}
	}
	if !CanApplyStaged() {
		return
	}
	currentPath := filepath.Join(testutil.TempDir(t), binaryName)
	if err := os.WriteFile(currentPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if applied, err := ApplyStaged(dataDir, currentPath, "v1.0.0", 0); err != nil || !applied {
		t.Fatalf("ApplyStaged() = (%t, %v), want applied", applied, err)
	}
	updated, err := os.ReadFile(currentPath)
	if err != nil || string(updated) != string(binary) {
		t.Fatalf("installed staged binary = %q, error = %v", updated, err)
	}
	if _, err := os.Stat(updateDir); !os.IsNotExist(err) {
		t.Fatalf("staged update directory remains after apply: %v", err)
	}
}

func TestApplyStagedDiscardsInvalidChecksumAndOldVersion(t *testing.T) {
	if !CanApplyStaged() {
		t.Skip("staged updates are applied only by an elevated Unix process")
	}
	for _, test := range []struct {
		name    string
		version string
		hash    string
	}{
		{name: "old version", version: "v1.0.0", hash: strings.Repeat("0", sha256.Size*2)},
		{name: "bad checksum", version: "v2.0.0", hash: strings.Repeat("0", sha256.Size*2)},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDir := filepath.Join(testutil.TempDir(t), "data")
			updateDir := filepath.Join(dataDir, "update")
			if err := os.MkdirAll(updateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(updateDir, stagedBinaryName), []byte("staged"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(updateDir, stagedHashName), []byte(test.hash), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(updateDir, stagedVersion), []byte(test.version), 0o600); err != nil {
				t.Fatal(err)
			}
			currentPath := filepath.Join(testutil.TempDir(t), "countries")
			if err := os.WriteFile(currentPath, []byte("old binary"), 0o755); err != nil {
				t.Fatal(err)
			}
			if applied, err := ApplyStaged(dataDir, currentPath, "v1.0.0", 0); err == nil || applied {
				t.Fatalf("ApplyStaged() = (%t, %v), want discarded error", applied, err)
			}
			if _, err := os.Stat(updateDir); !os.IsNotExist(err) {
				t.Fatalf("invalid staged directory was not discarded: %v", err)
			}
			current, err := os.ReadFile(currentPath)
			if err != nil || string(current) != "old binary" {
				t.Fatalf("installed binary changed after rejected update: %q, %v", current, err)
			}
		})
	}
}

func TestApplyStagedUsesBuildEpochForRollingRelease(t *testing.T) {
	if !CanApplyStaged() {
		t.Skip("staged updates are applied only by an elevated Unix process")
	}
	for _, test := range []struct {
		name         string
		publishedAt  time.Time
		buildEpoch   int64
		wantRejected bool
	}{{"newer", time.Unix(200, 0).UTC(), 100, false}, {"same build", time.Unix(100, 0).UTC(), 100, true}, {"missing timestamp", time.Time{}, 0, true}} {
		t.Run(test.name, func(t *testing.T) {
			dataDir := filepath.Join(testutil.TempDir(t), "data")
			updateDir := filepath.Join(dataDir, "update")
			if err := os.MkdirAll(updateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			binary := []byte("devel binary")
			checksum := sha256.Sum256(binary)
			if err := os.WriteFile(filepath.Join(updateDir, stagedBinaryName), binary, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(updateDir, stagedHashName), []byte(hex.EncodeToString(checksum[:])), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(updateDir, stagedVersion), []byte("devel"), 0o600); err != nil {
				t.Fatal(err)
			}
			if !test.publishedAt.IsZero() {
				if err := os.WriteFile(filepath.Join(updateDir, stagedPublished), []byte(test.publishedAt.Format(time.RFC3339Nano)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			currentPath := filepath.Join(testutil.TempDir(t), "countries")
			if err := os.WriteFile(currentPath, []byte("old binary"), 0o755); err != nil {
				t.Fatal(err)
			}
			applied, err := ApplyStaged(dataDir, currentPath, "devel", test.buildEpoch)
			if test.wantRejected {
				if err == nil || applied {
					t.Fatalf("ApplyStaged() = (%t, %v), want rejection", applied, err)
				}
				return
			}
			if err != nil || !applied {
				t.Fatalf("ApplyStaged() = (%t, %v), want applied", applied, err)
			}
		})
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
