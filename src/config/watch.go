package config

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const configWatchInterval = 5 * time.Second

// ConfigWatcher reloads the merged file configuration when a watched path changes.
type ConfigWatcher struct {
	mu         sync.Mutex
	configPath string
	paths      []string
	current    *Config
	snapshot   map[string][32]byte
	onReload   func(previous, next *Config, restartSettings []string)
}

// NewConfigWatcher creates a watcher for server.yml, yaml.d, secrets.yml and assets.
func NewConfigWatcher(configPath string, current *Config, onReload func(previous, next *Config, restartSettings []string)) *ConfigWatcher {
	configDir := filepath.Dir(configPath)
	paths := []string{
		configPath,
		filepath.Join(configDir, "yaml.d"),
		filepath.Join(configDir, "secrets.yml"),
		filepath.Join(configDir, "assets"),
	}
	return &ConfigWatcher{
		configPath: configPath,
		paths:      paths,
		current:    cloneConfig(current),
		snapshot:   watchedConfigSnapshot(paths),
		onReload:   onReload,
	}
}

// Start polls watched paths until the context is canceled.
func (w *ConfigWatcher) Start(ctx context.Context) {
	if w == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(configWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.Check()
			}
		}
	}()
}

// Check reloads the merged configuration if any watched file or directory changed.
func (w *ConfigWatcher) Check() {
	if err := w.check(false); err != nil {
		log.Printf("WARN: configuration reload failed: %v", err)
	}
}

func (w *ConfigWatcher) check(force bool) error {
	if w == nil {
		return fmt.Errorf("configuration watcher is nil")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	nextSnapshot := watchedConfigSnapshot(w.paths)
	if !force && reflect.DeepEqual(w.snapshot, nextSnapshot) {
		return nil
	}
	loaded, err := LoadConfigFromFile(w.configPath)
	if err != nil {
		return err
	}
	w.snapshot = nextSnapshot
	previous := cloneConfig(w.current)
	restartSettings := restartRequiredChanges(previous, loaded)
	live := cloneConfig(loaded)
	preserveRestartRequired(live, previous)
	if len(restartSettings) > 0 {
		log.Printf("WARN: configuration changes require restart: %s", strings.Join(restartSettings, ", "))
	}
	if w.onReload != nil {
		w.onReload(previous, live, restartSettings)
	}
	w.current = cloneConfig(live)
	return nil
}

func watchedConfigSnapshot(paths []string) map[string][32]byte {
	result := make(map[string][32]byte)
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			result[path] = sha256.Sum256([]byte(err.Error()))
			continue
		}
		if !info.IsDir() {
			result[path] = watchedFileDigest(path)
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			result[path] = sha256.Sum256([]byte(err.Error()))
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		var names strings.Builder
		for _, entry := range entries {
			names.WriteString(entry.Name())
			names.WriteByte(0)
			child := filepath.Join(path, entry.Name())
			childInfo, err := os.Lstat(child)
			if err != nil || childInfo.IsDir() {
				continue
			}
			result[child] = watchedFileDigest(child)
		}
		result[path] = sha256.Sum256([]byte(names.String()))
	}
	return result
}

func watchedFileDigest(path string) [32]byte {
	info, err := os.Lstat(path)
	if err != nil {
		return sha256.Sum256([]byte(err.Error()))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return sha256.Sum256([]byte(err.Error()))
		}
		return sha256.Sum256([]byte(target))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return sha256.Sum256([]byte(err.Error()))
	}
	data = append(data, []byte(info.Mode().String())...)
	data = append(data, []byte(info.ModTime().UTC().Format(time.RFC3339Nano))...)
	return sha256.Sum256(data)
}

func cloneConfig(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	node, err := configYAMLNode(cfg)
	if err != nil {
		copy := *cfg
		return &copy
	}
	data, err := yaml.Marshal(node)
	if err != nil {
		copy := *cfg
		return &copy
	}
	copy := DefaultConfig()
	if err := unmarshalConfigYAML(data, copy); err != nil {
		shallowCopy := *cfg
		return &shallowCopy
	}
	copy.ConfigDir = cfg.ConfigDir
	return copy
}

func restartRequiredChanges(previous, next *Config) []string {
	if previous == nil || next == nil {
		return nil
	}
	var changed []string
	add := func(path string, before, after any) {
		if !reflect.DeepEqual(before, after) {
			changed = append(changed, path)
		}
	}
	add("server.port", previous.Server.Port, next.Server.Port)
	add("server.address", previous.Server.Address, next.Server.Address)
	add("server.daemonize", previous.Server.Daemonize, next.Server.Daemonize)
	add("server.ssl", previous.Server.SSL, next.Server.SSL)
	add("server.database", previous.Server.Database, next.Server.Database)
	add("server.tor", previous.Server.Tor, next.Server.Tor)
	sort.Strings(changed)
	return changed
}

// ReloadNow immediately reloads the merged configuration.
func (w *ConfigWatcher) ReloadNow() error {
	return w.check(true)
}

func preserveRestartRequired(destination, active *Config) {
	if destination == nil || active == nil {
		return
	}
	destination.Server.Port = active.Server.Port
	destination.Server.Address = active.Server.Address
	destination.Server.Daemonize = active.Server.Daemonize
	destination.Server.SSL = active.Server.SSL
	destination.Server.Database = active.Server.Database
	destination.Server.Tor = active.Server.Tor
}
