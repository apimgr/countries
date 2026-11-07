// Package server exposes country data through HTTP APIs and web pages.
package server

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
	graphqlapi "github.com/apimgr/countries/src/graphql"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Server struct {
	router           *chi.Mux
	countriesService *countries.Service
	config           *config.Config
	templates        *template.Template
	version          string
	buildDate        string
	commit           string
	trustedProxies   []*net.IPNet
	graphqlHandler   http.Handler
	rateLimiter      *rateLimiter
}

func New(countriesService *countries.Service, cfg *config.Config, address, port, version, buildDate, commit string, databases ...*sql.DB) *Server {
	var db *sql.DB
	if len(databases) > 0 {
		db = databases[0]
	}
	s := &Server{
		router:           chi.NewRouter(),
		countriesService: countriesService,
		config:           cfg,
		version:          version,
		buildDate:        buildDate,
		commit:           commit,
		trustedProxies:   buildTrustedProxies(address, cfg.Server.TrustedProxies.Additional),
		rateLimiter:      newRateLimiter(cfg.Server.RateLimit, db),
	}

	// Parse templates
	tmpl, err := template.New("pages").Funcs(template.FuncMap{
		"t": i18n.Translate,
	}).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		log.Printf("Warning: could not parse templates: %v", err)
	}
	s.templates = tmpl
	graphqlHandler, err := graphqlapi.NewHandler(countriesService, version)
	if err != nil {
		log.Printf("Warning: could not initialize GraphQL schema: %v", err)
	} else {
		s.graphqlHandler = graphqlHandler
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	r := s.router

	// Middleware
	r.Use(requestIDMiddleware)
	r.Use(requestLogMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(s.trustedProxyMiddleware)
	r.Use(s.securityHeadersMiddleware)
	r.Use(s.rateLimiter.middleware)
	r.Use(s.corsMiddleware)

	// Canonical server status page.
	r.Get("/server/healthz", s.handleHealthPage)
	r.Get("/server/about", s.handleInfoPage)
	r.Get("/server/help", s.handleInfoPage)
	r.Get("/server/privacy", s.handleInfoPage)
	r.Get("/server/terms", s.handleInfoPage)
	r.Get("/server/security", s.handleInfoPage)
	r.Get("/server/contact", s.handleInfoPage)
	r.Get("/server/docs/graphql", s.handleGraphQLPage)
	r.Post("/server/preferences", s.handleSavePreferences)

	// Static files
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Web UI
	r.Get("/", s.handleHome)
	r.Get("/search", s.handleSearchPage)
	r.Get("/coordinates", s.handleCoordinatesPage)
	r.Get("/server/docs/swagger", s.handleOpenAPIPage)

	// PWA support
	r.Get("/manifest.json", s.handleManifest)
	r.Get("/sw.js", s.handleServiceWorker)
	r.Get("/robots.txt", s.handleRobots)
	r.Get("/.well-known/security.txt", s.handleSecurity)
	r.Get("/llms.txt", s.handleLLMS)
	r.Get("/.well-known/llms.txt", s.handleLLMS)

	// API v1 routes
	r.Route(api.APIBasePath(), func(r chi.Router) {
		r.Get("/server/healthz", s.handleHealthz)
		r.Get("/server/about", s.handleInfoAPI)
		r.Get("/server/help", s.handleInfoAPI)
		r.Get("/server/privacy", s.handleInfoAPI)
		r.Get("/server/terms", s.handleInfoAPI)
		r.Get("/server/security", s.handleInfoAPI)
		r.Get("/server/contact", s.handleInfoAPI)
		r.Post("/server/graphql", s.graphqlEndpoint())
		r.Get("/server/swagger", s.handleOpenAPISpec)
		r.Post("/server/reports/{report}", s.handleSecurityReport)
		r.Get("/countries", s.handleGetCountries)
		r.Get("/countries/search", s.handleSearchCountries)
		r.Get("/countries/{code}", s.handleGetCountryByCode)
		r.Get("/coordinates", s.handleFindNearestCountry)
		r.Post("/coordinates", s.handleFindNearestCountryPost)
		r.Get("/nearby", s.handleFindNearby)
		r.Get("/stats", s.handleStats)
		r.Get("/stats.txt", s.handleStatsTxt)
		r.Get("/count", s.handleCount)
		r.Get("/count.txt", s.handleCountTxt)
	})

	// Stable discovery aliases serve the same handlers directly.
	r.Get("/api/healthz", s.handleHealthz)
	r.Get("/api/swagger", s.handleOpenAPISpec)
	r.Post("/api/graphql", s.graphqlEndpoint())

}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.config.WebSecurity.CORS)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, X-Request-ID, X-Correlation-ID, X-Trace-ID")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.router,
	}

	errChan := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}

