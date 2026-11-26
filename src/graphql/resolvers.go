package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/apimgr/countries/src/countries"
)

type queryResolver struct {
	service *countries.Service
	version string
	reader  APIReader
}

type JSONScalar []byte

func (JSONScalar) ImplementsGraphQLType(name string) bool { return name == "JSON" }

func (value *JSONScalar) UnmarshalGraphQL(input interface{}) error {
	encoded, err := json.Marshal(input)
	if err != nil {
		return err
	}
	*value = JSONScalar(encoded)
	return nil
}

func (value JSONScalar) MarshalJSON() ([]byte, error) {
	if !json.Valid(value) {
		return nil, fmt.Errorf("invalid JSON scalar value")
	}
	return append([]byte(nil), value...), nil
}

func (q *queryResolver) Read(ctx context.Context, args struct{ Path string }) (JSONScalar, error) {
	if q.reader == nil {
		return nil, fmt.Errorf("GraphQL API reads are unavailable")
	}
	request := requestFromContext(ctx)
	if request == nil {
		return nil, fmt.Errorf("GraphQL request context is unavailable")
	}
	path, err := validateReadPath(args.Path)
	if err != nil {
		return nil, fmt.Errorf("path is not an allowlisted read-only API route")
	}
	result, err := q.reader(ctx, request, path)
	if err != nil {
		return nil, errors.New("API read failed")
	}
	if len(result) == 0 || !json.Valid(result) {
		return nil, fmt.Errorf("API route did not return JSON")
	}
	return JSONScalar(append([]byte(nil), result...)), nil
}

type countryResolver struct {
	value countries.Country
}

type distanceResolver struct {
	value countries.CountryWithDistance
}

type statsResolver struct {
	total   int32
	version string
}

func (q *queryResolver) Countries() []*countryResolver {
	items := q.service.GetAll()
	result := make([]*countryResolver, 0, len(items))
	for _, item := range items {
		result = append(result, &countryResolver{value: item})
	}
	return result
}

func (q *queryResolver) Country(_ context.Context, args struct{ Code string }) *countryResolver {
	item := q.service.GetByCode(strings.TrimSpace(args.Code))
	if item == nil {
		return nil
	}
	return &countryResolver{value: *item}
}

func (q *queryResolver) Search(_ context.Context, args struct {
	Query    *string
	Code     *string
	Region   *string
	Currency *string
	Language *string
}) []*countryResolver {
	filters := countries.SearchFilters{}
	if args.Query != nil {
		filters.Query = strings.TrimSpace(*args.Query)
	}
	if args.Code != nil {
		filters.Code = strings.TrimSpace(*args.Code)
	}
	if args.Region != nil {
		filters.Region = strings.TrimSpace(*args.Region)
	}
	if args.Currency != nil {
		filters.Currency = strings.TrimSpace(*args.Currency)
	}
	if args.Language != nil {
		filters.Language = strings.TrimSpace(*args.Language)
	}
	items := q.service.SearchFiltered(filters)
	result := make([]*countryResolver, 0, len(items))
	for _, item := range items {
		result = append(result, &countryResolver{value: item})
	}
	return result
}

func (q *queryResolver) Nearest(_ context.Context, args struct {
	Lat float64
	Lon float64
}) *distanceResolver {
	item := q.service.FindNearest(args.Lat, args.Lon)
	if item == nil {
		return nil
	}
	return &distanceResolver{value: *item}
}

func (q *queryResolver) Nearby(_ context.Context, args struct {
	Lat      float64
	Lon      float64
	RadiusKm float64
}) []*distanceResolver {
	items := q.service.FindNearby(args.Lat, args.Lon, args.RadiusKm)
	result := make([]*distanceResolver, 0, len(items))
	for _, item := range items {
		result = append(result, &distanceResolver{value: item})
	}
	return result
}

func (q *queryResolver) Stats() *statsResolver {
	return &statsResolver{total: int32(q.service.Count()), version: q.version}
}

func (r *countryResolver) Name() *countryNameResolver {
	return &countryNameResolver{value: r.value.Name}
}
func (r *countryResolver) Cca2() string        { return r.value.CCA2 }
func (r *countryResolver) Cca3() string        { return r.value.CCA3 }
func (r *countryResolver) NumericCode() string { return r.value.NumericCode }
func (r *countryResolver) Capital() []string   { return r.value.Capital }
func (r *countryResolver) Region() string      { return r.value.Region }
func (r *countryResolver) Subregion() string   { return r.value.Subregion }
func (r *countryResolver) Currencies() []*currencyResolver {
	codes := make([]string, 0, len(r.value.Currencies))
	for code := range r.value.Currencies {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	items := make([]*currencyResolver, 0, len(codes))
	for _, code := range codes {
		items = append(items, &currencyResolver{code: code, value: r.value.Currencies[code]})
	}
	return items
}
func (r *countryResolver) Languages() []*languageResolver {
	codes := make([]string, 0, len(r.value.Languages))
	for code := range r.value.Languages {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	items := make([]*languageResolver, 0, len(codes))
	for _, code := range codes {
		items = append(items, &languageResolver{code: code, name: r.value.Languages[code]})
	}
	return items
}
func (r *countryResolver) Population() *float64 {
	if r.value.Population == nil {
		return nil
	}
	value := float64(*r.value.Population)
	return &value
}
func (r *countryResolver) PopulationYear() *int32 {
	if r.value.PopulationYear == 0 {
		return nil
	}
	value := int32(r.value.PopulationYear)
	return &value
}
func (r *countryResolver) Area() float64       { return r.value.Area }
func (r *countryResolver) Flag() string        { return r.value.Flag }
func (r *countryResolver) FlagSvgURL() string  { return r.value.FlagSVGURL }
func (r *countryResolver) Timezones() []string { return r.value.Timezones }
func (r *countryResolver) Latlng() []float64   { return r.value.LatLng }
func (r *countryResolver) Borders() []string   { return r.value.Borders }
func (r *countryResolver) Tld() []string       { return r.value.TLD }

type countryNameResolver struct{ value countries.CountryName }

func (r *countryNameResolver) Common() string   { return r.value.Common }
func (r *countryNameResolver) Official() string { return r.value.Official }

type currencyResolver struct {
	code  string
	value countries.Currency
}

func (r *currencyResolver) Code() string   { return r.code }
func (r *currencyResolver) Name() string   { return r.value.Name }
func (r *currencyResolver) Symbol() string { return r.value.Symbol }

type languageResolver struct {
	code string
	name string
}

func (r *languageResolver) Code() string { return r.code }
func (r *languageResolver) Name() string { return r.name }

func (r *distanceResolver) Country() *countryResolver {
	return &countryResolver{value: r.value.Country}
}
func (r *distanceResolver) Distance() float64 { return r.value.Distance }

func (r *statsResolver) TotalCountries() int32 { return r.total }
func (r *statsResolver) Version() string       { return r.version }
