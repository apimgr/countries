package countries

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
)

type Country struct {
	Name        string    `json:"name"`
	CountryCode string    `json:"country_code"`
	Capital     string    `json:"capital"`
	Timezones   []string  `json:"timezones"`
	LatLng      []float64 `json:"latlng"`
}

type CountryWithDistance struct {
	Country
	Distance float64 `json:"distance"`
}

type Service struct {
	countries   []Country
	byCode      map[string]*Country
	rawData     json.RawMessage
}

func NewService(jsonData []byte) (*Service, error) {
	var countries []Country
	if err := json.Unmarshal(jsonData, &countries); err != nil {
		return nil, err
	}

	// Build index by country code
	byCode := make(map[string]*Country)
	for i := range countries {
		code := strings.ToUpper(countries[i].CountryCode)
		byCode[code] = &countries[i]
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
	query = strings.ToLower(query)
	var results []Country

	for _, country := range s.countries {
		if strings.Contains(strings.ToLower(country.Name), query) {
			results = append(results, country)
		}
	}

	return results
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
