// Package graphql provides the public GraphQL API for embedded country data.
package graphql

const schemaDefinition = `
type Query {
  countries: [Country!]!
  country(code: String!): Country
  search(query: String, code: String, region: String, currency: String, language: String): [Country!]!
  nearest(lat: Float!, lon: Float!): CountryDistance
  nearby(lat: Float!, lon: Float!, radiusKm: Float!): [CountryDistance!]!
  stats: Stats!
}

type Country {
  name: CountryName!
  cca2: String!
  cca3: String!
  numericCode: String!
  capital: [String!]!
  region: String!
  subregion: String!
  currencies: [Currency!]!
  languages: [Language!]!
  population: Float
  populationYear: Int
  area: Float!
  flag: String!
  flagSvgUrl: String!
  timezones: [String!]!
  latlng: [Float!]!
  borders: [String!]!
  tld: [String!]!
}

type CountryName {
  common: String!
  official: String!
}

type Currency {
  code: String!
  name: String!
  symbol: String!
}

type Language {
  code: String!
  name: String!
}

type CountryDistance {
  country: Country!
  distance: Float!
}

type Stats {
  totalCountries: Int!
  version: String!
}
`
