// Package testutil provides shared test helpers.
package testutil

import (
	"os"

	"github.com/apimgr/countries/src/paths"
)

type testingT interface {
	Helper()
	Cleanup(func())
	Fatalf(string, ...any)
}

// TempDir creates a private project-scoped temporary directory for tests.
func TempDir(t testingT) string {
	t.Helper()
	directory, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatalf("create project-scoped temporary directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Fatalf("remove project-scoped temporary directory: %v", err)
		}
	})
	return directory
}
