// Package i18n provides the embedded translations shared by application binaries.
package i18n

import (
	"embed"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/text/feature/plural"
	xlanguage "golang.org/x/text/language"
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
	Values  map[string]string            `json:"-"`
	Plurals map[string]map[string]string `json:"plurals"`
}

type language struct {
	Code             string
	Name             string
	NativeName       string
	Direction        string
	PluralCategories []string
}

var languageRegistry = []language{
	{Code: "en", Name: "English", NativeName: "English", Direction: "ltr", PluralCategories: []string{"one", "other"}},
	{Code: "es", Name: "Spanish", NativeName: "Español", Direction: "ltr", PluralCategories: []string{"one", "other"}},
	{Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr", PluralCategories: []string{"one", "other"}},
	{Code: "ar", Name: "Arabic", NativeName: "العربية", Direction: "rtl", PluralCategories: []string{"zero", "one", "two", "few", "many", "other"}},
	{Code: "de", Name: "German", NativeName: "Deutsch", Direction: "ltr", PluralCategories: []string{"one", "other"}},
	{Code: "it", Name: "Italian", NativeName: "Italiano", Direction: "ltr", PluralCategories: []string{"one", "many", "other"}},
	{Code: "zh", Name: "Chinese", NativeName: "中文", Direction: "ltr", PluralCategories: []string{"other"}},
	{Code: "ja", Name: "Japanese", NativeName: "日本語", Direction: "ltr", PluralCategories: []string{"other"}},
}

func registeredLanguage(code string) (language, bool) {
	code = strings.ToLower(strings.TrimSpace(code))
	for _, item := range languageRegistry {
		if item.Code == code {
			return item, true
		}
	}
	return language{}, false
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
		_ = json.Unmarshal(raw["plurals"], &item.Plurals)
		item.Values = make(map[string]string)
		for key, value := range raw {
			if key == "meta" || key == "plurals" {
				continue
			}
			flattenMessages(item.Values, key, value)
		}
		locales[item.Meta.Language] = item
	}
}

func flattenMessages(values map[string]string, prefix string, raw json.RawMessage) {
	var message string
	if json.Unmarshal(raw, &message) == nil {
		values[prefix] = message
		return
	}
	var nested map[string]json.RawMessage
	if json.Unmarshal(raw, &nested) != nil {
		return
	}
	for key, value := range nested {
		flattenMessages(values, prefix+"."+key, value)
	}
}

func getLocales() map[string]locale {
	loadOnce.Do(load)
	return locales
}

// IsSupported reports whether the language has an embedded translation catalog.
func IsSupported(lang string) bool {
	_, ok := registeredLanguage(lang)
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

// TranslateFormat substitutes named placeholders literally in a translated message.
func TranslateFormat(lang, key string, args map[string]string) string {
	message := Translate(lang, key)
	for name, value := range args {
		message = strings.ReplaceAll(message, "{"+name+"}", value)
	}
	return message
}

// TranslatePlural selects a localized plural message using CLDR categories.
// Plural messages are stored in each locale's nested "plurals" object.
func TranslatePlural(lang, key string, count int) string {
	catalogs := getLocales()
	lang = strings.ToLower(strings.TrimSpace(lang))
	key = strings.TrimPrefix(key, "plurals.")
	if _, ok := registeredLanguage(lang); !ok {
		lang = "en"
	}
	if value := pluralMessage(catalogs[lang].Plurals[key], lang, count); value != "" {
		return value
	}
	if lang != "en" {
		if value := pluralMessage(catalogs["en"].Plurals[key], "en", count); value != "" {
			return value
		}
	}
	return key
}

func pluralMessage(forms map[string]string, lang string, count int) string {
	if len(forms) == 0 {
		return ""
	}
	category := pluralCategory(lang, count)
	if count == 0 && forms["zero"] != "" {
		category = "zero"
	}
	message := forms[category]
	if message == "" {
		message = forms["other"]
	}
	if message == "" {
		return ""
	}
	return strings.ReplaceAll(message, "{count}", strconv.Itoa(count))
}

func pluralCategory(lang string, count int) string {
	if strings.EqualFold(strings.TrimSpace(lang), "it") && count != 0 && count%1_000_000 == 0 {
		return "many"
	}
	tag, err := xlanguage.Parse(lang)
	if err != nil {
		tag = xlanguage.English
	}
	switch plural.Cardinal.MatchPlural(tag, count, 0, 0, 0, 0) {
	case plural.Zero:
		return "zero"
	case plural.One:
		return "one"
	case plural.Two:
		return "two"
	case plural.Few:
		return "few"
	case plural.Many:
		return "many"
	default:
		return "other"
	}
}

// Direction returns the writing direction configured for a language.
func Direction(lang string) string {
	if item, ok := registeredLanguage(lang); ok {
		return item.Direction
	}
	return "ltr"
}

// Languages returns supported language codes in stable order.
func Languages() []string {
	items := make([]string, 0, len(languageRegistry))
	for _, item := range languageRegistry {
		items = append(items, item.Code)
	}
	return items
}

// NativeName returns the language's self-name for the language selector.
func NativeName(lang string) string {
	if item, ok := registeredLanguage(lang); ok {
		return item.NativeName
	}
	return "English"
}

// Keys returns the sorted translation keys in the English catalog.
func Keys() []string {
	keys := make([]string, 0, len(getLocales()["en"].Values))
	for key := range getLocales()["en"].Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
