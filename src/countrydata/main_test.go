package main

import (
	"encoding/json"
	"testing"
)

func TestDecodePopulationsUsesPinnedYear(t *testing.T) {
	data := []byte(`[{"page":1},[
		{"countryiso3code":"TST","date":"2024","value":12345},
		{"countryiso3code":"OLD","date":"2023","value":54321},
		{"countryiso3code":"NUL","date":"2024","value":null}
	]]`)
	got, err := decodePopulations(data)
	if err != nil {
		t.Fatalf("decodePopulations() error = %v", err)
	}
	if got["TST"] == nil || *got["TST"] != 12345 {
		t.Errorf("2024 population = %v, want 12345", got["TST"])
	}
	if _, exists := got["OLD"]; exists {
		t.Errorf("out-of-year population was included: %v", got["OLD"])
	}
	if _, exists := got["NUL"]; exists {
		t.Errorf("null population was included")
	}
}

func TestDecodePopulationsRejectsMalformedEnvelope(t *testing.T) {
	for _, data := range [][]byte{[]byte(`{}`), []byte(`[{"page":1},{}]`)} {
		if _, err := decodePopulations(data); err == nil {
			t.Errorf("decodePopulations(%s) succeeded, want error", data)
		}
	}
}

func TestDecodeTimezones(t *testing.T) {
	got, err := decodeTimezones([]byte("# country code\tcoordinates\tTZ identifier\nAA\t+0000+00000\tEtc/UTC\nBB,CC\t+0000+00000\tEtc/Shared\n"))
	if err != nil {
		t.Fatalf("decodeTimezones() error = %v", err)
	}
	if len(got["BB"]) != 1 || got["BB"][0] != "Etc/Shared" || len(got["CC"]) != 1 {
		t.Errorf("shared timezone result = %#v", got)
	}
	if _, err := decodeTimezones([]byte("invalid record\n")); err == nil {
		t.Error("decodeTimezones() accepted malformed input")
	}
}

func TestFlagEmoji(t *testing.T) {
	if got := flagEmoji("US"); got != "🇺🇸" {
		t.Errorf("flagEmoji(US) = %q, want 🇺🇸", got)
	}
	if got := flagEmoji("X1"); got != "" {
		t.Errorf("flagEmoji(X1) = %q, want empty", got)
	}
}

func TestCountryJSONFieldNames(t *testing.T) {
	value := country{Name: countryName{Common: "Test", Official: "Test Republic"}, CCA2: "TS", CCA3: "TST"}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"name", "cca2", "cca3", "ccn3", "capital", "timezones", "flag_svg_url"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("generated record missing %q: %s", key, data)
		}
	}
}
