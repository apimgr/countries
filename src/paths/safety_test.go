package paths

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSafePath(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr error
	}{
		{input: "/my//admin/", want: "my/admin"},
		{input: "/Admin", wantErr: ErrInvalidPath},
		{input: "/server/../secret", wantErr: ErrPathTraversal},
	}
	for _, test := range tests {
		got, err := SafePath(test.input)
		if got != test.want || !errors.Is(err, test.wantErr) {
			t.Errorf("SafePath(%q) = (%q, %v), want (%q, %v)", test.input, got, err, test.want, test.wantErr)
		}
	}
}

func TestSafeResourcePath(t *testing.T) {
	if got, err := SafeResourcePath("/static/css/main.css"); err != nil || got != "static/css/main.css" {
		t.Fatalf("SafeResourcePath() = (%q, %v)", got, err)
	}
	if _, err := SafeResourcePath("../secret"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("SafeResourcePath traversal error = %v", err)
	}
}

func TestSafeDirPath(t *testing.T) {
	got, err := SafeDirPath("/var/lib/countries")
	if err != nil || got != filepath.Clean("/var/lib/countries") {
		t.Fatalf("SafeDirPath() = (%q, %v)", got, err)
	}
	if _, err := SafeDirPath("../escape"); !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("SafeDirPath traversal error = %v", err)
	}
}

func TestSafeFilePath(t *testing.T) {
	base := t.TempDir()
	want := filepath.Join(base, "images", "logo.svg")
	got, err := SafeFilePath(base, "images/logo.svg")
	if err != nil || got != want {
		t.Fatalf("SafeFilePath() = (%q, %v), want %q", got, err, want)
	}
	if _, err := SafeFilePath(base, "../secret"); err == nil {
		t.Fatal("SafeFilePath accepted traversal")
	}
}
