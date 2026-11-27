package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type yamlConfigSource struct {
	Path string
	Node *yaml.Node
	Err  error
}

type yamlConfigIssue struct {
	Path   string
	Line   int
	Key    string
	Reason string
}

func (issue yamlConfigIssue) Error() string {
	location := issue.Path
	if issue.Line > 0 {
		location = fmt.Sprintf("%s:%d", location, issue.Line)
	}
	if issue.Key == "" {
		return fmt.Sprintf("%s: %s", location, issue.Reason)
	}
	return fmt.Sprintf("%s: %s: %s", location, issue.Key, issue.Reason)
}

func readYAMLConfigSources(serverConfigPath string) ([]yamlConfigSource, []yamlConfigIssue) {
	configDirectory := filepath.Dir(serverConfigPath)
	sources := make([]yamlConfigSource, 0)
	issues := make([]yamlConfigIssue, 0)
	appendYAMLConfigSource(&sources, serverConfigPath, serverConfigPath)

	dropInDirectory := filepath.Join(configDirectory, "yaml.d")
	if err := ensurePrivateYAMLDirectory(dropInDirectory); err != nil {
		issues = append(issues, yamlConfigIssue{Path: dropInDirectory, Reason: err.Error()})
	} else {
		entries, err := os.ReadDir(dropInDirectory)
		if err != nil {
			issues = append(issues, yamlConfigIssue{Path: dropInDirectory, Reason: err.Error()})
		} else {
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			for _, entry := range entries {
				if !isYAMLConfigDropInName(entry.Name()) {
					continue
				}
				path := filepath.Join(dropInDirectory, entry.Name())
				resolvedPath, eligible, err := resolveYAMLConfigDropIn(dropInDirectory, path)
				if err != nil {
					issues = append(issues, yamlConfigIssue{Path: path, Reason: err.Error()})
					continue
				}
				if !eligible {
					continue
				}
				appendYAMLConfigSource(&sources, path, resolvedPath)
			}
		}
	}
	secretsPath := filepath.Join(configDirectory, "secrets.yml")
	appendYAMLConfigSource(&sources, secretsPath, secretsPath)
	return sources, issues
}

func appendYAMLConfigSource(sources *[]yamlConfigSource, sourcePath, filePath string) {
	info, err := os.Lstat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		*sources = append(*sources, yamlConfigSource{Path: sourcePath, Err: err})
		return
	}
	if !info.Mode().IsRegular() {
		*sources = append(*sources, yamlConfigSource{Path: sourcePath, Err: fmt.Errorf("configuration source is not a regular file")})
		return
	}
	if err := os.Chmod(filePath, 0o600); err != nil {
		*sources = append(*sources, yamlConfigSource{Path: sourcePath, Err: fmt.Errorf("secure configuration source: %w", err)})
		return
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		*sources = append(*sources, yamlConfigSource{Path: sourcePath, Err: err})
		return
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		*sources = append(*sources, yamlConfigSource{Path: sourcePath, Err: err})
		return
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		*sources = append(*sources, yamlConfigSource{Path: sourcePath, Err: fmt.Errorf("configuration document must be a mapping")})
		return
	}
	*sources = append(*sources, yamlConfigSource{Path: sourcePath, Node: document.Content[0]})
}

func ensurePrivateYAMLDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("yaml.d must be a directory")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	info, err = os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("yaml.d must be a directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure yaml.d directory: %w", err)
	}
	return nil
}

func isYAMLConfigDropInName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") || strings.HasSuffix(name, ".orig") || strings.HasSuffix(name, ".bak") {
		return false
	}
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
}

func resolveYAMLConfigDropIn(directory, path string) (string, bool, error) {
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return "", false, err
	}
	resolvedPath := path
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		resolvedPath, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", false, err
		}
		relativePath, err := filepath.Rel(directory, resolvedPath)
		if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
			return "", false, fmt.Errorf("drop-in symlink target is outside yaml.d")
		}
	} else if !linkInfo.Mode().IsRegular() {
		return "", false, nil
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, nil
	}
	return resolvedPath, true, nil
}

