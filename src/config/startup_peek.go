package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// StartupSettings contains the only configuration values needed before the
// server's full configuration load.
type StartupSettings struct {
	Port      string
	Address   string
	Daemonize bool
	PIDFile   bool
}

// PeekStartupSettings reads only the startup settings from server.yml and the
// ordered yaml.d drop-ins. Invalid files are skipped; the full load reports
// their errors later.
func PeekStartupSettings(serverConfigPath string) StartupSettings {
	settings := StartupSettings{PIDFile: true}
	sources := make([]yamlConfigSource, 0)
	startupConfigPath := serverConfigPath
	if _, err := os.Stat(startupConfigPath); os.IsNotExist(err) && filepath.Ext(startupConfigPath) == ".yml" {
		legacyPath := strings.TrimSuffix(startupConfigPath, ".yml") + ".yaml"
		if _, legacyErr := os.Stat(legacyPath); legacyErr == nil {
			startupConfigPath = legacyPath
		}
	}
	appendStartupYAMLConfigSource(&sources, startupConfigPath, startupConfigPath)

	dropInDirectory := filepath.Join(filepath.Dir(serverConfigPath), "yaml.d")
	if info, err := os.Lstat(dropInDirectory); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		entries, err := os.ReadDir(dropInDirectory)
		if err == nil {
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			for _, entry := range entries {
				if !isYAMLConfigDropInName(entry.Name()) {
					continue
				}
				path := filepath.Join(dropInDirectory, entry.Name())
				resolved, eligible, resolveErr := resolveYAMLConfigDropIn(dropInDirectory, path)
				if resolveErr != nil || !eligible {
					continue
				}
				appendStartupYAMLConfigSource(&sources, path, resolved)
			}
		}
	}

	for _, source := range sources {
		if source.Err != nil || source.Node == nil {
			continue
		}
		server := yamlConfigMappingValues(source.Node)["server"]
		if server == nil || server.Kind != yaml.MappingNode {
			continue
		}
		values := yamlConfigMappingValues(server)
		if value := values["port"]; value != nil && value.Kind == yaml.ScalarNode && value.Tag != "!!null" {
			settings.Port = value.Value
		}
		if value := values["address"]; value != nil && value.Kind == yaml.ScalarNode && value.Tag != "!!null" {
			settings.Address = value.Value
		}
		if value := values["daemonize"]; value != nil && value.Kind == yaml.ScalarNode && value.Tag != "!!null" {
			if enabled, err := ParseBool(value.Value, false); err == nil {
				settings.Daemonize = enabled
			}
		}
		if value := values["pidfile"]; value != nil && value.Kind == yaml.ScalarNode && value.Tag != "!!null" {
			if enabled, err := ParseBool(value.Value, true); err == nil {
				settings.PIDFile = enabled
			}
		}
	}
	return settings
}

// PrepareStartupConfig secures the files read during the startup settings peek.
func PrepareStartupConfig(serverConfigPath string) error {
	dropInDirectory := filepath.Join(filepath.Dir(serverConfigPath), "yaml.d")
	if err := ensurePrivateYAMLDirectory(dropInDirectory); err != nil {
		return err
	}
	startupConfigPath := serverConfigPath
	if _, err := os.Stat(startupConfigPath); os.IsNotExist(err) && filepath.Ext(startupConfigPath) == ".yml" {
		legacyPath := strings.TrimSuffix(startupConfigPath, ".yml") + ".yaml"
		if _, legacyErr := os.Stat(legacyPath); legacyErr == nil {
			startupConfigPath = legacyPath
		}
	}
	paths := []string{startupConfigPath}
	entries, err := os.ReadDir(dropInDirectory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if isYAMLConfigDropInName(entry.Name()) {
			path := filepath.Join(dropInDirectory, entry.Name())
			resolved, eligible, err := resolveYAMLConfigDropIn(dropInDirectory, path)
			if err != nil {
				continue
			}
			if eligible {
				paths = append(paths, resolved)
			}
		}
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func appendStartupYAMLConfigSource(sources *[]yamlConfigSource, sourcePath, filePath string) {
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return
	}
	*sources = append(*sources, yamlConfigSource{Path: sourcePath, Node: document.Content[0]})
}
