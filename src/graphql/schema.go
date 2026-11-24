// Package graphql generates the GraphQL schema from its Go field annotations.
package graphql

import (
	"fmt"
	"reflect"
	"strings"
)

//go:generate go run ./cmd/schema-gen

type querySchema struct {
	Countries struct{} `graphql:"countries: [Country!]!"`
	Country   struct{} `graphql:"country(code: String!): Country"`
	Search    struct{} `graphql:"search(query: String, code: String, region: String, currency: String, language: String): [Country!]!"`
	Nearest   struct{} `graphql:"nearest(lat: Float!, lon: Float!): CountryDistance"`
	Nearby    struct{} `graphql:"nearby(lat: Float!, lon: Float!, radiusKm: Float!): [CountryDistance!]!"`
	Stats     struct{} `graphql:"stats: Stats!"`
}

type countrySchema struct {
	Name           struct{} `graphql:"name: CountryName!"`
	CCA2           struct{} `graphql:"cca2: String!"`
	CCA3           struct{} `graphql:"cca3: String!"`
	NumericCode    struct{} `graphql:"numericCode: String!"`
	Capital        struct{} `graphql:"capital: [String!]!"`
	Region         struct{} `graphql:"region: String!"`
	Subregion      struct{} `graphql:"subregion: String!"`
	Currencies     struct{} `graphql:"currencies: [Currency!]!"`
	Languages      struct{} `graphql:"languages: [Language!]!"`
	Population     struct{} `graphql:"population: Float"`
	PopulationYear struct{} `graphql:"populationYear: Int"`
	Area           struct{} `graphql:"area: Float!"`
	Flag           struct{} `graphql:"flag: String!"`
	FlagSvgURL     struct{} `graphql:"flagSvgUrl: String!"`
	Timezones      struct{} `graphql:"timezones: [String!]!"`
	Latlng         struct{} `graphql:"latlng: [Float!]!"`
	Borders        struct{} `graphql:"borders: [String!]!"`
	TLD            struct{} `graphql:"tld: [String!]!"`
}

type countryNameSchema struct {
	Common   struct{} `graphql:"common: String!"`
	Official struct{} `graphql:"official: String!"`
}

type currencySchema struct {
	Code   struct{} `graphql:"code: String!"`
	Name   struct{} `graphql:"name: String!"`
	Symbol struct{} `graphql:"symbol: String!"`
}

type languageSchema struct {
	Code struct{} `graphql:"code: String!"`
	Name struct{} `graphql:"name: String!"`
}

type countryDistanceSchema struct {
	Country  struct{} `graphql:"country: Country!"`
	Distance struct{} `graphql:"distance: Float!"`
}

type statsSchema struct {
	TotalCountries struct{} `graphql:"totalCountries: Int!"`
	Version        struct{} `graphql:"version: String!"`
}

type schemaType struct {
	name   string
	typeOf reflect.Type
}

var schemaTypes = []schemaType{
	{name: "Query", typeOf: reflect.TypeOf(querySchema{})},
	{name: "Country", typeOf: reflect.TypeOf(countrySchema{})},
	{name: "CountryName", typeOf: reflect.TypeOf(countryNameSchema{})},
	{name: "Currency", typeOf: reflect.TypeOf(currencySchema{})},
	{name: "Language", typeOf: reflect.TypeOf(languageSchema{})},
	{name: "CountryDistance", typeOf: reflect.TypeOf(countryDistanceSchema{})},
	{name: "Stats", typeOf: reflect.TypeOf(statsSchema{})},
}

// SchemaSDL returns the SDL generated from the graphql struct-tag annotations.
func SchemaSDL() string {
	var output strings.Builder
	for _, schema := range schemaTypes {
		fmt.Fprintf(&output, "type %s {\n", schema.name)
		for index := 0; index < schema.typeOf.NumField(); index++ {
			field := schema.typeOf.Field(index)
			annotation := field.Tag.Get("graphql")
			if annotation == "" {
				continue
			}
			fmt.Fprintf(&output, "  %s\n", annotation)
		}
		output.WriteString("}\n\n")
	}
	return strings.TrimSpace(output.String()) + "\n"
}
