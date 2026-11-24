package i18n

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var interpolationPattern = regexp.MustCompile(`\{[a-zA-Z][a-zA-Z0-9_]*\}`)

func TestKeyConsistency(t *testing.T) {
	var english map[string]json.RawMessage
	data, err := localeFiles.ReadFile("locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &english); err != nil {
		t.Fatal(err)
	}
	englishMessages := make(map[string]string)
	englishVariables := make(map[string][]string)
	var englishPlurals map[string]map[string]string
	for key, value := range english {
		if key == "meta" || key == "plurals" {
			continue
		}
		flattenTestMessages(englishMessages, key, value)
	}
	englishKeys := make([]string, 0, len(englishMessages)+1)
	for key, message := range englishMessages {
		englishKeys = append(englishKeys, key)
		englishVariables[key] = interpolationPattern.FindAllString(message, -1)
		sort.Strings(englishVariables[key])
	}
	if err := json.Unmarshal(english["plurals"], &englishPlurals); err != nil {
		t.Fatalf("English plurals are invalid: %v", err)
	}
	for key := range englishPlurals {
		englishKeys = append(englishKeys, "plurals."+key)
	}
	sort.Strings(englishKeys)
	for _, lang := range Languages() {
		encoded, err := localeFiles.ReadFile("locales/" + lang + ".json")
		if err != nil {
			t.Fatalf("locale %s missing: %v", lang, err)
		}
		var current map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &current); err != nil {
			t.Fatalf("locale %s: %v", lang, err)
		}
		currentMessages := make(map[string]string)
		var currentPlurals map[string]map[string]string
		for key, value := range current {
			if key == "meta" || key == "plurals" {
				continue
			}
			flattenTestMessages(currentMessages, key, value)
		}
		currentKeys := make([]string, 0, len(currentMessages)+1)
		for key, message := range currentMessages {
			currentKeys = append(currentKeys, key)
			if strings.TrimSpace(message) == "" {
				t.Errorf("locale %s key %q has an empty translation", lang, key)
			}
			variables := interpolationPattern.FindAllString(message, -1)
			sort.Strings(variables)
			if !equalStrings(variables, englishVariables[key]) {
				t.Errorf("locale %s key %q interpolation variables = %v; want %v", lang, key, variables, englishVariables[key])
			}
		}
		if err := json.Unmarshal(current["plurals"], &currentPlurals); err != nil {
			t.Errorf("locale %s plurals are invalid: %v", lang, err)
			continue
		}
		for key, forms := range englishPlurals {
			currentKeys = append(currentKeys, "plurals."+key)
			localized, exists := currentPlurals[key]
			if !exists {
				t.Errorf("locale %s is missing plural message %q", lang, key)
				continue
			}
			registered, _ := registeredLanguage(lang)
			required := make(map[string]bool, len(registered.PluralCategories))
			for _, category := range registered.PluralCategories {
				required[category] = true
				if localized[category] == "" {
					t.Errorf("locale %s plural %q is missing required category %q", lang, key, category)
				}
			}
			for category, message := range localized {
				if !required[category] && category != "zero" {
					t.Errorf("locale %s plural %q has unsupported category %q", lang, key, category)
				}
				if strings.TrimSpace(message) == "" {
					t.Errorf("locale %s plural %q category %q is empty", lang, key, category)
				}
				variables := pluralVariables(message)
				if !equalStrings(variables, pluralVariables(forms["other"])) {
					t.Errorf("locale %s plural %q category %q placeholders = %v; want %v", lang, key, category, variables, pluralVariables(forms["other"]))
				}
			}
		}
		for key := range currentPlurals {
			if _, exists := englishPlurals[key]; !exists {
				t.Errorf("locale %s has orphan plural message %q", lang, key)
			}
		}
		sort.Strings(currentKeys)
		if !equalStrings(currentKeys, englishKeys) {
			t.Errorf("locale %s key set differs from en.json", lang)
		}
	}
}

func flattenTestMessages(messages map[string]string, prefix string, raw json.RawMessage) {
	var message string
	if json.Unmarshal(raw, &message) == nil {
		messages[prefix] = message
		return
	}
	var nested map[string]json.RawMessage
	if json.Unmarshal(raw, &nested) != nil {
		return
	}
	for key, value := range nested {
		flattenTestMessages(messages, prefix+"."+key, value)
	}
}

func pluralVariables(message string) []string {
	variables := interpolationPattern.FindAllString(message, -1)
	nonCount := variables[:0]
	for _, variable := range variables {
		if variable != "{count}" {
			nonCount = append(nonCount, variable)
		}
	}
	sort.Strings(nonCount)
	return nonCount
}

