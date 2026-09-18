package main

import (
	"io"
	"os"
	"strings"
	"testing"
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
