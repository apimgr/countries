// Command countrydata rebuilds the embedded country dataset from pinned public sources.
package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	countriesURL   = "https://raw.githubusercontent.com/mledoze/countries/6031f5df137c41c76d180c55a0782185ae3ed05b/countries.json"
	populationURL  = "https://api.worldbank.org/v2/country/all/indicator/SP.POP.TOTL?date=2024&format=json&per_page=1000"
	tzdbURL        = "https://data.iana.org/time-zones/releases/tzdata2026e.tar.gz"
	populationYear = 2024
)

type countryName struct {
	Common   string `json:"common"`
	Official string `json:"official"`
}

type currency struct {
	Name   string `json:"name"`
	Symbol string `json:"symbol"`
}

type country struct {
	Name           countryName         `json:"name"`
	CCA2           string              `json:"cca2"`
	CCA3           string              `json:"cca3"`
	NumericCode    string              `json:"ccn3"`
	Capital        []string            `json:"capital"`
	Region         string              `json:"region"`
	Subregion      string              `json:"subregion"`
	Currencies     map[string]currency `json:"currencies"`
	Languages      map[string]string   `json:"languages"`
	Population     *int64              `json:"population,omitempty"`
	PopulationYear int                 `json:"population_year,omitempty"`
	Area           float64             `json:"area"`
	Flag           string              `json:"flag"`
	FlagSVGURL     string              `json:"flag_svg_url"`
	LatLng         []float64           `json:"latlng"`
	Borders        []string            `json:"borders"`
	Timezones      []string            `json:"timezones"`
	TLD            []string            `json:"tld"`
}

type upstreamCountry struct {
	Name struct {
		Common   string `json:"common"`
		Official string `json:"official"`
	} `json:"name"`
	CCA2        string              `json:"cca2"`
	CCA3        string              `json:"cca3"`
	NumericCode string              `json:"ccn3"`
	Capital     []string            `json:"capital"`
	Region      string              `json:"region"`
	Subregion   string              `json:"subregion"`
	Currencies  map[string]currency `json:"currencies"`
	Languages   map[string]string   `json:"languages"`
	Area        float64             `json:"area"`
	LatLng      []float64           `json:"latlng"`
	Borders     []string            `json:"borders"`
	TLD         []string            `json:"tld"`
}

type populationRecord struct {
	CountryISO3 string `json:"countryiso3code"`
	Date        string `json:"date"`
	Value       *int64 `json:"value"`
}

func main() {
	countriesFile := flag.String("countries", "", "read the country source from a local JSON file")
	populationFile := flag.String("population", "", "read the World Bank source from a local JSON file")
	tzdbFile := flag.String("tzdb", "", "read the IANA tzdb source from a local tar.gz file")
	outputFile := flag.String("out", "src/data/countries.json", "output dataset path")
	flag.Parse()

	if err := generate(*countriesFile, *populationFile, *tzdbFile, *outputFile); err != nil {
		fatal(err)
	}
}