func TestLocalesFS(t *testing.T) {
	entries, err := localeFiles.ReadDir("locales")
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		code := strings.TrimSuffix(entry.Name(), ".json")
		files[code] = true
		registered, ok := registeredLanguage(code)
		if !ok {
			t.Errorf("locale file %s has no language registry entry", entry.Name())
			continue
		}
		var item locale
		encoded, err := localeFiles.ReadFile("locales/" + entry.Name())
		if err != nil {
			t.Errorf("read locale %s: %v", code, err)
			continue
		}
		if err := json.Unmarshal(encoded, &item); err != nil {
			t.Errorf("decode locale %s: %v", code, err)
			continue
		}
		if item.Meta.Language != code || item.Meta.Name != registered.Name || item.Meta.NativeName != registered.NativeName || item.Meta.Direction != registered.Direction {
			t.Errorf("locale %s metadata does not match the language registry", code)
		}
	}
	if len(files) != len(languageRegistry) {
		t.Errorf("embedded locale file count = %d; registry has %d languages", len(files), len(languageRegistry))
	}
	for _, item := range languageRegistry {
		if !files[item.Code] {
			t.Errorf("language registry entry %s has no embedded locale file", item.Code)
		}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestTranslateFallbackAndDirection(t *testing.T) {
	if got := Translate("es", "search.title"); got != "Buscar países" {
		t.Errorf("Spanish title = %q", got)
	}
	if got := Translate("unsupported", "search.title"); got != "Search Countries" {
		t.Errorf("fallback title = %q", got)
	}
	if got := Direction("ar"); got != "rtl" {
		t.Errorf("Arabic direction = %q", got)
	}
}

func TestTranslateFormatUsesLiteralNamedPlaceholders(t *testing.T) {
	got := TranslateFormat("en", "about.title", map[string]string{"app_name": "Countries $1 100%"})
	if got != "About Countries $1 100%" {
		t.Fatalf("TranslateFormat() = %q, want literal placeholder replacement", got)
	}
	if got := TranslateFormat("en", "about.title", nil); got != "About {app_name}" {
		t.Fatalf("TranslateFormat() with missing value = %q, want visible token", got)
	}
}

func TestTrackingProviderLabelsAreLocalized(t *testing.T) {
	providers := []string{"google", "matomo", "piwik", "owa", "fathom", "plausible", "umami", "simple", "cloudflare"}
	for _, language := range Languages() {
		if got := Translate(language, "privacy.analytics_provider"); !strings.Contains(got, "{provider}") {
			t.Errorf("%s analytics provider message = %q, want {provider} placeholder", language, got)
		}
		for _, provider := range providers {
			key := "tracking.provider." + provider
			if got := Translate(language, key); got == key {
				t.Errorf("%s tracking provider label %q is missing", language, provider)
			}
		}
	}
}

func TestPluralCategoryUsesCLDR(t *testing.T) {
	tests := []struct {
		lang  string
		count int
		want  string
	}{
		{"en", 1, "one"},
		{"en", 2, "other"},
		{"fr", 0, "one"},
		{"ar", 0, "zero"},
		{"ar", 2, "two"},
		{"ar", 3, "few"},
		{"ar", 11, "many"},
		{"ar", 100, "other"},
		{"it", 1000000, "many"},
		{"zh", 3, "other"},
	}
	for _, test := range tests {
		if got := pluralCategory(test.lang, test.count); got != test.want {
			t.Errorf("pluralCategory(%q, %d) = %q, want %q", test.lang, test.count, got, test.want)
		}
	}
}

func TestPluralMessageSubstitutionAndFallback(t *testing.T) {
	forms := map[string]string{"one": "{count} item", "other": "{count} items"}
	if got := pluralMessage(forms, "en", 1); got != "1 item" {
		t.Errorf("one form = %q", got)
	}
	if got := pluralMessage(forms, "en", 4); got != "4 items" {
		t.Errorf("other form = %q", got)
	}
	if got := pluralMessage(map[string]string{"zero": "No items", "other": "{count} items"}, "ar", 0); got != "No items" {
		t.Errorf("explicit zero form = %q", got)
	}
}

func TestPublicLocaleAndPluralHelpers(t *testing.T) {
	if !IsSupported(" FR ") || IsSupported("xx") {
		t.Fatal("language support lookup returned unexpected values")
	}
	if got := Translate("en", "missing.translation"); got != "missing.translation" {
		t.Errorf("missing translation fallback = %q", got)
	}
	if got := TranslatePlural("en", "plurals.format_seconds", 1); got != "1 second" {
		t.Errorf("English singular plural = %q", got)
	}
	if got := TranslatePlural("en", "format_seconds", 3); got != "3 seconds" {
		t.Errorf("English plural = %q", got)
	}
	if got := TranslatePlural("unsupported", "plurals.format_minutes", 2); got != "2 minutes" {
		t.Errorf("unsupported-language plural fallback = %q", got)
	}
	if got := TranslatePlural("en", "missing_plural", 2); got != "missing_plural" {
		t.Errorf("missing plural fallback = %q", got)
	}
	if got := NativeName("fr"); got != "Français" {
		t.Errorf("French native name = %q", got)
	}
	if got := NativeName("unsupported"); got != "English" {
		t.Errorf("unknown native name fallback = %q", got)
	}
	keys := Keys()
	if len(keys) == 0 || !sort.StringsAreSorted(keys) {
		t.Fatalf("translation keys are empty or unsorted: %v", keys)
	}
}