func mergeYAMLConfigNodes(destination, defaults, override *yaml.Node, source string) []yamlConfigIssue {
	issues := make([]yamlConfigIssue, 0)
	if destination == nil || defaults == nil || override == nil || destination.Kind != yaml.MappingNode || defaults.Kind != yaml.MappingNode || override.Kind != yaml.MappingNode {
		return append(issues, yamlConfigIssue{Path: source, Reason: "configuration merge requires mapping nodes"})
	}
	mergeYAMLConfigMapping(destination, defaults, override, source, nil, &issues)
	return issues
}

func mergeYAMLConfigMapping(destination, defaults, override *yaml.Node, source string, prefix []string, issues *[]yamlConfigIssue) {
	keys := make(map[string]*yaml.Node)
	defaultValues := yamlConfigMappingValues(defaults)
	for index := 0; index+1 < len(override.Content); index += 2 {
		key := override.Content[index]
		value := override.Content[index+1]
		if key.Kind != yaml.ScalarNode {
			*issues = append(*issues, yamlConfigIssue{Path: source, Line: key.Line, Key: strings.Join(prefix, "."), Reason: "mapping key must be a scalar"})
			continue
		}
		if _, duplicate := keys[key.Value]; duplicate {
			resetYAMLConfigKey(destination, key.Value, defaultValues[key.Value])
			keyPath := append(append([]string(nil), prefix...), key.Value)
			*issues = append(*issues, yamlConfigIssue{Path: source, Line: key.Line, Key: strings.Join(keyPath, "."), Reason: "duplicate key"})
			continue
		}
		keys[key.Value] = key
		keyPath := append(append([]string(nil), prefix...), key.Value)
		if value.Tag == "!!null" {
			resetYAMLConfigKey(destination, key.Value, defaultValues[key.Value])
			*issues = append(*issues, yamlConfigIssue{Path: source, Line: value.Line, Key: strings.Join(keyPath, "."), Reason: "null is not allowed"})
			continue
		}
		currentValues := yamlConfigMappingValues(destination)
		current := currentValues[key.Value]
		defaultValue := defaultValues[key.Value]
		if value.Kind == yaml.MappingNode && current != nil && current.Kind == yaml.MappingNode {
			if defaultValue == nil || defaultValue.Kind != yaml.MappingNode {
				defaultValue = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}
			mergeYAMLConfigMapping(current, defaultValue, value, source, keyPath, issues)
			continue
		}
		if value.Kind == yaml.SequenceNode {
			invalidValues := yamlConfigSequenceIssues(value, source, keyPath)
			if len(invalidValues) > 0 {
				resetYAMLConfigKey(destination, key.Value, defaultValue)
				*issues = append(*issues, invalidValues...)
				continue
			}
		}
		if value.Kind == yaml.MappingNode && (current == nil || current.Kind != yaml.MappingNode) {
			invalidValues := yamlConfigMappingIssues(value, source, keyPath)
			if len(invalidValues) > 0 {
				resetYAMLConfigKey(destination, key.Value, defaultValue)
				*issues = append(*issues, invalidValues...)
				continue
			}
		}
		setYAMLConfigKey(destination, key.Value, cloneYAMLConfigNode(value))
	}
}

func yamlConfigMappingIssues(mapping *yaml.Node, source string, path []string) []yamlConfigIssue {
	issues := make([]yamlConfigIssue, 0)
	seen := make(map[string]bool)
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key, value := mapping.Content[index], mapping.Content[index+1]
		if key.Kind != yaml.ScalarNode {
			issues = append(issues, yamlConfigIssue{Path: source, Line: key.Line, Key: strings.Join(path, "."), Reason: "mapping key must be a scalar"})
			continue
		}
		keyPath := append(append([]string(nil), path...), key.Value)
		if seen[key.Value] {
			issues = append(issues, yamlConfigIssue{Path: source, Line: key.Line, Key: strings.Join(keyPath, "."), Reason: "duplicate key"})
			continue
		}
		seen[key.Value] = true
		if value.Tag == "!!null" {
			issues = append(issues, yamlConfigIssue{Path: source, Line: value.Line, Key: strings.Join(keyPath, "."), Reason: "null is not allowed"})
			continue
		}
		switch value.Kind {
		case yaml.MappingNode:
			issues = append(issues, yamlConfigMappingIssues(value, source, keyPath)...)
		case yaml.SequenceNode:
			issues = append(issues, yamlConfigSequenceIssues(value, source, keyPath)...)
		}
	}
	return issues
}

