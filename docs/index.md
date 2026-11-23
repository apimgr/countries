# Countries

Countries is a read-only reference service for 249 ISO 3166-1 country records. It embeds the dataset in the server binary and serves country lookups through a web interface, REST API, GraphQL, and the `countries-cli` client.

## Quick Start

Run the container with persistent named volumes:

```sh
docker run -d --name countries \
  -p 172.17.0.1:64580:80 \
  -v countries-config:/config \
  -v countries-data:/data \
  ghcr.io/apimgr/countries:latest
```

Or start the server binary using its operating-system configuration and data directories:

```sh
./countries-linux-amd64
```

See the [installation guide](installation.md) for platform downloads and service setup.

## Features

- Browse, filter, and search country records by name, ISO code, region, currency, or language.
- Look up countries by code and find nearby countries or the nearest country to coordinates.
- Query the versioned REST API and read-only GraphQL API.
- Connect MCP clients with bearer tokens.
- Use the server-rendered interface without requiring JavaScript for core navigation and forms.

## Documentation

- [Installation](installation.md) — Docker, binaries, and system services
- [Configuration](configuration.md) — server settings and configuration files
- [API reference](api.md) — REST, GraphQL, and response formats
- [CLI reference](cli.md) — server and companion client commands
- [Security](security.md) — authentication, public endpoints, and reporting
- [Integrations](integrations.md) — discovery, MCP, and binary downloads
- [Operations](operations.md) — backups, updates, and service management
- [Development](development.md) — build and contribution guide
- [Translation guide](translating.md) — locale and review process

## Links

- [GitHub repository](https://github.com/apimgr/countries)
- [Swagger UI](http://YOUR_SERVER/server/docs/swagger)
- [GraphQL explorer](http://YOUR_SERVER/server/docs/graphql)
- Live service pages: `http://YOUR_SERVER/server/about`, including help, privacy, security, and preferences. Replace `YOUR_SERVER` with your deployment's host.

## License

MIT — see [LICENSE.md](https://github.com/apimgr/countries/blob/main/LICENSE.md).
