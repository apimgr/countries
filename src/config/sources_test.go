package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/testutil"
	"gopkg.in/yaml.v3"
)

func TestReadYAMLConfigSourcesOrderFilterAndPermissions(t *testing.T) {
	directory := testutil.TempDir(t)
	configPath := filepath.Join(directory, "server.yml")
	dropInDirectory := filepath.Join(directory, "yaml.d")
	if err := os.Mkdir(dropInDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"server.yml":      "server:\n  port: 64001\n",
		"10-web.yaml":     "web:\n  title: first\n",
		"20-pages.yml":    "server:\n  pages:\n    enabled: true\n",
		".hidden.yml":     "web:\n  title: hidden\n",
		"30-backup.yml~":  "web:\n  title: backup\n",
		"40-copy.yml.bak": "web:\n  title: backup\n",
		"50-old.yml.orig": "web:\n  title: old\n",
		"secrets.yml":     "server:\n  token: private\n",
		"ignored.txt":     "web:\n  title: ignored\n",
	}
	for name, content := range files {
		path := filepath.Join(directory, name)
		if name != "server.yml" && name != "secrets.yml" {
			path = filepath.Join(dropInDirectory, name)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dropInDirectory, "60-directory.yml"), 0o755); err != nil {
		t.Fatal(err)
	}

	sources, issues := readYAMLConfigSources(configPath)
	if len(issues) != 0 {
		t.Fatalf("readYAMLConfigSources() issues = %v", issues)
	}
	want := []string{
		configPath,
		filepath.Join(dropInDirectory, "10-web.yaml"),
		filepath.Join(dropInDirectory, "20-pages.yml"),
		filepath.Join(directory, "secrets.yml"),
	}
	got := make([]string, len(sources))
	for index, source := range sources {
		got[index] = source.Path
		if source.Err != nil {
			t.Errorf("source %s error = %v", source.Path, source.Err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source paths = %v, want %v", got, want)
	}
	directoryInfo, err := os.Stat(dropInDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Errorf("yaml.d mode = %04o, want 0700", directoryInfo.Mode().Perm())
	}
	for _, path := range want {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %04o, want 0600", path, info.Mode().Perm())
		}
	}
}

func TestReadYAMLConfigSourcesSymlinkBoundary(t *testing.T) {
	directory := testutil.TempDir(t)
	configPath := filepath.Join(directory, "server.yml")
	dropInDirectory := filepath.Join(directory, "yaml.d")
	if err := os.Mkdir(dropInDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	insideTarget := filepath.Join(dropInDirectory, "actual.conf")
	if err := os.WriteFile(insideTarget, []byte("web:\n  title: inside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideTarget := filepath.Join(directory, "outside.yml")
	if err := os.WriteFile(outsideTarget, []byte("web:\n  title: outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(insideTarget, filepath.Join(dropInDirectory, "10-inside.yml")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if err := os.Symlink(outsideTarget, filepath.Join(dropInDirectory, "20-outside.yaml")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	sources, issues := readYAMLConfigSources(configPath)
	insideLink := filepath.Join(dropInDirectory, "10-inside.yml")
	if len(sources) != 1 || sources[0].Path != insideLink {
		t.Fatalf("drop-in sources = %+v, want only in-directory symlink %s", sources, insideLink)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Error(), "outside yaml.d") {
		t.Fatalf("symlink issues = %v, want outside-target rejection", issues)
	}
}

func TestMergeYAMLConfigNodes(t *testing.T) {
	defaults := parseYAMLConfigTestNode(t, "server:\n  nested:\n    enabled: false\n    remove: keep\n  list:\n    - default\n  scalar: default\n")
	destination := cloneYAMLConfigNode(defaults)
	override := parseYAMLConfigTestNode(t, "server:\n  nested:\n    enabled: true\n    added: value\n  list:\n    - replacement\n  scalar: changed\n")
	issues := mergeYAMLConfigNodes(destination, defaults, override, "20-area.yml")
	if len(issues) != 0 {
		t.Fatalf("merge issues = %v", issues)
	}
	want := parseYAMLConfigTestNode(t, "server:\n  nested:\n    enabled: true\n    remove: keep\n    added: value\n  list:\n    - replacement\n  scalar: changed\n")
	if !yamlConfigTestNodesEqual(destination, want) {
		gotYAML, _ := yaml.Marshal(destination)
		wantYAML, _ := yaml.Marshal(want)
		t.Fatalf("merged YAML =\n%s\nwant\n%s", gotYAML, wantYAML)
	}
}

func TestMergeYAMLConfigNodesRejectsDuplicateAndNull(t *testing.T) {
	defaults := parseYAMLConfigTestNode(t, "server:\n  enabled: false\n  removed: default\n")
	destination := cloneYAMLConfigNode(defaults)
	override := parseYAMLConfigTestNode(t, "server:\n  enabled: true\n  enabled: false\n  removed: null\n")
	issues := mergeYAMLConfigNodes(destination, defaults, override, "30-area.yml")
	if len(issues) != 2 {
		t.Fatalf("merge issues = %v, want duplicate and null issues", issues)
	}
	if !strings.Contains(issues[0].Error(), "30-area.yml:") || !strings.Contains(issues[0].Error(), "server.enabled") || !strings.Contains(issues[1].Error(), "server.removed") {
		t.Fatalf("issues lack source and dotted key context: %v", issues)
	}
	if !yamlConfigTestNodesEqual(destination, defaults) {
		gotYAML, _ := yaml.Marshal(destination)
		wantYAML, _ := yaml.Marshal(defaults)
		t.Fatalf("invalid values did not reset to defaults: got\n%s\nwant\n%s", gotYAML, wantYAML)
	}
}

func TestReadYAMLConfigSourcesKeepsParseErrorSourceIdentity(t *testing.T) {
	directory := testutil.TempDir(t)
	configPath := filepath.Join(directory, "server.yml")
	if err := os.WriteFile(configPath, []byte("server: [unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sources, _ := readYAMLConfigSources(configPath)
	if len(sources) != 1 || sources[0].Path != configPath || sources[0].Err == nil {
		t.Fatalf("sources = %+v, want parse failure retaining %s", sources, configPath)
	}
}

func parseYAMLConfigTestNode(t *testing.T, data string) *yaml.Node {
	t.Helper()
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(data), &document); err != nil {
		t.Fatal(err)
	}
	return document.Content[0]
}

func yamlConfigTestNodesEqual(left, right *yaml.Node) bool {
	leftData, leftErr := yaml.Marshal(left)
	rightData, rightErr := yaml.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftData) == string(rightData)
}
