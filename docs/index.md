# Countries

Countries is a read-only reference service that embeds country records in a Go binary and exposes them through a versioned REST API, GraphQL, a web interface, and a small command-line client. Country lookups do not call an external service at runtime.

## Current data coverage

The checked-in dataset has **249 ISO 3166-1 records**. Records include common and official names, alpha-2/alpha-3/numeric codes, capitals, regions, currencies, languages, population values and observation years where available, area, flag emoji and SVG links, representative coordinates, borders, time zones, and top-level domains. Source versions, license terms, and field-level provenance are documented in [`src/data/README.md`](https://github.com/apimgr/countries/blob/main/src/data/README.md).

## Interfaces

- Browse and search at `/` and `/search`.
- Explore REST at `/server/docs/swagger` and GraphQL at `/server/docs/graphql`.
- Query the public REST API below `/api/v1/`.
- Use the `countries-cli` binary for common lookups.

## Guides

- [Install the server](installation.md)
- [Configure the server and CLI](configuration.md)
- [REST and GraphQL API](api.md)
- [CLI reference](cli.md)
- [Security and privacy](security.md)
- [Integrations and discovery](integrations.md)
- [Development](development.md)
