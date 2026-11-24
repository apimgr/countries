package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/testutil"
)

func TestShellCompletionsAndInit(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "sh", "dash", "ksh", "powershell", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			var completions bytes.Buffer
			if err := writeCompletions(&completions, shell, "countries-cli"); err != nil {
				t.Fatalf("writeCompletions() error = %v", err)
			}
			for _, command := range clientCommands {
				if !strings.Contains(completions.String(), command) {
					t.Errorf("%s completion omits command %q", shell, command)
				}
			}
			var init bytes.Buffer
			if err := writeShellInit(&init, shell, "countries-cli"); err != nil {
				t.Fatalf("writeShellInit() error = %v", err)
			}
			if !strings.Contains(init.String(), "--shell completions "+shell) {
				t.Errorf("%s init does not invoke its completion command: %q", shell, init.String())
			}
		})
	}
}

func TestShellCommandValidationDetectionAndEscaping(t *testing.T) {
	var output bytes.Buffer
	if handled, err := shellCommand([]string{"list"}, &output); handled || err != nil {
		t.Fatalf("non-shell command = handled %t, err %v", handled, err)
	}
	if handled, err := shellCommand([]string{"--shell"}, &output); !handled || err == nil {
		t.Fatalf("missing shell action = handled %t, err %v", handled, err)
	}
	if handled, err := shellCommand([]string{"--shell", "unsupported"}, &output); !handled || err == nil {
		t.Fatalf("unsupported shell action = handled %t, err %v", handled, err)
	}
	if handled, err := shellCommand([]string{"--shell", "help", "bash"}, &output); !handled || err != nil || output.Len() == 0 {
		t.Fatalf("shell help = handled %t, output %q, err %v", handled, output.String(), err)
	}
	if err := writeCompletions(&output, "unknown", "countries-cli"); err == nil {
		t.Fatal("unknown shell completion was accepted")
	}
	if err := writeShellInit(&output, "unknown", "countries-cli"); err == nil {
		t.Fatal("unknown shell initialization was accepted")
	}
	if got := completionFunctionName("countries-cli"); got != "countries_cli" {
		t.Errorf("completion function name = %q", got)
	}
	if got := shellQuote("country's cli"); got != "'country'\\''s cli'" {
		t.Errorf("shell quote = %q", got)
	}
	t.Setenv("SHELL", "  /usr/bin/fish  ")
	if got := detectShell(); got != "fish" {
		t.Errorf("detected shell = %q, want fish", got)
	}
	t.Setenv("SHELL", " ")
	if got := detectShell(); got != "bash" {
		t.Errorf("fallback shell = %q, want bash", got)
	}
}

func TestRunShellHelpUsesConfiguredAndFlagLanguage(t *testing.T) {
	configHome := testutil.TempDir(t)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	configPath, err := resolveConfigPath("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("lang: es\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	if err := run([]string{"--shell", "help"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() with configured language: %v", err)
	}
	if !strings.Contains(stdout.String(), "Integración del shell") {
		t.Fatalf("shell help ignored cli.yml language: %q", stdout.String())
	}

	stdout.Reset()
	if err := run([]string{"--lang", "fr", "--shell", "help"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() with language flag: %v", err)
	}
	if !strings.Contains(stdout.String(), "Intégration du shell") {
		t.Fatalf("shell help ignored --lang: %q", stdout.String())
	}
}