// Health check handler
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, map[string]interface{}{
		"ok": true,
		"data": map[string]interface{}{
			"status":        "healthy",
			"service":       "countries",
			"version":       s.version,
			"country_count": s.countriesService.Count(),
		},
	})
}

// Web UI handlers
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		http.Error(w, "Templates not loaded", http.StatusInternalServerError)
		return
	}

	data := s.pageContext(w, r, "/")
	data["TotalCountries"] = s.countriesService.Count()
	data["Version"] = s.version
	data["BaseURL"] = s.reportOrigin(r)

	if err := s.templates.ExecuteTemplate(w, "home.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (s *Server) handleSearchPage(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		http.Error(w, "Templates not loaded", http.StatusInternalServerError)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	data := s.pageContext(w, r, "/search")
	data["Query"], data["HasSearch"] = query, query != ""
	if query != "" {
		filters, err := searchFiltersFromRequest(r)
		if err != nil {
			s.errorResponse(w, err.Error(), http.StatusBadRequest)
			return
		}
		data["Results"] = s.countriesService.SearchFiltered(filters)
	}

	if err := s.templates.ExecuteTemplate(w, "search.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (s *Server) handleCoordinatesPage(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		http.Error(w, "Templates not loaded", http.StatusInternalServerError)
		return
	}

	data := s.pageContext(w, r, "/coordinates")
	latText, lonText := strings.TrimSpace(r.URL.Query().Get("lat")), strings.TrimSpace(r.URL.Query().Get("lon"))
	if latText != "" || lonText != "" {
		lat, latErr := strconv.ParseFloat(latText, 64)
		lon, lonErr := strconv.ParseFloat(lonText, 64)
		if latErr != nil || lonErr != nil || !validCoordinates(lat, lon) {
			data["Error"] = "Enter a latitude from -90 to 90 and a longitude from -180 to 180."
			w.WriteHeader(http.StatusBadRequest)
		} else {
			data["Latitude"], data["Longitude"] = latText, lonText
			data["Nearest"] = s.countriesService.FindNearest(lat, lon)
		}
	}

	if err := s.templates.ExecuteTemplate(w, "coordinates.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (s *Server) handleOpenAPIPage(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		http.Error(w, "Templates not loaded", http.StatusInternalServerError)
		return
	}

	data := s.pageContext(w, r, "/server/docs/swagger")

	if err := s.templates.ExecuteTemplate(w, "openapi.html", data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// PWA handlers
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	manifest := map[string]interface{}{
		"name":             "Countries API",
		"short_name":       "Countries",
		"description":      "World countries database with geolocation",
		"start_url":        "/",
		"display":          "standalone",
		"background_color": "#1a1a2e",
		"theme_color":      "#0f3460",
		"icons": []map[string]interface{}{
			{"src": "/static/icons/icon.svg", "sizes": "any", "type": "image/svg+xml", "purpose": "any maskable"},
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	json.NewEncoder(w).Encode(manifest)
}

func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	sw := `const CACHE_NAME = 'countries-v1';
const urlsToCache = ['/', '/static/css/main.css', '/static/js/main.js'];

self.addEventListener('install', event => {
  event.waitUntil(caches.open(CACHE_NAME).then(cache => cache.addAll(urlsToCache)));
});

self.addEventListener('fetch', event => {
  event.respondWith(
    caches.match(event.request).then(response => response || fetch(event.request))
  );
});`
	w.Header().Set("Content-Type", "application/javascript")
	w.Write([]byte(sw))
}

func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	var builder strings.Builder
	builder.WriteString("User-agent: *\n")
	for _, path := range s.config.WebRobots.Allow {
		builder.WriteString("Allow: " + path + "\n")
	}
	for _, path := range s.config.WebRobots.Deny {
		builder.WriteString("Disallow: " + path + "\n")
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(builder.String()))
}

func (s *Server) handleSecurity(w http.ResponseWriter, r *http.Request) {
	security := "Contact: https://github.com/apimgr/countries/security/advisories/new\n" +
		"Policy: " + s.reportOrigin(r) + "/server/security\n" +
		"Expires: " + time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339) + "\n"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(security))
}

