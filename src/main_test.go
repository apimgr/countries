package main

import (
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/config"
)

// printHelp writes usage text directly to stdout; capture it via an
// os.Pipe to confirm the help text is produced and contains the
// documented flags and command sections.
func TestPrintHelp(t *testing.T) {
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = w

	printHelp()

	if err := w.Close(); err != nil {
		t.Fatalf("w.Close() error = %v", err)
	}
	os.Stdout = origStdout

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	got := string(out)

	wantSubstrings := []string{
		"Countries API Server",
		"Usage: countries [options]",
		"--port PORT",
		"--address ADDRESS",
		"--config DIR",
		"--data DIR",
		"--log DIR",
		"--help",
		"Mode Commands:",
		"Service Commands:",
		"Maintenance Commands:",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("printHelp() output missing %q, got: %s", want, got)
		}
	}
}

func TestUpdateCommandBranches(t *testing.T) {
	releaseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	}))
	defer releaseServer.Close()
	previousReleaseURL := updaterReleaseURL
	updaterReleaseURL = releaseServer.URL
	t.Cleanup(func() { updaterReleaseURL = previousReleaseURL })
	cases := []struct {
		name  string
		cmd   string
		args  []string
		check func(*testing.T, *config.Config, string)
	}{
		{name: "check", cmd: "check", check: func(t *testing.T, _ *config.Config, output string) {
			if !strings.Contains(output, "up to date") {
				t.Errorf("check output = %q", output)
			}
		}},
		{name: "install", cmd: "yes", check: func(t *testing.T, _ *config.Config, output string) {
			if !strings.Contains(output, "up to date") {
				t.Errorf("install output = %q", output)
			}
		}},
		{name: "show branch", cmd: "branch", check: func(t *testing.T, cfg *config.Config, output string) {
			if !strings.Contains(output, cfg.Server.Update.Branch) {
				t.Errorf("branch output = %q, want branch %q", output, cfg.Server.Update.Branch)
			}
		}},
		{name: "set branch", cmd: "branch", args: []string{"beta"}, check: func(t *testing.T, cfg *config.Config, output string) {
			if cfg.Server.Update.Branch != "beta" || !strings.Contains(output, "Branch set to: beta") {
				t.Errorf("branch config/output = %q / %q", cfg.Server.Update.Branch, output)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldFlags := flag.CommandLine
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			flag.CommandLine = fs
			t.Cleanup(func() { flag.CommandLine = oldFlags })
			cfg := config.DefaultConfig()
			configPath := filepath.Join(t.TempDir(), "server.yml")
			if err := config.Save(configPath, cfg); err != nil {
				t.Fatal(err)
			}
			output := captureOutput(t, func() { _ = handleUpdateCommand(tc.cmd, cfg, configPath) })
			tc.check(t, cfg, output)
			if tc.name == "set branch" {
				loaded, err := config.Load(configPath)
				if err != nil || loaded.Server.Update.Branch != "beta" {
					t.Fatalf("saved branch = %q, err = %v", loaded.Server.Update.Branch, err)
				}
			}
		})
	}
}

func TestResolveUpdateInvocation(t *testing.T) {
	for _, test := range []struct {
		value string
		args  []string
		cmd   string
		want  []string
	}{
		{value: "true", cmd: "yes"},
		{value: "true", args: []string{"check"}, cmd: "check"},
		{value: "true", args: []string{"branch", "beta"}, cmd: "branch", want: []string{"beta"}},
		{value: "check", cmd: "check"},
		{value: "branch", args: []string{"devel"}, cmd: "branch", want: []string{"devel"}},
	} {
		gotCommand, gotArgs := resolveUpdateInvocation(test.value, test.args)
		if gotCommand != test.cmd || strings.Join(gotArgs, " ") != strings.Join(test.want, " ") {
			t.Errorf("resolveUpdateInvocation(%q, %v) = (%q, %v), want (%q, %v)", test.value, test.args, gotCommand, gotArgs, test.cmd, test.want)
		}
	}
}
