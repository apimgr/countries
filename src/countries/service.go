// Package countries provides country data lookup and geographic search.
package countries

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
)

type CountryName struct {
	Common   string `json:"common"`
	Official string `json:"official"`
}

type Currency struct {
	Name   string `json:"name"`
	Symbol string `json:"symbol"`
}

type Country struct {
	Name           CountryName         `json:"name"`
	CCA2           string              `json:"cca2"`
	CCA3           string              `json:"cca3"`
	NumericCode    string              `json:"ccn3"`
	Capital        []string            `json:"capital"`
	Region         string              `json:"region"`
	Subregion      string              `json:"subregion"`
	Currencies     map[string]Currency `json:"currencies"`
	Languages      map[string]string   `json:"languages"`
	Population     *int64              `json:"population,omitempty"`
	PopulationYear int                 `json:"population_year,omitempty"`
	Area           float64             `json:"area"`
	Flag           string              `json:"flag"`
	FlagSVGURL     string              `json:"flag_svg_url"`
	Timezones      []string            `json:"timezones"`
	LatLng         []float64           `json:"latlng"`
	Borders        []string            `json:"borders"`
	TLD            []string            `json:"tld"`
}

type CountryWithDistance struct {
	Country
	Distance float64 `json:"distance"`
}

type Service struct {
	countries []Country
	byCode    map[string]*Country
	rawData   json.RawMessage
}

func NewService(jsonData []byte) (*Service, error) {
	var countries []Country
	if err := json.Unmarshal(jsonData, &countries); err != nil {
		return nil, err
	}

	// Build index by country code
	byCode := make(map[string]*Country)
	for i := range countries {
		for _, code := range []string{countries[i].CCA2, countries[i].CCA3, countries[i].NumericCode} {
			if code != "" {
				byCode[strings.ToUpper(code)] = &countries[i]
			}
		}
	}

	return &Service{
		countries: countries,
		byCode:    byCode,
		rawData:   jsonData,
	}, nil
}

func (s *Service) Count() int {
	return len(s.countries)
}

func (s *Service) GetAll() []Country {
	return s.countries
}

func (s *Service) GetByCode(code string) *Country {
	return s.byCode[strings.ToUpper(code)]
}

func (s *Service) Search(query string) []Country {
	return s.SearchFiltered(SearchFilters{Query: query})
}

type SearchFilters struct {
	Query    string
	Code     string
	Region   string
	Currency string
	Language string
}

func (s *Service) SearchFiltered(filters SearchFilters) []Country {
	filters.Query = strings.ToLower(strings.TrimSpace(filters.Query))
	filters.Code = strings.ToLower(strings.TrimSpace(filters.Code))
	filters.Region = strings.ToLower(strings.TrimSpace(filters.Region))
	filters.Currency = strings.ToLower(strings.TrimSpace(filters.Currency))
	filters.Language = strings.ToLower(strings.TrimSpace(filters.Language))
	var results []Country

	for _, country := range s.countries {
		if filters.Query != "" && !countryMatches(country, filters.Query) {
			continue
		}
		if filters.Code != "" && !containsAny(filters.Code, country.CCA2, country.CCA3, country.NumericCode) {
			continue
		}
		if filters.Region != "" && !containsAny(filters.Region, country.Region, country.Subregion) {
			continue
		}
		if filters.Currency != "" && !matchesCurrency(country, filters.Currency) {
			continue
		}
		if filters.Language != "" && !matchesLanguage(country, filters.Language) {
			continue
		}
		results = append(results, country)
	}

	return results
}

func countryMatches(country Country, query string) bool {
	if containsAny(query, country.Name.Common, country.Name.Official, country.CCA2, country.CCA3, country.NumericCode, country.Region, country.Subregion, country.Flag) {
		return true
	}
	if containsAny(query, country.Capital...) || containsAny(query, country.Timezones...) || containsAny(query, country.Borders...) || containsAny(query, country.TLD...) {
		return true
	}
	if matchesCurrency(country, query) || matchesLanguage(country, query) {
		return true
	}
	return false
}

func containsAny(query string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func matchesCurrency(country Country, query string) bool {
	for code, currency := range country.Currencies {
		if containsAny(query, code, currency.Name, currency.Symbol) {
			return true
		}
	}
	return false
}

func matchesLanguage(country Country, query string) bool {
	for code, language := range country.Languages {
		if containsAny(query, code, language) {
			return true
		}
	}
	return false
}

func (s *Service) FindNearest(lat, lon float64) *CountryWithDistance {
	if len(s.countries) == 0 {
		return nil
	}

	var nearest *Country
	minDistance := math.MaxFloat64

	for i := range s.countries {
		country := &s.countries[i]
		if len(country.LatLng) < 2 {
			continue
		}

		distance := haversine(lat, lon, country.LatLng[0], country.LatLng[1])
		if distance < minDistance {
			minDistance = distance
			nearest = country
		}
	}

	if nearest == nil {
		return nil
	}

	return &CountryWithDistance{
		Country:  *nearest,
		Distance: math.Round(minDistance*100) / 100,
	}
}

func (s *Service) FindNearby(lat, lon, radiusKm float64) []CountryWithDistance {
	var results []CountryWithDistance

	for _, country := range s.countries {
		if len(country.LatLng) < 2 {
			continue
		}

		distance := haversine(lat, lon, country.LatLng[0], country.LatLng[1])
		if distance <= radiusKm {
			results = append(results, CountryWithDistance{
				Country:  country,
				Distance: math.Round(distance*100) / 100,
			})
		}
	}

	// Sort by distance
	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})

	return results
}

func (s *Service) GetRaw() json.RawMessage {
	return s.rawData
}

func (s *Service) GetRandom() *Country {
	if len(s.countries) == 0 {
		return nil
	}
	// Use a simple random selection
	idx := int(math.Floor(float64(len(s.countries)) * pseudoRandom()))
	return &s.countries[idx]
}

// Simple pseudo-random using time
var seed uint64 = 1

func pseudoRandom() float64 {
	seed = seed*1103515245 + 12345
	return float64(seed%1000000) / 1000000.0
}

// Haversine formula to calculate distance between two points on Earth
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadius = 6371.0 // Earth's radius in kilometers

	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	deltaLat := (lat2 - lat1) * math.Pi / 180
	deltaLon := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*
			math.Sin(deltaLon/2)*math.Sin(deltaLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadius * c
}