func yamlConfigSequenceIssues(sequence *yaml.Node, source string, path []string) []yamlConfigIssue {
	issues := make([]yamlConfigIssue, 0)
	var visit func(*yaml.Node, []string)
	visit = func(node *yaml.Node, currentPath []string) {
		if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
			issues = append(issues, yamlConfigIssue{Path: source, Line: node.Line, Key: strings.Join(currentPath, "."), Reason: "null is not allowed"})
			return
		}
		if node.Kind == yaml.MappingNode {
			seen := make(map[string]bool)
			for index := 0; index+1 < len(node.Content); index += 2 {
				key := node.Content[index]
				value := node.Content[index+1]
				if key.Kind != yaml.ScalarNode {
					issues = append(issues, yamlConfigIssue{Path: source, Line: key.Line, Key: strings.Join(currentPath, "."), Reason: "mapping key must be a scalar"})
					continue
				}
				keyPath := append(append([]string(nil), currentPath...), key.Value)
				if seen[key.Value] {
					issues = append(issues, yamlConfigIssue{Path: source, Line: key.Line, Key: strings.Join(keyPath, "."), Reason: "duplicate key"})
					continue
				}
				seen[key.Value] = true
				visit(value, keyPath)
			}
			return
		}
		if node.Kind == yaml.SequenceNode {
			for _, child := range node.Content {
				visit(child, currentPath)
			}
		}
	}
	for _, item := range sequence.Content {
		visit(item, path)
	}
	return issues
}

func yamlConfigMappingValues(mapping *yaml.Node) map[string]*yaml.Node {
	values := make(map[string]*yaml.Node)
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return values
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key := mapping.Content[index]
		if key.Kind == yaml.ScalarNode {
			values[key.Value] = mapping.Content[index+1]
		}
	}
	return values
}

func resetYAMLConfigKey(mapping *yaml.Node, key string, fallback *yaml.Node) {
	if fallback == nil {
		removeYAMLConfigKey(mapping, key)
		return
	}
	setYAMLConfigKey(mapping, key, cloneYAMLConfigNode(fallback))
}

func resetYAMLConfigPath(destination, defaults *yaml.Node, path []string) {
	if len(path) == 0 {
		return
	}
	for _, key := range path[:len(path)-1] {
		destination = mappingNodeValue(destination, key)
		defaults = mappingNodeValue(defaults, key)
		if destination == nil || destination.Kind != yaml.MappingNode {
			return
		}
	}
	resetYAMLConfigKey(destination, path[len(path)-1], mappingNodeValue(defaults, path[len(path)-1]))
}

func yamlConfigLineAtPath(node *yaml.Node, path []string) int {
	for _, key := range path {
		node = mappingNodeValue(node, key)
		if node == nil {
			return 0
		}
	}
	if node == nil {
		return 0
	}
	return node.Line
}

func configErrorPath(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(strings.SplitN(err.Error(), "\n", 2)[0])
	field := strings.Fields(message)
	if len(field) == 0 {
		return ""
	}
	path := strings.TrimSuffix(field[0], ":")
	if !strings.Contains(path, ".") {
		return ""
	}
	for _, char := range path {
		if char != '.' && char != '_' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return ""
		}
	}
	return path
}

func setYAMLConfigKey(mapping *yaml.Node, key string, value *yaml.Node) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Kind == yaml.ScalarNode && mapping.Content[index].Value == key {
			mapping.Content[index+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func removeYAMLConfigKey(mapping *yaml.Node, key string) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Kind == yaml.ScalarNode && mapping.Content[index].Value == key {
			mapping.Content = append(mapping.Content[:index], mapping.Content[index+2:]...)
			return
		}
	}
}

func cloneYAMLConfigNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	clone := *node
	clone.Content = make([]*yaml.Node, len(node.Content))
	for index, child := range node.Content {
		clone.Content[index] = cloneYAMLConfigNode(child)
	}
	return &clone
}
