package server

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/countries/src/admin"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
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
	adminHandler     *admin.Handler
	version          string
	buildDate        string
	commit           string
}

func New(countriesService *countries.Service, cfg *config.Config, address, port, version, buildDate, commit string) *Server {
	s := &Server{
		router:           chi.NewRouter(),
		countriesService: countriesService,
		config:           cfg,
		version:          version,
		buildDate:        buildDate,
		commit:           commit,
	}

	// Parse templates
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		log.Printf("Warning: could not parse templates: %v", err)
	}
	s.templates = tmpl

	// Create admin handler
	sessionTimeout := cfg.Server.Session.Timeout
	if sessionTimeout == 0 {
		sessionTimeout = 3600
	}
	s.adminHandler = admin.NewHandler(
		cfg.Server.Admin.Username,
		cfg.Server.Admin.Password,
		cfg.Server.Admin.APIToken,
		sessionTimeout,
		false, // SSL enabled - will be updated when SSL is configured
		version,
		commit,
		buildDate,
	)

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	r := s.router

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(s.corsMiddleware)

	// Admin routes (session auth for web, bearer token for API)
	s.adminHandler.RegisterRoutes(r)

	// Health check endpoints
	r.Get("/healthz", s.handleHealthz)
	r.Get("/health", s.handleHealthz)
	r.Get("/status", s.handleHealthz)

	// Static files
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Web UI
	r.Get("/", s.handleHome)
	r.Get("/search", s.handleSearchPage)
	r.Get("/coordinates", s.handleCoordinatesPage)
	r.Get("/openapi", s.handleOpenAPIPage)

	// PWA support
	r.Get("/manifest.json", s.handleManifest)
	r.Get("/sw.js", s.handleServiceWorker)
	r.Get("/robots.txt", s.handleRobots)
	r.Get("/security.txt", s.handleSecurity)
	r.Get("/.well-known/security.txt", s.handleSecurity)

	// API v1 routes
	r.Route("/api/v1", func(r chi.Router) {
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

	// Raw data endpoint
	r.Get("/api/data", s.handleRawData)

	// Shorthand routes
	r.Get("/random", s.handleRandomCountry)
	r.Get("/random.txt", s.handleRandomCountryTxt)
	r.Get("/countries", s.handleGetCountries)
	r.Get("/countries/search/{query}", s.handleSearchCountriesPath)
	r.Get("/countries/{code}", s.handleGetCountryByCode)
	r.Get("/coordinates", s.handleFindNearestCountry)
	r.Post("/coordinates", s.handleFindNearestCountryPost)
	r.Get("/data", s.handleRawData)
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.config.WebSecurity.CORS)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

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
		"status":  "ok",
		"service": "countries",
		"version": s.version,
	})
}

// Web UI handlers
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		http.Error(w, "Templates not loaded", http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"TotalCountries": s.countriesService.Count(),
		"Version":        s.version,
		"Theme":          s.config.WebUI.Theme,
	}

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

	data := map[string]interface{}{
		"Theme": s.config.WebUI.Theme,
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

	data := map[string]interface{}{
		"Theme": s.config.WebUI.Theme,
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

	data := map[string]interface{}{
		"Theme": s.config.WebUI.Theme,
	}

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
			{"src": "/static/icons/icon-192.png", "sizes": "192x192", "type": "image/png"},
			{"src": "/static/icons/icon-512.png", "sizes": "512x512", "type": "image/png"},
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
	security := "Contact: mailto:" + s.config.WebSecurity.Admin + "\nPreferred-Languages: en\n"
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(security))
}

// API handlers
func (s *Server) handleGetCountries(w http.ResponseWriter, r *http.Request) {
	countries := s.countriesService.GetAll()
	s.jsonResponse(w, map[string]interface{}{
		"success": true,
		"data":    countries,
		"count":   len(countries),
	})
}

func (s *Server) handleSearchCountries(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		s.errorResponse(w, "Query parameter 'q' is required", http.StatusBadRequest)
		return
	}

	results := s.countriesService.Search(query)
	s.jsonResponse(w, map[string]interface{}{
		"success": true,
		"data":    results,
		"count":   len(results),
		"query":   query,
	})
}

func (s *Server) handleSearchCountriesPath(w http.ResponseWriter, r *http.Request) {
	query := chi.URLParam(r, "query")
	if query == "" {
		s.errorResponse(w, "Search query is required", http.StatusBadRequest)
		return
	}

	results := s.countriesService.Search(query)
	s.jsonResponse(w, map[string]interface{}{
		"success": true,
		"data":    results,
		"count":   len(results),
		"query":   query,
	})
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
		"success": true,
		"data":    country,
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

	nearest := s.countriesService.FindNearest(lat, lon)
	if nearest == nil {
		s.errorResponse(w, "No countries found", http.StatusNotFound)
		return
	}

	s.jsonResponse(w, map[string]interface{}{
		"success": true,
		"data":    nearest,
	})
}

func (s *Server) handleFindNearestCountryPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.errorResponse(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	nearest := s.countriesService.FindNearest(req.Lat, req.Lon)
	if nearest == nil {
		s.errorResponse(w, "No countries found", http.StatusNotFound)
		return
	}

	s.jsonResponse(w, map[string]interface{}{
		"success": true,
		"data":    nearest,
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

	radius := 500.0 // Default 500km
	if radiusStr != "" {
		radius, err = strconv.ParseFloat(radiusStr, 64)
		if err != nil {
			s.errorResponse(w, "Invalid radius", http.StatusBadRequest)
			return
		}
	}

	results := s.countriesService.FindNearby(lat, lon, radius)
	s.jsonResponse(w, map[string]interface{}{
		"success": true,
		"data":    results,
		"count":   len(results),
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"total_countries": s.countriesService.Count(),
			"version":         s.version,
			"data_source":     "REST Countries",
		},
	})
}

func (s *Server) handleStatsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Total Countries: " + strconv.Itoa(s.countriesService.Count()) + "\nVersion: " + s.version + "\nData Source: REST Countries\n"))
}

func (s *Server) handleCount(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, map[string]interface{}{
		"success": true,
		"count":   s.countriesService.Count(),
	})
}

func (s *Server) handleCountTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(strconv.Itoa(s.countriesService.Count())))
}

func (s *Server) handleRawData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(s.countriesService.GetRaw())
}

func (s *Server) handleRandomCountry(w http.ResponseWriter, r *http.Request) {
	allCountries := s.countriesService.GetAll()
	if len(allCountries) == 0 {
		s.errorResponse(w, "No countries available", http.StatusNotFound)
		return
	}

	idx := rand.Intn(len(allCountries))
	s.jsonResponse(w, map[string]interface{}{
		"success": true,
		"data":    allCountries[idx],
	})
}

func (s *Server) handleRandomCountryTxt(w http.ResponseWriter, r *http.Request) {
	allCountries := s.countriesService.GetAll()
	if len(allCountries) == 0 {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("No countries available"))
		return
	}

	idx := rand.Intn(len(allCountries))
	country := allCountries[idx]
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(country.Name + " (" + country.CountryCode + ") - Capital: " + country.Capital))
}

func (s *Server) jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *Server) errorResponse(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   message,
	})
}
