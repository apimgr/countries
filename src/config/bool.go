package config

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

var truthyValues = map[string]bool{
	"1": true, "y": true, "t": true,
	"yes": true, "true": true, "on": true, "ok": true,
	"enable": true, "enabled": true,
	"yep": true, "yup": true, "yeah": true,
	"aye": true, "si": true, "oui": true, "da": true, "hai": true,
	"affirmative": true, "accept": true, "allow": true, "grant": true,
	"sure": true, "totally": true,
}

var falsyValues = map[string]bool{
	"0": true, "n": true, "f": true,
	"no": true, "false": true, "off": true, "deny": true,
	"disable": true, "disabled": true,
	"nope": true, "nah": true, "nay": true,
	"nein": true, "non": true, "niet": true, "iie": true, "lie": true,
	"negative": true, "reject": true, "block": true, "revoke": true,
	"never": true, "noway": true,
}

var invalidBooleanWarnings sync.Map

// ParseBool accepts the truthy and falsy values documented by AI.md.
func ParseBool(value string, defaultValue bool) (bool, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return defaultValue, nil
	}
	if truthyValues[value] {
		return true, nil
	}
	if falsyValues[value] {
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean value: %q", value)
}

// ParseBoolOrDefault keeps invalid configuration values from aborting startup
// and emits at most one warning for each setting name.
func ParseBoolOrDefault(name, value string, defaultValue bool) bool {
	parsed, err := ParseBool(value, defaultValue)
	if err == nil {
		return parsed
	}
	if _, loaded := invalidBooleanWarnings.LoadOrStore(name, struct{}{}); !loaded {
		slog.Warn("invalid boolean, using default", "setting", name, "value", value, "default", defaultValue)
	}
	return defaultValue
}

// IsTruthy reports whether value is one of the recognized truthy values.
func IsTruthy(value string) bool {
	parsed, err := ParseBool(value, false)
	return err == nil && parsed
}

func parseYAMLBool(node *yaml.Node, current bool) (bool, error) {
	if node == nil {
		return current, nil
	}
	return ParseBool(node.Value, current)
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}