func generate(countriesFile, populationFile, tzdbFile, outputFile string) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	countryData, err := readSource(client, countriesFile, countriesURL)
	if err != nil {
		return fmt.Errorf("read country source: %w", err)
	}
	populationData, err := readSource(client, populationFile, populationURL)
	if err != nil {
		return fmt.Errorf("read population source: %w", err)
	}
	tzdbData, err := readSource(client, tzdbFile, tzdbURL)
	if err != nil {
		return fmt.Errorf("read tzdb source: %w", err)
	}

	var upstream []upstreamCountry
	if err := json.Unmarshal(countryData, &upstream); err != nil {
		return fmt.Errorf("decode country source: %w", err)
	}
	populations, err := decodePopulations(populationData)
	if err != nil {
		return fmt.Errorf("decode population source: %w", err)
	}
	timezones, err := decodeTimezones(tzdbData)
	if err != nil {
		return fmt.Errorf("decode tzdb source: %w", err)
	}

	result := make([]country, 0, 249)
	seen := make(map[string]struct{}, 249)
	for _, source := range upstream {
		if len(source.CCA2) != 2 || !asciiLetters(source.CCA2) || source.CCA2 == "XK" {
			continue
		}
		if _, duplicate := seen[source.CCA2]; duplicate {
			return fmt.Errorf("duplicate alpha-2 country code %q", source.CCA2)
		}
		seen[source.CCA2] = struct{}{}
		item := country{
			Name:        countryName{Common: source.Name.Common, Official: source.Name.Official},
			CCA2:        source.CCA2,
			CCA3:        source.CCA3,
			NumericCode: source.NumericCode,
			Capital:     nonNilStrings(source.Capital),
			Region:      source.Region,
			Subregion:   source.Subregion,
			Currencies:  nonNilCurrencies(source.Currencies),
			Languages:   nonNilLanguages(source.Languages),
			Area:        source.Area,
			Flag:        flagEmoji(source.CCA2),
			FlagSVGURL:  "https://flagcdn.io/flags/4x3/" + strings.ToLower(source.CCA2) + ".svg",
			LatLng:      nonNilFloats(source.LatLng),
			Borders:     nonNilStrings(source.Borders),
			Timezones:   append([]string{}, timezones[source.CCA2]...),
			TLD:         nonNilStrings(source.TLD),
		}
		if population := populations[source.CCA3]; population != nil {
			value := *population
			item.Population = &value
			item.PopulationYear = populationYear
		}
		result = append(result, item)
	}
	if len(result) != 249 {
		return fmt.Errorf("source produced %d countries; expected 249", len(result))
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name.Common) < strings.ToLower(result[j].Name.Common)
	})
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode generated dataset: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(outputFile, encoded, 0644); err != nil {
		return fmt.Errorf("write generated dataset: %w", err)
	}
	fmt.Printf("Generated %d country records at %s\n", len(result), outputFile)
	return nil
}

func readSource(client *http.Client, path, sourceURL string) ([]byte, error) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(path, ".tar.gz") {
			return zoneTableFromTar(data)
		}
		return data, nil
	}
	request, err := http.NewRequest(http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "apimgr-countries-dataset-generator/1")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("source returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if len(data) == 10<<20 {
		return nil, errors.New("source exceeds the 10 MiB download limit")
	}
	if strings.Contains(sourceURL, "tzdata") {
		return zoneTableFromTar(data)
	}
	return data, nil
}

func zoneTableFromTar(data []byte) ([]byte, error) {
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("zone1970.tab is missing from tzdb archive")
		}
		if err != nil {
			return nil, err
		}
		if header.Name == "zone1970.tab" {
			return io.ReadAll(io.LimitReader(reader, 1<<20))
		}
	}
}

func decodePopulations(data []byte) (map[string]*int64, error) {
	var envelope []json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if len(envelope) < 2 {
		return nil, errors.New("response from World Bank has no data array")
	}
	var rows []populationRecord
	if err := json.Unmarshal(envelope[1], &rows); err != nil {
		return nil, err
	}
	values := make(map[string]*int64, len(rows))
	for _, row := range rows {
		if row.Date == fmt.Sprint(populationYear) && row.CountryISO3 != "" && row.Value != nil {
			values[row.CountryISO3] = row.Value
		}
	}
	return values, nil
}

func decodeTimezones(data []byte) (map[string][]string, error) {
	zones := make(map[string][]string)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			return nil, fmt.Errorf("invalid zone1970.tab record %q", line)
		}
		for _, code := range strings.Split(fields[0], ",") {
			zones[code] = append(zones[code], fields[2])
		}
	}
	return zones, nil
}

func flagEmoji(code string) string {
	first, second := rune(code[0]), rune(code[1])
	if first < 'A' || first > 'Z' || second < 'A' || second > 'Z' {
		return ""
	}
	return string([]rune{0x1F1E6 + first - 'A', 0x1F1E6 + second - 'A'})
}

func asciiLetters(value string) bool {
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilFloats(values []float64) []float64 {
	if values == nil {
		return []float64{}
	}
	return values
}

func nonNilCurrencies(values map[string]currency) map[string]currency {
	if values == nil {
		return map[string]currency{}
	}
	return values
}

func nonNilLanguages(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
