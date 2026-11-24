package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/testutil"
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

func TestZoneTableFromCompressedAndPlainTar(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	content := []byte("AA\t+0000+00000\tEtc/UTC\n")
	if err := writer.WriteHeader(&tar.Header{Name: "zone1970.tab", Mode: 0600, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := zoneTableFromTar(archive.Bytes()); err != nil || string(got) != string(content) {
		t.Fatalf("zoneTableFromTar(plain) = %q, %v", got, err)
	}
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	if _, err := gzipWriter.Write(archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := zoneTableFromTar(compressed.Bytes()); err != nil || string(got) != string(content) {
		t.Fatalf("zoneTableFromTar(gzip) = %q, %v", got, err)
	}
	if _, err := zoneTableFromTar([]byte("not an archive")); err == nil {
		t.Fatal("zoneTableFromTar accepted a non-archive")
	}
}

func TestReadSourceLocalFiles(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "source.json")
	if err := os.WriteFile(path, []byte(`{"ok":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := readSource(nil, path, "unused")
	if err != nil || string(data) != `{"ok":true}` {
		t.Fatalf("readSource(local) = %q, %v", data, err)
	}
	if _, err := readSource(nil, filepath.Join(filepath.Dir(path), "missing.json"), "unused"); err == nil {
		t.Fatal("readSource accepted a missing local file")
	}
}

func TestGenerateCountryDatasetFromLocalFixtures(t *testing.T) {
	var countriesFixture []upstreamCountry
	for first := byte('A'); first <= 'Z' && len(countriesFixture) < 249; first++ {
		for second := byte('A'); second <= 'Z' && len(countriesFixture) < 249; second++ {
			code := string([]byte{first, second})
			if code == "XK" {
				continue
			}
			var item upstreamCountry
			item.Name.Common = fmt.Sprintf("Country %03d", 249-len(countriesFixture))
			item.Name.Official = item.Name.Common
			item.CCA2 = code
			item.CCA3 = fmt.Sprintf("C%02d", len(countriesFixture))
			countriesFixture = append(countriesFixture, item)
		}
	}
	countryData, err := json.Marshal(countriesFixture)
	if err != nil {
		t.Fatal(err)
	}
	populationData := []byte(`[{"page":1},[{"countryiso3code":"C00","date":"2024","value":123}]]`)
	zoneData := []byte("AA\t+0000+00000\tEtc/UTC\n")
	directory := testutil.TempDir(t)
	countriesPath := filepath.Join(directory, "countries.json")
	populationPath := filepath.Join(directory, "population.json")
	zonePath := filepath.Join(directory, "zone1970.tab")
	outputPath := filepath.Join(directory, "generated.json")
	for path, data := range map[string][]byte{countriesPath: countryData, populationPath: populationData, zonePath: zoneData} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := generate(countriesPath, populationPath, zonePath, outputPath); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	var result []country
	if err := json.Unmarshal(generated, &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 249 || result[0].Name.Common > result[len(result)-1].Name.Common {
		t.Fatalf("generated dataset count/order = %d, %q .. %q", len(result), result[0].Name.Common, result[len(result)-1].Name.Common)
	}
	if result[0].Capital == nil || result[0].Currencies == nil || result[0].Languages == nil || result[0].LatLng == nil || result[0].Borders == nil || result[0].TLD == nil {
		t.Fatal("generated records must use non-nil empty collections")
	}
	if !strings.HasSuffix(string(generated), "\n") {
		t.Fatal("generated country dataset must end with a newline")
	}
}

func TestGenerateRejectsDuplicateAndUnexpectedCountryCounts(t *testing.T) {
	directory := testutil.TempDir(t)
	countriesPath := filepath.Join(directory, "countries.json")
	populationPath := filepath.Join(directory, "population.json")
	zonePath := filepath.Join(directory, "zone1970.tab")
	if err := os.WriteFile(populationPath, []byte(`[{"page":1},[]]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zonePath, []byte("AA\t+0000+00000\tEtc/UTC\n"), 0600); err != nil {
		t.Fatal(err)
	}
	duplicate := []upstreamCountry{{CCA2: "AA", CCA3: "AAA"}, {CCA2: "AA", CCA3: "AAB"}}
	data, err := json.Marshal(duplicate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(countriesPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := generate(countriesPath, populationPath, zonePath, filepath.Join(directory, "duplicate.json")); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("generate(duplicate) error = %v", err)
	}
	if err := os.WriteFile(countriesPath, []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := generate(countriesPath, populationPath, zonePath, filepath.Join(directory, "short.json")); err == nil || !strings.Contains(err.Error(), "expected 249") {
		t.Fatalf("generate(short) error = %v", err)
	}
}

func TestCollectionNormalizationHelpers(t *testing.T) {
	if got := nonNilStrings(nil); got == nil || len(got) != 0 {
		t.Fatal("nonNilStrings(nil) returned nil")
	}
	if got := nonNilFloats(nil); got == nil || len(got) != 0 {
		t.Fatal("nonNilFloats(nil) returned nil")
	}
	if got := nonNilCurrencies(nil); got == nil || len(got) != 0 {
		t.Fatal("nonNilCurrencies(nil) returned nil")
	}
	if got := nonNilLanguages(nil); got == nil || len(got) != 0 {
		t.Fatal("nonNilLanguages(nil) returned nil")
	}
	if got := nonNilStrings([]string{"x"}); len(got) != 1 || got[0] != "x" {
		t.Fatal("nonNilStrings replaced a non-nil slice")
	}
}
