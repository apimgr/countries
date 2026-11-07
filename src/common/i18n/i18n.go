// Package i18n provides the embedded translations shared by application binaries.
package i18n

import (
	"embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFiles embed.FS

type locale struct {
	Meta struct {
		Language   string `json:"language"`
		Name       string `json:"name"`
		NativeName string `json:"native_name"`
		Direction  string `json:"direction"`
	} `json:"meta"`
	Values map[string]string `json:"-"`
}

var (
	loadOnce sync.Once
	locales  map[string]locale
)

func load() {
	locales = make(map[string]locale)
	entries, _ := localeFiles.ReadDir("locales")
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := localeFiles.ReadFile("locales/" + entry.Name())
		if err != nil {
			continue
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		var item locale
		_ = json.Unmarshal(raw["meta"], &item.Meta)
		item.Values = make(map[string]string)
		for key, value := range raw {
			if key == "meta" {
				continue
			}
			var text string
			if json.Unmarshal(value, &text) == nil {
				item.Values[key] = text
			}
		}
		locales[item.Meta.Language] = item
	}
}

func getLocales() map[string]locale {
	loadOnce.Do(load)
	return locales
}

// IsSupported reports whether the language has an embedded translation catalog.
func IsSupported(lang string) bool {
	_, ok := getLocales()[strings.ToLower(strings.TrimSpace(lang))]
	return ok
}

// Translate returns a translated string, falling back to English and then the key.
func Translate(lang, key string) string {
	catalogs := getLocales()
	lang = strings.ToLower(strings.TrimSpace(lang))
	if value := catalogs[lang].Values[key]; value != "" {
		return value
	}
	if value := catalogs["en"].Values[key]; value != "" {
		return value
	}
	return key
}

// Direction returns the writing direction configured for a language.
func Direction(lang string) string {
	if item, ok := getLocales()[strings.ToLower(strings.TrimSpace(lang))]; ok && item.Meta.Direction != "" {
		return item.Meta.Direction
	}
	return "ltr"
}

// Languages returns supported language codes in stable order.
func Languages() []string {
	return []string{"en", "es", "zh", "fr", "ar", "de", "ja"}
}

// NativeName returns the language's self-name for the language selector.
func NativeName(lang string) string {
	if item, ok := getLocales()[strings.ToLower(strings.TrimSpace(lang))]; ok {
		return item.Meta.NativeName
	}
	return "English"
}

// Keys returns the sorted translation keys in the English catalog.
func Keys() []string {
	keys := make([]string, 0, len(getLocales()["en"].Values))
	for key := range getLocales()["en"].Values {
		keys = append(keys, key)
	}
	return keys
}
