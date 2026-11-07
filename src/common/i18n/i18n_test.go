package i18n

import (
	"encoding/json"
	"testing"
)

func TestLocaleKeysMatchEnglish(t *testing.T) {
	var english map[string]json.RawMessage
	data, err := localeFiles.ReadFile("locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &english); err != nil {
		t.Fatal(err)
	}
	for _, lang := range Languages() {
		encoded, err := localeFiles.ReadFile("locales/" + lang + ".json")
		if err != nil {
			t.Fatalf("locale %s missing: %v", lang, err)
		}
		var current map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &current); err != nil {
			t.Fatalf("locale %s: %v", lang, err)
		}
		if len(current) != len(english) {
			t.Errorf("locale %s has %d top-level keys; want %d", lang, len(current), len(english))
		}
		for key := range english {
			if _, ok := current[key]; !ok {
				t.Errorf("locale %s is missing key %s", lang, key)
			}
		}
		for key := range current {
			if _, ok := english[key]; !ok {
				t.Errorf("locale %s has unsupported key %s", lang, key)
			}
		}
	}
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
