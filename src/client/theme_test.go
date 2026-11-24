package main

import "testing"

func TestTUIThemeSelection(t *testing.T) {
	if got := tuiTheme(""); got.Name != "dark" {
		t.Errorf("default theme = %q, want dark", got.Name)
	}
	if got := tuiTheme("light"); got.Name != "light" {
		t.Errorf("light theme = %q, want light", got.Name)
	}
	if got := tuiTheme("unsupported"); got.Name != "dark" {
		t.Errorf("unsupported theme = %q, want dark fallback", got.Name)
	}
}

func TestClientExitCodeMapping(t *testing.T) {
	for _, code := range []int{1, 2, 3, 4, 5, 64} {
		err := withClientExitCode(code, errClientTest{})
		if got := clientExitCode(err); got != code {
			t.Errorf("clientExitCode(%d) = %d", code, got)
		}
	}
	if got := clientExitCode(errClientTest{}); got != 1 {
		t.Errorf("default client exit code = %d, want 1", got)
	}
}

type errClientTest struct{}

func (errClientTest) Error() string { return "test error" }