func (s *Server) handleLLMS(w http.ResponseWriter, r *http.Request) {
	origin := s.reportOrigin(r)
	content := "# Countries\n" +
		"> Public, read-only country reference data and geographic lookups.\n\n" +
		"## API\n" +
		"Base URL: " + origin + api.APIBasePath() + "\n" +
		"Authentication: None\n" +
		"Rate limit: See server response headers and deployment policy.\n\n" +
		"## Endpoints\n" +
		"- GET /countries — list and filter country records\n" +
		"- GET /countries/{code} — read a country by ISO code\n" +
		"- GET /countries/search — search country fields\n" +
		"- GET /coordinates — find the nearest country\n" +
		"- POST /coordinates — find the nearest country from JSON coordinates\n" +
		"- GET /nearby — find nearby country centroids\n" +
		"- GET /stats — read dataset statistics\n" +
		"\nSee " + origin + "/server/docs/swagger for the complete API reference.\n"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(content))
}

// API handlers
func (s *Server) handleGetCountries(w http.ResponseWriter, r *http.Request) {
	filters, err := searchFiltersFromRequest(r)
	if err != nil {
		s.errorResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	allCountries := s.countriesService.GetAll()
	if filters != (countries.SearchFilters{}) {
		allCountries = s.countriesService.SearchFiltered(filters)
	}
	page, limit, err := paginationFromRequest(r)
	if err != nil {
		s.errorResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	start := (page - 1) * limit
	end := start + limit
	if start >= len(allCountries) {
		start = len(allCountries)
	}
	if end > len(allCountries) {
		end = len(allCountries)
	}
	items := allCountries[start:end]
	s.jsonResponse(w, map[string]interface{}{
		"ok":   true,
		"data": items,
		"pagination": map[string]int{
			"page": page, "limit": limit, "total": len(allCountries),
			"pages": (len(allCountries) + limit - 1) / limit,
		},
	})
}

func (s *Server) handleSearchCountries(w http.ResponseWriter, r *http.Request) {
	filters, err := searchFiltersFromRequest(r)
	if err != nil {
		s.errorResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	if filters == (countries.SearchFilters{}) {
		s.errorResponse(w, "At least one search filter is required", http.StatusBadRequest)
		return
	}
	results := s.countriesService.SearchFiltered(filters)
	page, limit, err := paginationFromRequest(r)
	if err != nil {
		s.errorResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	total := len(results)
	start := (page - 1) * limit
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	s.jsonResponse(w, map[string]interface{}{
		"ok":   true,
		"data": results[start:end],
		"pagination": map[string]int{
			"page": page, "limit": limit, "total": total,
			"pages": (total + limit - 1) / limit,
		},
	})
}

func searchFiltersFromRequest(r *http.Request) (countries.SearchFilters, error) {
	values := r.URL.Query()
	read := func(key string) (string, error) {
		value := strings.TrimSpace(values.Get(key))
		if len(value) > 256 {
			return "", fmt.Errorf("query parameter %q exceeds 256 characters", key)
		}
		return value, nil
	}
	var filters countries.SearchFilters
	var err error
	if filters.Query, err = read("q"); err != nil {
		return filters, err
	}
	if filters.Code, err = read("code"); err != nil {
		return filters, err
	}
	if filters.Region, err = read("region"); err != nil {
		return filters, err
	}
	if filters.Currency, err = read("currency"); err != nil {
		return filters, err
	}
	if filters.Language, err = read("language"); err != nil {
		return filters, err
	}
	return filters, nil
}

func paginationFromRequest(r *http.Request) (int, int, error) {
	page := 1
	limit := 250
	if value := strings.TrimSpace(r.URL.Query().Get("page")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 1000000 {
			return 0, 0, fmt.Errorf("page must be between 1 and 1000000")
		}
		page = parsed
	}
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 1000 {
			return 0, 0, fmt.Errorf("limit must be between 1 and 1000")
		}
		limit = parsed
	}
	return page, limit, nil
}

func (s *Server) handleGetCountryByCode(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if code == "" {
		s.errorResponse(w, "Country code is required", http.StatusBadRequest)
		return
	}

	country := s.countriesService.GetByCode(code)
	if country == nil {
		s.errorResponse(w, "Country not found", http.StatusNotFound)
		return
	}

	s.jsonResponse(w, map[string]interface{}{
		"ok":   true,
		"data": country,
	})
}

func (s *Server) handleFindNearestCountry(w http.ResponseWriter, r *http.Request) {
	latStr := r.URL.Query().Get("lat")
	lonStr := r.URL.Query().Get("lon")

	if latStr == "" || lonStr == "" {
		s.errorResponse(w, "Latitude and longitude are required", http.StatusBadRequest)
		return
	}

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		s.errorResponse(w, "Invalid latitude", http.StatusBadRequest)
		return
	}

	lon, err := strconv.ParseFloat(lonStr, 64)
	if err != nil {
		s.errorResponse(w, "Invalid longitude", http.StatusBadRequest)
		return
	}
	if !validCoordinates(lat, lon) {
		s.errorResponse(w, "Latitude or longitude is outside its valid range", http.StatusBadRequest)
		return
	}

	nearest := s.countriesService.FindNearest(lat, lon)
	if nearest == nil {
		s.errorResponse(w, "No countries found", http.StatusNotFound)
		return
	}

	s.jsonResponse(w, map[string]interface{}{
		"ok":   true,
		"data": nearest,
	})
}

func (s *Server) handleFindNearestCountryPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Lat *float64 `json:"lat"`
		Lon *float64 `json:"lon"`
	}

	defer r.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		s.errorResponse(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Lat == nil || req.Lon == nil {
		s.errorResponse(w, "Latitude and longitude are required", http.StatusBadRequest)
		return
	}
	if !validCoordinates(*req.Lat, *req.Lon) {
		s.errorResponse(w, "Latitude or longitude is outside its valid range", http.StatusBadRequest)
		return
	}
	nearest := s.countriesService.FindNearest(*req.Lat, *req.Lon)
	if nearest == nil {
		s.errorResponse(w, "No countries found", http.StatusNotFound)
		return
	}

	s.jsonResponse(w, map[string]interface{}{
		"ok":   true,
		"data": nearest,
	})
}

func (s *Server) handleFindNearby(w http.ResponseWriter, r *http.Request) {
	latStr := r.URL.Query().Get("lat")
	lonStr := r.URL.Query().Get("lon")
	radiusStr := r.URL.Query().Get("radius")

	if latStr == "" || lonStr == "" {
		s.errorResponse(w, "Latitude and longitude are required", http.StatusBadRequest)
		return
	}

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		s.errorResponse(w, "Invalid latitude", http.StatusBadRequest)
		return
	}

	lon, err := strconv.ParseFloat(lonStr, 64)
	if err != nil {
		s.errorResponse(w, "Invalid longitude", http.StatusBadRequest)
		return
	}
	if !validCoordinates(lat, lon) {
		s.errorResponse(w, "Latitude or longitude is outside its valid range", http.StatusBadRequest)
		return
	}

	radius := 500.0 // Default 500km
	if radiusStr != "" {
		radius, err = strconv.ParseFloat(radiusStr, 64)
		if err != nil {
			s.errorResponse(w, "Invalid radius", http.StatusBadRequest)
			return
		}
	}

	if math.IsNaN(radius) || math.IsInf(radius, 0) || radius < 0 || radius > 20038 {
		s.errorResponse(w, "Radius must be between 0 and 20038 kilometers", http.StatusBadRequest)
		return
	}
	results := s.countriesService.FindNearby(lat, lon, radius)
	s.jsonResponse(w, map[string]interface{}{
		"ok":    true,
		"data":  results,
		"count": len(results),
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, map[string]interface{}{
		"ok": true,
		"data": map[string]interface{}{
			"total_countries": s.countriesService.Count(),
			"version":         s.version,
			"data_source":     "embedded country dataset",
		},
	})
}

func (s *Server) handleStatsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Total Countries: " + strconv.Itoa(s.countriesService.Count()) + "\nVersion: " + s.version + "\nData Source: embedded country dataset\n"))
}

func (s *Server) handleCount(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, map[string]interface{}{
		"ok":   true,
		"data": map[string]interface{}{"count": s.countriesService.Count()},
	})
}

func (s *Server) handleCountTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(strconv.Itoa(s.countriesService.Count())))
}

func validCoordinates(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsInf(lat, 0) && lat >= -90 && lat <= 90 &&
		!math.IsNaN(lon) && !math.IsInf(lon, 0) && lon >= -180 && lon <= 180
}

func (s *Server) jsonResponse(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) errorResponse(w http.ResponseWriter, message string, status int) {
	code := "BAD_REQUEST"
	if status == http.StatusNotFound {
		code = "NOT_FOUND"
	}
	writeJSON(w, status, map[string]interface{}{
		"ok":      false,
		"error":   code,
		"message": message,
	})
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
