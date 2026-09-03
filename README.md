# Countries API 🌍

A comprehensive REST API providing detailed information about countries worldwide, built with Express.js and featuring a beautiful Dracula-themed frontend.

## Features

- ✅ **REST API** with versioning (`/api/v1`)
- 🔒 **Security** with Helmet middleware
- ⏰ **Rate limiting** (1000 requests/hour)
- 🌐 **CORS** enabled for all origins
- 📚 **Swagger documentation**
- 🎨 **EJS frontend** with Dracula theme
- 🐳 **Docker support** with health checks
- 📊 **249 countries** with comprehensive data

## Quick Start

### Using Docker (Recommended)

```bash
# Build and run with Docker
docker build -t countries-api .
docker run -p 3000:3000 countries-api

# Or use Docker Compose
docker-compose up -d
```

### Local Development

```bash
# Install dependencies
npm install

# Start the server
npm start
```

## API Endpoints

### Base URL: `/api/v1`

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/countries` | Get all countries |
| GET | `/countries/:code` | Get country by ISO code |
| GET | `/countries/search/:name` | Search countries by name |

## Documentation

- **Swagger UI**: [http://localhost:3000/docs](http://localhost:3000/docs)
- **Swagger JSON**: [http://localhost:3000/api/docs](http://localhost:3000/api/docs)
- **Frontend**: [http://localhost:3000/](http://localhost:3000/)

## Example API Calls

```bash
# Get all countries
curl http://localhost:3000/api/v1/countries

# Get specific country (USA)
curl http://localhost:3000/api/v1/countries/US

# Search for countries containing "United"
curl http://localhost:3000/api/v1/countries/search/United
```

## Response Format

```json
{
  "timezones": ["America/New_York", "America/Chicago", "..."],
  "latlng": [38, -97],
  "name": "United States",
  "country_code": "US",
  "capital": "Washington D.C."
}
```

## Security Features

- **Helmet**: Security headers protection
- **Rate Limiting**: 1000 requests per hour per IP
- **CORS**: Cross-origin resource sharing enabled
- **Health Checks**: Built-in health monitoring
- **Non-root User**: Docker container runs as non-root user

## Development

### Project Structure

```
countries/
├── server.js              # Main application file
├── countries.json          # Countries data
├── healthcheck.js          # Health check script
├── package.json           # Dependencies
├── Dockerfile             # Docker configuration
├── docker-compose.yml     # Docker Compose setup
├── .dockerignore          # Docker ignore file
├── public/                # Static assets
│   ├── css/style.css      # Dracula theme styles
│   └── js/main.js         # Frontend JavaScript
└── views/                 # EJS templates
    ├── index.ejs          # Homepage
    ├── country.ejs        # Country detail page
    ├── 404.ejs            # 404 error page
    └── error.ejs          # Error page
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `3000` | Server port |
| `NODE_ENV` | `production` | Environment mode |

### Docker Commands

```bash
# Build image
docker build -t countries-api .

# Run container
docker run -d --name countries-api -p 3000:3000 countries-api

# Check health
docker exec countries-api node healthcheck.js

# View logs
docker logs countries-api

# Stop and remove
docker stop countries-api && docker rm countries-api
```

### Docker Compose Commands

```bash
# Start services
docker-compose up -d

# View logs
docker-compose logs -f

# Stop services
docker-compose down

# Rebuild and restart
docker-compose up -d --build
```

### Development Mode

For development with hot reload:

```bash
# Start development version
docker-compose --profile dev up -d

# This will run on port 3001 with volume mounting
```

## Health Monitoring

The application includes built-in health checks:

- **Health endpoint**: Internal health check script
- **Docker health check**: Automatic container health monitoring
- **API status**: Monitors API endpoint availability

## Troubleshooting

### Common Issues

1. **Port already in use**
   ```bash
   # Find process using port 3000
   lsof -i :3000
   
   # Kill process
   kill -9 <PID>
   ```

2. **Docker build issues**
   ```bash
   # Clean Docker cache
   docker system prune -a
   
   # Rebuild without cache
   docker build --no-cache -t countries-api .
   ```

3. **Rate limiting**
   - Default limit: 1000 requests/hour
   - Wait for limit reset or modify in `server.js`

### Performance Tips

- Use Docker for consistent environments
- Enable Docker health checks for monitoring
- Consider adding Redis for caching in production
- Monitor API response times with built-in logging

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make changes
4. Test with Docker
5. Submit a pull request

## License

ISC License - see package.json for details

## Tech Stack

- **Backend**: Node.js, Express.js
- **Frontend**: EJS, Custom CSS (Dracula theme)
- **Security**: Helmet, Rate limiting, CORS
- **Documentation**: Swagger/OpenAPI
- **Containerization**: Docker, Docker Compose
- **Data**: JSON file with 249 countries

---

🚀 **Ready to explore the world's countries?** Start the server and visit [http://localhost:3000](http://localhost:3000)!