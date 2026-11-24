package config

import (
	"os"
	"path/filepath"
	"sort"

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
	appendYAMLConfigSource(&sources, serverConfigPath, serverConfigPath)

	dropInDirectory := filepath.Join(filepath.Dir(serverConfigPath), "yaml.d")
	if ensurePrivateYAMLDirectory(dropInDirectory) == nil {
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
				appendYAMLConfigSource(&sources, path, resolved)
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
