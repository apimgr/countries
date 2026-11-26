// Package server exposes country data through HTTP APIs and web pages.
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"expvar"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"net/http/pprof"
	"net/mail"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/common/network"
	commontheme "github.com/apimgr/countries/src/common/theme"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
	"github.com/apimgr/countries/src/database"
	graphqlapi "github.com/apimgr/countries/src/graphql"
	appmode "github.com/apimgr/countries/src/mode"
	"github.com/apimgr/countries/src/notifications"
	"github.com/apimgr/countries/src/paths"
	"github.com/redis/go-redis/v9"
	backgroundscheduler "github.com/apimgr/countries/src/scheduler"
	"github.com/apimgr/countries/src/securitydata"
	servermetrics "github.com/apimgr/countries/src/server/metrics"
	"github.com/apimgr/countries/src/ssl"
	"github.com/apimgr/countries/src/swagger"
	"github.com/go-chi/chi/v5"
	"github.com/shirou/gopsutil/v3/disk"
	xhtml "golang.org/x/net/html"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

//go:embed template
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

func EmailTemplateFS() fs.FS {
	templates, _ := fs.Sub(templatesFS, "template/email")
	return templates
}

type Server struct {
	router                 *chi.Mux
	apiHandlers            *chi.Mux
	db                     *sql.DB
	contactKey             []byte
	contactPreviousKey     []byte
	contactPreviousUntil   time.Time
	countriesService       *countries.Service
	config                 *config.Config
	configMu               sync.RWMutex
	dataDir                string
	templates              map[string]*template.Template
	version                string
	buildDate              string
	commit                 string
	startedAt              time.Time
	healthStats            *healthCounters
	healthStore            *database.HealthStore
	healthDataDir          string
	diskHealthStatus       func() string
	scheduler              *backgroundscheduler.Scheduler
	schedulerRunning       atomic.Bool
	shuttingDown           atomic.Bool
	maintenance            atomic.Bool
	cacheClient            redis.UniversalClient
	cacheDegraded          atomic.Bool
	cacheWarningOnce       sync.Once
	maintenanceMu          sync.RWMutex
	maintenanceReason      string
	maintenanceSince       time.Time
	maintenanceLastAttempt time.Time
	maintenanceAttempts    int
	maintenanceExhausted   bool
	pendingRestart         atomic.Bool
	healthStateMu          sync.RWMutex
	restartReasons         []string
	trustedProxies         []*net.IPNet
	learnedCORSOriginsMu    sync.RWMutex
	learnedCORSOrigins      map[string]struct{}
	allowlist              []*net.IPNet
	graphqlHandler         http.Handler
	rateLimiter            *rateLimiter
	geoIPManager           countryLookup
	securityData           *securitydata.Manager
	notificationManager    *notifications.Manager
	metrics                *servermetrics.Metrics
	overlayMu              sync.RWMutex
	torHealth              HealthOverlayInfo
	i2pHealth              HealthI2PInfo
	torHTTPClient          *http.Client
}

func New(countriesService *countries.Service, cfg *config.Config, address, port, version, buildDate, commit string, databases ...*sql.DB) *Server {
	var db *sql.DB
	if len(databases) > 0 {
		db = databases[0]
	}
	previousContactKey, previousContactUntil := loadPreviousContactKey(db)
	s := &Server{
		router:               chi.NewRouter(),
		db:                   db,
		contactKey:           loadContactKey(db),
		contactPreviousKey:   previousContactKey,
		contactPreviousUntil: previousContactUntil,
		countriesService:     countriesService,
		config:               cfg,
		dataDir:              paths.GetDataDir("countries"),
		version:              version,
		buildDate:            buildDate,
		commit:               commit,
		startedAt:            time.Now().UTC(),
		healthStats:          newHealthCounters(),
		trustedProxies:       buildTrustedProxies(address, cfg.Server.TrustedProxies.Additional),
		learnedCORSOrigins:   make(map[string]struct{}),
		rateLimiter:          newRateLimiter(cfg.Server.RateLimit, db),
	}
	if db != nil {
		healthStore, err := database.NewHealthStore(context.Background(), db)
		if err != nil {
			log.Printf("Warning: could not initialize health statistics: %v", err)
		} else {
			s.healthStore = healthStore
			_ = healthStore.RegisterCheck("cache", false, func(context.Context) string {
				return s.cacheHealthStatus()
			})
			_ = healthStore.RegisterCheck("disk", true, func(context.Context) string {
				if s.diskHealthStatus != nil {
					return s.diskHealthStatus()
				}
				if s.healthDataDir == "" {
					return "error"
				}
				if _, err := disk.Usage(s.healthDataDir); err != nil {
					return "error"
				}
				return "ok"
			})
			_ = healthStore.RegisterCheck("scheduler", false, func(context.Context) string {
				if !s.schedulerRunning.Load() {
					return "error"
				}
				return "ok"
			})
			if countriesService != nil {
				_ = healthStore.RegisterCheck("data_countries", true, func(context.Context) string {
					if countriesService.Count() == 0 {
						return "error"
					}
					return "ok"
				})
			}
			if cfg.Server.GeoIP.Enabled {
				assets := []struct{ name, check string }{{"asn", "geoip_asn"}, {"country", "geoip_country"}, {"city_ipv4", "geoip_city_ipv4"}, {"city_ipv6", "geoip_city_ipv6"}}
				for _, asset := range assets {
					name := asset.name
					_ = healthStore.RegisterCheck(asset.check, false, func(context.Context) string {
						manager, ok := s.geoIPManager.(interface { HasDatabase(string) bool; IsStale(time.Time, time.Duration) bool })
						if !ok || !manager.HasDatabase(name) { return "error" }
						if manager.IsStale(time.Now().UTC(), 21*24*time.Hour) { return "degraded" }
						return "ok"
					})
				}
			}
			s.healthStore.RefreshChecks(context.Background())
		}
	}
	s.rateLimiter.clientKey = s.rateLimitClientKey
	s.rateLimiter.authRequired = s.rateLimitAuthRequired
	s.rateLimiter.authValid = s.rateLimitAuthValid
	s.allowlist = s.buildAllowlist()
	database.SetMetricsObserver(nil)
	if cfg.Server.Metrics.Enabled {
		s.metrics = servermetrics.New(servermetrics.Config{
			ProjectName: "countries", Version: version, Commit: commit, BuildDate: buildDate,
			IncludeSystem: cfg.Server.Metrics.IncludeSystem, IncludeRuntime: cfg.Server.Metrics.IncludeRuntime,
			HTTPBuckets: cfg.Server.Metrics.DurationBuckets, SizeBuckets: cfg.Server.Metrics.SizeBuckets,
			DBBuckets:        []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
			SchedulerBuckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 300, 600},
		})
		s.metrics.SetCountryRecords(countriesService.Count())
		s.rateLimiter.metrics = s.metrics
		database.SetMetricsObserver(s.metrics)
	}

	// Parse each page with a fresh layout tree so its content block cannot
	// replace another page's content block.
	s.templates = parsePageTemplates(template.FuncMap{
		"t": i18n.Translate,
		"tf": func(lang, key string, values ...interface{}) string {
			args := make(map[string]string, len(values)/2)
			for index := 0; index+1 < len(values); index += 2 {
				args[fmt.Sprint(values[index])] = fmt.Sprint(values[index+1])
			}
			return i18n.TranslateFormat(lang, key, args)
		},
		"tp":               i18n.TranslatePlural,
		"nextTheme":        nextTheme,
		"themeName":        func(lang, theme string) string { return i18n.Translate(lang, "theme."+theme) },
		"languageName":     i18n.NativeName,
		"markdownToHTML":   markdownToHTML,
		"trackingScript":   s.trackingScript,
		"trackingNoscript": s.trackingNoscript,
		"consentRequired": func(r *http.Request) bool {
			return r != nil && r.URL.Query().Get("consent_required") == "1"
		},
		"operatorHTML":     func(r *http.Request, name string) template.HTML { return s.operatorHTML(r, name) },
		"healthCheckClass": healthCheckClass,
		"path": func(prefix, path string) string {
			return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(path, "/")
		},
		"asset": func(prefix, path string) string {
			return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(path, "/") + "?v=" + s.assetStamp()
		},
		"pageHubPath": func(name string) string {
			for _, page := range registeredHubPages(s.config) {
				if strings.TrimPrefix(page.Hub, "/server/") == name {
					return page.Hub
				}
			}
			return ""
		},
	})
	graphqlHandler, err := graphqlapi.NewHandler(countriesService, version)
	if err != nil {
		log.Printf("Warning: could not initialize GraphQL schema: %v", err)
	} else {
		s.graphqlHandler = graphqlHandler
	}

	s.setupRoutes()
	if graphqlHandler != nil {
		graphqlHandler.SetAPIReader(func(ctx context.Context, request *http.Request, path string) (json.RawMessage, error) {
			if s.apiHandlers == nil {
				return nil, errors.New("API router is unavailable")
			}
			parsedURL, err := url.ParseRequestURI(path)
			if err != nil || (parsedURL.Path != "/api/autodiscover" && !strings.HasPrefix(parsedURL.Path, api.APIBasePath()+"/")) {
				return nil, errors.New("API read path is invalid")
			}
			readRequest := request.Clone(ctx)
			urlCopy := *request.URL
			urlCopy.Path = parsedURL.Path
			urlCopy.RawPath = parsedURL.RawPath
			urlCopy.RawQuery = parsedURL.RawQuery
			readRequest.URL = &urlCopy
			readRequest.Method = http.MethodGet
			readRequest.RequestURI = urlCopy.RequestURI()
			readRequest.Header = request.Header.Clone()
			readRequest.Header.Set("Accept", "application/json")
			response := httptest.NewRecorder()
			if parsedURL.Path == "/api/autodiscover" {
				s.handleAutodiscover(response, readRequest)
			} else {
				s.apiHandlers.ServeHTTP(response, readRequest)
			}
			if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
				return nil, fmt.Errorf("API read returned HTTP %d", response.Code)
			}
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				return nil, fmt.Errorf("decode API read response: %w", err)
			}
			if len(envelope.Data) == 0 {
				return json.RawMessage(response.Body.Bytes()), nil
			}
			if string(envelope.Data) == "null" {
				return nil, errors.New("API read response has no data")
			}
			return envelope.Data, nil
		})
	}
	return s
}

// OverlayHandler returns the application router for isolated loopback overlay listeners.
func (s *Server) OverlayHandler() http.Handler { return s.router }

// TorOverlayHandler adds the Tor-specific aggregate request metric.
func (s *Server) TorOverlayHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := torCircuitIdentity(r.RemoteAddr)
		if identity == "" {
			identity = "tor"
		}
		r.RemoteAddr = identity
		if s.metrics != nil {
			s.metrics.RecordTorRequest()
		}
		s.router.ServeHTTP(w, r)
	})
}

// SetTorBandwidthUsage updates the aggregate Tor accounting metric when metrics are enabled.
func (s *Server) SetTorBandwidthUsage(bytes uint64) {
	if s.metrics != nil {
		s.metrics.SetTorBandwidthUsage(bytes)
	}
}

// SetTorHTTPClient publishes the outbound Tor client after its SOCKS listener is ready.
func (s *Server) SetTorHTTPClient(client *http.Client) {
	s.overlayMu.Lock()
	s.torHTTPClient = client
	s.overlayMu.Unlock()
}

func (s *Server) SetSecurityDataManager(manager *securitydata.Manager) {
	s.securityData = manager
	if manager == nil || s.healthStore == nil {
		return
	}
	for name := range manager.Health() {
		checkName := name
		if err := s.healthStore.RegisterCheck(checkName, false, func(context.Context) string {
			health, exists := manager.Health()[checkName]
			if !exists {
				return "error"
			}
			if health.Status == "healthy" {
				return "ok"
			}
			return health.Status
		}); err != nil {
			log.Printf("Could not register security data health check %s: %v", checkName, err)
		}
	}
	s.healthStore.RefreshChecks(context.Background())
}

func (s *Server) torOutboundHTTPClient() *http.Client {
	s.overlayMu.RLock()
	defer s.overlayMu.RUnlock()
	return s.torHTTPClient
}

func torRequest(r *http.Request) bool {
	return r.RemoteAddr == "tor" || strings.HasPrefix(r.RemoteAddr, "tor:")
}

// SetOverlayStatus publishes sanitized Tor and I2P lifecycle state to health output.
func (s *Server) SetOverlayStatus(tor HealthOverlayInfo, i2p HealthI2PInfo) {
	if !tor.Running {
		tor.Hostname = ""
	}
	if !i2p.Enabled {
		i2p.Running = false
		i2p.Status = "disabled"
		i2p.Hostname = ""
		if i2p.State == "" {
			i2p.State = "disabled"
		}
		if i2p.Provider == "" {
			i2p.Provider = "none"
		}
	}
	if !i2p.Running {
		i2p.Hostname = ""
	}
	s.overlayMu.Lock()
	s.torHealth, s.i2pHealth = tor, i2p
	s.overlayMu.Unlock()
	if s.metrics != nil {
		s.metrics.SetTorState(tor.Enabled, tor.Running, tor.Running)
	}
}

func parsePageTemplates(functions template.FuncMap) map[string]*template.Template {
	common := []string{
		"template/layout/public.tmpl",
		"template/partial/head.tmpl",
		"template/partial/scripts.tmpl",
		"template/partial/consent.tmpl",
		"template/partial/public/header.tmpl",
		"template/partial/public/nav.tmpl",
		"template/partial/public/footer.tmpl",
	}
	pages := map[string]string{
		"contact.html":                "contact.tmpl",
		"coordinates.html":            "coordinates.tmpl",
		"error.html":                  "error.tmpl",
		"graphql.html":                "graphql.tmpl",
		"home.html":                   "index.tmpl",
		"info.html":                   "info.tmpl",
		"openapi.html":                "openapi.tmpl",
		"preferences-page.html":       "preferences.tmpl",
		"preferences-sync.html":       "preferences-sync.tmpl",
		"search.html":                 "search.tmpl",
		"security-report-status.html": "security-report-status.tmpl",
		"tokens-page.html":            "tokens.tmpl",
	}
	templates := make(map[string]*template.Template, len(pages))
	for name, page := range pages {
		files := append(append([]string(nil), common...), "template/page/"+page)
		parsed, err := template.New(name).Funcs(functions).ParseFS(templatesFS, files...)
		if err != nil {
			log.Printf("Warning: could not parse %s template: %v", name, err)
			continue
		}
		templates[name] = parsed
	}
	return templates
}

func (s *Server) executeTemplate(w io.Writer, name string, data interface{}) error {
	parsed := s.templates[name]
	if parsed == nil {
		return fmt.Errorf("template %q is not loaded", name)
	}
	var rendered bytes.Buffer
	if err := parsed.ExecuteTemplate(&rendered, "page", data); err != nil {
		return err
	}
	_, err := w.Write(rendered.Bytes())
	return err
}

func (s *Server) renderTemplateResponse(w http.ResponseWriter, r *http.Request, name string, data interface{}, status int) {
	var rendered bytes.Buffer
	if err := s.executeTemplate(&rendered, name, data); err != nil {
		log.Printf("Template %q failed: %v", name, err)
		renderFallbackError(w, r, http.StatusInternalServerError, s)
		return
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/plain") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write(renderHTMLText(rendered.Bytes(), 80))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.setHTMLResponseHeaders(w, r, status)
	if status >= 200 && status < 300 && r.Method == http.MethodGet && len(r.Cookies()) == 0 &&
		!strings.Contains(strings.ToLower(rendered.String()), "<form") {
		w.Header().Set("X-Offline-Cache", "public")
	}
	w.WriteHeader(status)
	if _, err := w.Write(rendered.Bytes()); err != nil {
		log.Printf("Write rendered template %q: %v", name, err)
	}
}

func renderHTMLText(document []byte, width int) []byte {
	tokenizer := xhtml.NewTokenizer(bytes.NewReader(document))
	var content strings.Builder
	skipTag := ""
	for {
		tokenType := tokenizer.Next()
		token := tokenizer.Token()
		switch tokenType {
		case xhtml.ErrorToken:
			if tokenizer.Err() == io.EOF {
				goto finished
			}
			goto finished
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			tag := strings.ToLower(token.Data)
			if tag == "script" || tag == "style" {
				skipTag = tag
				continue
			}
			if skipTag == "" && isHTMLBlockTag(tag) {
				content.WriteByte('\n')
			}
		case xhtml.EndTagToken:
			tag := strings.ToLower(token.Data)
			if tag == skipTag {
				skipTag = ""
				continue
			}
			if skipTag == "" && isHTMLBlockTag(tag) {
				content.WriteByte('\n')
			}
		case xhtml.TextToken:
			if skipTag == "" {
				content.WriteString(token.Data)
				content.WriteByte(' ')
			}
		}
	}

finished:
	words := strings.Fields(content.String())
	var output strings.Builder
	column := 0
	for _, word := range words {
		if column > 0 && column+1+len(word) > width {
			output.WriteByte('\n')
			column = 0
		}
		if column > 0 {
			output.WriteByte(' ')
			column++
		}
		output.WriteString(word)
		column += len(word)
	}
	output.WriteByte('\n')
	return []byte(output.String())
}

func isHTMLBlockTag(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "br", "dd", "div", "dl", "dt", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "section", "table", "tbody", "td", "th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}

// RecoverMiddleware turns handler panics into a bounded error response.
func RecoverMiddleware(next http.Handler) http.Handler {
	return recoverMiddleware(next, nil)
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return recoverMiddleware(next, s)
}

func recoverMiddleware(next http.Handler, server *Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("panic recovered: %v\n%s", recovered, debug.Stack())
				renderFallbackError(w, r, http.StatusInternalServerError, server)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func renderFallbackError(w http.ResponseWriter, r *http.Request, status int, server ...*Server) {
	if isAPIRequestPath(r.URL.Path) || strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, "{\n  \"ok\": false,\n  \"error\": %q,\n  \"message\": %q\n}\n", fallbackErrorCode(status), "The request could not be completed.")
		return
	}
	lang, theme, prefix, stamp := "en", "dark", "", ""
	if len(server) > 0 && server[0] != nil {
		app := server[0]
		lang = app.languageForRequest(w, r)
		theme = app.themeForRequest(r)
		prefix = app.requestURLPrefix(r)
		stamp = app.assetStamp()
		app.setHTMLResponseHeaders(w, r, status)
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	title := i18n.Translate(lang, "error.title")
	messageKey := "error.generic"
	if status == http.StatusNotFound {
		messageKey = "error.not_found"
	}
	message := i18n.Translate(lang, messageKey)
	home := i18n.Translate(lang, "error.home")
	dir := i18n.Direction(lang)
	cssLinks := strings.Builder{}
	for _, name := range []string{"common.css", "components.css", "public.css"} {
		cssURL := strings.TrimSuffix(prefix, "/") + "/static/css/" + name
		if stamp != "" {
			cssURL += "?v=" + stamp
		}
		fmt.Fprintf(&cssLinks, `<link rel="stylesheet" href="%s">`, template.HTMLEscapeString(cssURL))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!doctype html><html lang=\"%s\" dir=\"%s\" class=\"theme-%s\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>%d %s</title>%s</head><body><a class=\"skip-link\" href=\"#main-content\">%s</a><header class=\"site-header\"><nav aria-label=\"%s\"><a href=\"%s/\">%s</a></nav></header><main id=\"main-content\" class=\"page-content error-page\"><h1>%s</h1><p>%s</p><a class=\"btn btn-primary\" href=\"%s/\">%s</a></main></body></html>",
		template.HTMLEscapeString(lang), template.HTMLEscapeString(dir), template.HTMLEscapeString(theme), status,
		template.HTMLEscapeString(title), cssLinks.String(), template.HTMLEscapeString(i18n.Translate(lang, "nav.skip_main")),
		template.HTMLEscapeString(i18n.Translate(lang, "nav.main_navigation")), template.HTMLEscapeString(strings.TrimSuffix(prefix, "/")),
		template.HTMLEscapeString(i18n.Translate(lang, "nav.home")), template.HTMLEscapeString(title),
		template.HTMLEscapeString(message), template.HTMLEscapeString(strings.TrimSuffix(prefix, "/")), template.HTMLEscapeString(home))
}

func (s *Server) setupRoutes() {
	r := s.router
	r.Use(URLNormalizeMiddleware)
	r.Use(requestIDMiddleware)
	r.Use(s.PathSecurityMiddleware)
	r.Use(s.securityHeadersMiddleware)
	r.Use(s.serverTimingMiddleware)
	r.Use(s.privacySignalMiddleware)
	r.Use(s.secFetchValidationMiddleware)
	r.Use(s.configReloadMiddleware)
	r.Use(s.recoverMiddleware)
	r.Use(s.maintenanceWriteMiddleware)
	r.Use(s.languagePreferenceConsentMiddleware)
	r.Use(apiContentNegotiation)
	r.Use(s.publicNotificationsMiddleware)
	r.Use(s.healthStatsMiddleware)
	if s.metrics != nil {
		r.Use(s.metrics.Middleware)
	}
	// Middleware
	r.Use(s.trustedProxyMiddleware)
	r.Use(s.debugMiddleware)
	r.Use(requestLogMiddleware)
	r.Use(s.corsMiddleware)
	r.Use(s.allowlistMiddleware)
	r.Use(s.blocklistMiddleware)
	r.Use(s.rateLimiter.middleware)
	r.Use(s.frontendContentNegotiationMiddleware)
	r.Use(s.geoIPMiddleware)
	if s.metrics != nil {
		RegisterMetricsRoutes(r, s.config.Server.Metrics, s.metrics)
	}
	if appmode.DebugEnabled() {
		s.registerDebugRoutes(r, "/debug")
	}
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		s.errorResponse(w, r, "Page not found", http.StatusNotFound)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		s.errorResponse(w, r, "Method not allowed", http.StatusMethodNotAllowed)
	})

	// Canonical server status page.
	r.Get("/server", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/server/about", http.StatusMovedPermanently)
	})
	r.Get("/server/healthz", s.handleHealthPage)
	if s.config.Server.Healthz.Root.Enabled {
		r.Get("/healthz", s.handleHealthPage)
	}
	r.Get("/server/about", s.handleInfoPage)
	r.Get("/server/help", s.handleInfoPage)
	s.registerFrontendPageRoutes(r)
	r.Get("/server/privacy", s.handleInfoPage)
	r.Get("/server/terms", s.handleInfoPage)
	r.Get("/server/security", s.handleInfoPage)
	r.Get("/server/security/policy", s.handleInfoPage)
	r.Get("/server/security/thanks", s.handleInfoPage)
	r.Get("/server/contact", s.handleContactPage)
	r.Post("/server/contact", s.handleContactSubmit)
	r.Post("/server/ccpa", s.handleCCPAOptOut)
	r.Get("/server/security/report/{tracking_id}", s.handleSecurityReportStatus)
	r.Get("/server/dpo", s.handleDPOPage)
	r.Get("/server/preferences", s.handlePreferencesPage)
	r.Get("/server/preferences/sync/import", s.handlePreferencesImport)
	r.Get("/server/docs/graphql", s.handleGraphQLPage)
	r.Post("/server/docs/graphql", s.handleGraphQLPage)
	r.Post("/server/preferences", s.handleSavePreferences)
	r.Post("/server/preferences/token/create", s.handleIssueOwnerToken)
	r.Post("/server/preferences/token/restore", s.handleRestoreOwnerToken)
	r.Post("/server/preferences/token/forget", s.handleForgetOwnerToken)
	r.Post("/server/preferences/token/reveal", s.handleRevealPrimaryToken)
	r.Post("/server/preferences/token/rotate", s.handleRotatePrimaryToken)
	r.Post("/server/preferences/token/revoke", s.handleRevokePrimaryTokenWeb)
	r.Post("/server/preferences/tokens", s.handleCreateLinkedToken)
	r.Post("/server/preferences/tokens/{id}/reveal", s.handleRevealLinkedToken)
	r.Post("/server/preferences/tokens/{id}/rotate", s.handleRotateLinkedToken)
	r.Post("/server/preferences/tokens/{id}/update", s.handleUpdateLinkedTokenWeb)
	r.Post("/server/preferences/tokens/{id}/revoke", s.handleRevokeLinkedTokenWeb)
	r.Post("/server/preferences/sync", s.handlePreferencesSyncSave)
	r.Post("/server/consent", s.handleConsent)
	r.Post("/announcements/dismiss", s.dismissAnnouncement)

	// Static files
	staticFiles, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Printf("Warning: could not expose static assets: %v", err)
	} else {
		fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles)))
		themeAssets := map[string]func() string{
			"css/common.css":           commontheme.CSSVariables,
			"css/swagger-ui-theme.css": swagger.CSSVariables,
			"css/graphiql-theme.css":   graphqlapi.CSSVariables,
		}
		r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.setAssetCacheHeaders(w, r) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			for asset, variables := range themeAssets {
				if !strings.HasSuffix(r.URL.Path, "/static/"+asset) {
					continue
				}
				css, readErr := fs.ReadFile(staticFiles, asset)
				if readErr != nil {
					http.Error(w, "Static asset unavailable", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "text/css; charset=utf-8")
				if r.Method != http.MethodHead {
					_, _ = io.WriteString(w, variables())
					_, _ = w.Write(css)
				}
				return
			}
			fileServer.ServeHTTP(w, r)
		}))
	}

	// Web UI
	r.Get("/", s.handleHome)
	r.Get("/search", s.handleSearchPage)
	r.Get("/coordinates", s.handleCoordinatesPage)
	r.Get("/server/docs/swagger", s.handleOpenAPIPage)

	// PWA support
	r.Get("/manifest.json", s.handleManifest)
	r.Get("/sw.js", s.handleServiceWorker)
	r.Get("/robots.txt", s.handleRobots)
	r.Get("/sitemap.xml", s.handleSitemap)
	r.Handle("/.well-known/security.txt", http.HandlerFunc(s.handleSecurity))
	r.Handle("/.well-known/pgp-key.asc", http.HandlerFunc(s.handlePGPPublicKey))
	r.Get("/llms.txt", s.handleLLMS)
	r.Get("/.well-known/llms.txt", s.handleLLMS)
	r.Get("/server/tracking.js", s.handleTrackingScript)

	// API handlers are mounted on both the public router and a private in-process router.
	s.apiHandlers = chi.NewRouter()
	r.Route(api.APIBasePath(), s.registerAPIRoutes)
	s.apiHandlers.Route(api.APIBasePath(), s.registerAPIRoutes)

	// Stable discovery aliases serve the same handlers directly.
	r.Get("/api/healthz", s.handleHealthAPI)
	r.Get("/api/healthz.txt", s.handleHealthAPI)
	r.Get("/api/swagger", s.handleOpenAPISpec)
	r.Get("/api/graphql", s.graphqlEndpoint())
	r.Post("/api/graphql", s.graphqlEndpoint())
	r.Post("/api/mcp", s.handleMCP)
	r.Post("/mcp", s.handleMCP)
	r.Get("/api/autodiscover", s.handleAutodiscover)
	r.Get("/cli/binaries/{binary}", s.handleCLIBinary)
}

func (s *Server) configReloadMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.configMu.RLock()
		defer s.configMu.RUnlock()
		next.ServeHTTP(w, r)
	})
}

// ApplyReloadedConfig swaps the live configuration after in-flight requests finish.
func (s *Server) ApplyReloadedConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.config = cfg
	s.rateLimiter.config = cfg.Server.RateLimit
	s.allowlist = s.buildAllowlist()
}

// SetCacheClient installs the optional shared Valkey/Redis client. A client
// returned with an initial ping error remains available for later recovery.
func (s *Server) SetCacheClient(client redis.UniversalClient, initialErr ...error) {
	s.cacheClient = client
	if s.rateLimiter != nil {
		s.rateLimiter.cache = client
		s.rateLimiter.cachePrefix = s.config.Server.Cache.Prefix
		s.rateLimiter.cacheFailure = s.markCacheFailure
		s.rateLimiter.cacheRecovery = func() { s.cacheDegraded.Store(false) }
	}
	if len(initialErr) > 0 && initialErr[0] != nil {
		s.markCacheFailure()
	}
}

// CloseCache releases the optional shared cache connection during shutdown.
func (s *Server) CloseCache() error {
	var closeErr error
	if s.rateLimiter != nil {
		closeErr = s.rateLimiter.Close()
	}
	if s.cacheClient != nil {
		closeErr = errors.Join(closeErr, s.cacheClient.Close())
	}
	return closeErr
}

func (s *Server) markCacheFailure() {
	s.cacheDegraded.Store(true)
	s.cacheWarningOnce.Do(func() {
		log.Printf("Warning: external rate-limit cache unavailable; using database-seeded in-memory fallback")
	})
}

func (s *Server) cacheHealthStatus() string {
	if s.cacheClient == nil {
		if s.config != nil && (s.config.Server.Cache.Type == "redis" || s.config.Server.Cache.Type == "valkey") && s.cacheDegraded.Load() {
			return "degraded"
		}
		return "ok"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := s.cacheClient.Ping(ctx).Err()
	cancel()
	if err != nil {
		s.markCacheFailure()
		return "degraded"
	}
	s.cacheDegraded.Store(false)
	return "ok"
}

func (s *Server) registerDebugRoutes(router chi.Router, prefix string) {
	debugRouter := chi.NewRouter()
	debugProtected := debugRouter.With(s.requireDebugOperator)
	if s.config.Server.Debug.Pprof {
		debugProtected.Handle("/pprof", http.HandlerFunc(pprof.Index))
		debugProtected.Handle("/pprof/", http.HandlerFunc(pprof.Index))
		debugProtected.HandleFunc("/pprof/cmdline", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, filepath.Base(os.Args[0]))
		})
		debugProtected.Handle("/pprof/profile", http.HandlerFunc(pprof.Profile))
		debugProtected.Handle("/pprof/symbol", http.HandlerFunc(pprof.Symbol))
		debugProtected.Handle("/pprof/trace", http.HandlerFunc(pprof.Trace))
		for _, profile := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
			debugProtected.Handle("/pprof/"+profile, pprof.Handler(profile))
		}
	}
	if s.config.Server.Debug.RuntimeEndpoints {
		debugProtected.HandleFunc("/vars", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			values := make(map[string]json.RawMessage)
			expvar.Do(func(value expvar.KeyValue) {
				if value.Key == "cmdline" {
					binary, _ := json.Marshal([]string{filepath.Base(os.Args[0])})
					values[value.Key] = binary
					return
				}
				if encoded, err := json.Marshal(value.Value); err == nil {
					values[value.Key] = encoded
				}
			})
			_ = json.NewEncoder(w).Encode(values)
		})
	}
	debugProtected.Get("/config", s.handleDebugConfig)
	debugProtected.Get("/routes", s.handleDebugRoutes)
	debugProtected.Get("/cache", s.handleDebugCache)
	debugProtected.Get("/db", s.handleDebugDB)
	debugProtected.Get("/scheduler", s.handleDebugScheduler)
	if s.config.Server.Debug.RuntimeEndpoints {
		debugProtected.Get("/memory", s.handleDebugMemory)
		debugProtected.Get("/goroutines", s.handleDebugGoroutines)
	}
	debugProtected.Get("/error500", func(w http.ResponseWriter, r *http.Request) {
		s.errorResponse(w, r, "Internal server error", http.StatusInternalServerError)
	})
	router.Mount(prefix, debugRouter)
}

func (s *Server) requireDebugOperator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.validOperatorToken(bearerValue(r)) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeAPIError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "A valid operator token is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func fallbackErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "BAD_REQUEST"
	case http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusMethodNotAllowed:
		return "METHOD_NOT_ALLOWED"
	case http.StatusRequestEntityTooLarge:
		return "PAYLOAD_TOO_LARGE"
	case http.StatusUnsupportedMediaType:
		return "UNSUPPORTED_MEDIA_TYPE"
	case http.StatusTooManyRequests:
		return "RATE_LIMITED"
	default:
		return "INTERNAL_ERROR"
	}
}

func (s *Server) registerAPIRoutes(r chi.Router) {
	r.Get("/server/preferences", s.handlePreferencesAPI)
	r.Get("/server/preferences/sync/import", s.handlePreferencesImport)
	r.Post("/server/preferences/sync", s.handlePreferencesSyncAPI)
	s.registerAPIPageRoutes(r)
	r.Post("/server/preferences/tokens/primary", s.handleIssueOwnerToken)
	r.Post("/server/preferences/tokens/primary/reveal", s.handleRevealPrimaryToken)
	r.Post("/server/preferences/tokens/primary/rotate", s.handleRotatePrimaryToken)
	r.Delete("/server/preferences/tokens/primary", s.handleRevokePrimaryTokenAPI)
	r.Post("/server/preferences/tokens", s.handleCreateLinkedToken)
	r.Post("/server/preferences/tokens/{id}/reveal", s.handleRevealLinkedToken)
	r.Post("/server/preferences/tokens/{id}/rotate", s.handleRotateLinkedToken)
	r.Patch("/server/preferences/tokens/{id}", s.handleUpdateLinkedTokenAPI)
	r.Delete("/server/preferences/tokens/{id}", s.handleRevokeLinkedTokenAPI)
	r.Post("/server/preferences", s.handleSavePreferencesAPI)
	r.Get("/server/healthz", s.handleHealthAPI)
	r.Get("/server/healthz.txt", s.handleHealthAPI)
	r.Get("/server/about", s.handleInfoAPI)
	r.Get("/server/help", s.handleInfoAPI)
	r.Get("/server/privacy", s.handleInfoAPI)
	r.Get("/server/terms", s.handleInfoAPI)
	r.Get("/server/security", s.handleInfoAPI)
	r.Get("/server/security/policy", s.handleInfoAPI)
	r.Get("/server/security/thanks", s.handleInfoAPI)
	r.Get("/server/contact", s.handleContactAPI)
	r.Post("/server/contact", s.handleContactAPISubmit)
	r.Get("/server/dpo", s.handleDPOAPI)
	r.Get("/server/graphql", s.graphqlEndpoint())
	r.Post("/server/graphql", s.graphqlEndpoint())
	r.Post("/server/mcp", s.handleMCP)
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
}

func (s *Server) handleAutodiscover(w http.ResponseWriter, r *http.Request) {
	publicOrigins := []string{s.reportOrigin(r)}
	originSeen := map[string]struct{}{strings.ToLower(publicOrigins[0]): {}}
	scheme := "http"
	if strings.HasPrefix(publicOrigins[0], "https://") {
		scheme = "https"
	}
	for _, configured := range strings.Split(os.Getenv("DOMAIN"), ",") {
		host, _, ok := splitURLHost(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(configured, "https://"), "http://")))
		if !ok || host == "" || strings.HasSuffix(strings.ToLower(host), ".onion") || strings.HasSuffix(strings.ToLower(host), ".b32.i2p") {
			continue
		}
		origin := scheme + "://" + strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(configured, "https://"), "http://"))
		if !strings.Contains(origin, "://:") {
			if _, _, valid := splitURLHost(strings.TrimPrefix(origin, scheme+"://")); valid {
				key := strings.ToLower(origin)
				if _, exists := originSeen[key]; !exists {
					originSeen[key] = struct{}{}
					publicOrigins = append(publicOrigins, origin)
				}
			}
		}
	}
	versions := make(map[string]map[string]string)
	for platform, binary := range s.availableCLIBinaries() {
		versions[platform] = map[string]string{"version": binary.version, "sha256": binary.sha256}
	}
	data := map[string]interface{}{
		"ok": true,
		"data": map[string]interface{}{
			"name":            "countries",
			"version":         s.version,
			"api":             map[string]string{"current_version": strings.TrimPrefix(api.APIBasePath(), "/api/")},
			"urls":            publicOrigins,
			"cli_versions":    versions,
			"cli_min_version": s.config.Server.Update.CLIMinVersion,
		},
	}
	s.overlayMu.RLock()
	torInfo, i2pInfo := s.torHealth, s.i2pHealth
	s.overlayMu.RUnlock()
	result := data["data"].(map[string]interface{})
	if s.config.Server.Tor.Expose && torInfo.Enabled && torInfo.Hostname != "" {
		result["onion"] = torInfo.Hostname
	}
	if s.config.Server.I2P.Expose && i2pInfo.Enabled && i2pInfo.Hostname != "" {
		result["i2p"] = i2pInfo.Hostname
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) handleCLIBinary(w http.ResponseWriter, r *http.Request) {
	binary := chi.URLParam(r, "binary")
	if !isStaticCLIBinaryName(binary) {
		s.errorResponse(w, r, "CLI binary not found", http.StatusNotFound)
		return
	}
	if s.config.CLI.BinaryDownload.RequireAuth {
		token := bearerValue(r)
		if !s.validOperatorToken(token) {
			if strings.HasPrefix(r.Header.Get("User-Agent"), "countries-cli/") {
				s.writeCLIAuditEvent(r, "cli.token_revoked_detected", map[string]string{
					"token_prefix": cliTokenPrefix(token), "client_ip": requestClientIP(r.RemoteAddr),
				}, false)
			}
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"ok": false, "error": "UNAUTHORIZED", "message": "A valid operator token is required"})
			return
		}
	}
	platform, _ := cliPlatformKey(binary)
	available, found := s.availableCLIBinaries()[platform]
	if !found {
		s.errorResponse(w, r, "CLI binary not found", http.StatusNotFound)
		return
	}
	var file *os.File
	for _, directory := range s.cliBinaryDirectories() {
		candidate := filepath.Join(directory, available.name)
		linkInfo, err := os.Lstat(candidate)
		if err != nil || !linkInfo.Mode().IsRegular() {
			continue
		}
		file, err = os.Open(candidate)
		if err == nil {
			openedInfo, statErr := file.Stat()
			if statErr == nil && openedInfo.Mode().IsRegular() && os.SameFile(linkInfo, openedInfo) {
				break
			}
			_ = file.Close()
			file = nil
		}
	}
	if file == nil {
		s.errorResponse(w, r, "CLI binary not found", http.StatusNotFound)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("close CLI binary after serving: %v", err)
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		s.errorResponse(w, r, "CLI binary not found", http.StatusNotFound)
		return
	}
	s.writeCLIAuditEvent(r, "cli.update_started", map[string]string{
		"os_arch": platform, "version": available.version, "client_ip": requestClientIP(r.RemoteAddr),
	}, true)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+available.name+`"`)
	http.ServeContent(w, r, available.name, info.ModTime(), file)
}

type cliBinary struct {
	name    string
	version string
	sha256  string
}

func (s *Server) availableCLIBinaries() map[string]cliBinary {
	available := make(map[string]cliBinary)
	for _, directory := range s.cliBinaryDirectories() {
		versionBytes, err := os.ReadFile(filepath.Join(directory, "version.txt"))
		if err != nil || strings.TrimSpace(string(versionBytes)) == "" {
			continue
		}
		version := strings.TrimSpace(string(versionBytes))
		manifestBytes, err := os.ReadFile(filepath.Join(directory, "sha256.txt"))
		if err != nil {
			continue
		}
		manifest := parseCLIChecksumManifest(string(manifestBytes))
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			platform, valid := cliPlatformKey(name)
			if !valid || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
				continue
			}
			expected, recorded := manifest[name]
			if !recorded {
				log.Printf("WARN: CLI binary checksum is missing from sha256.txt: %s", name)
				continue
			}
			filename := filepath.Join(directory, name)
			linkInfo, err := os.Lstat(filename)
			if err != nil || !linkInfo.Mode().IsRegular() {
				continue
			}
			file, err := os.Open(filename)
			if err != nil {
				continue
			}
			openedInfo, statErr := file.Stat()
			if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(linkInfo, openedInfo) {
				_ = file.Close()
				continue
			}
			hash := sha256.New()
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil {
				continue
			}
			actual := hex.EncodeToString(hash.Sum(nil))
			if actual != expected {
				log.Printf("WARN: CLI binary checksum mismatch: %s", name)
				continue
			}
			if _, exists := available[platform]; !exists {
				available[platform] = cliBinary{name: name, version: version, sha256: actual}
			}
		}
	}
	return available
}

func parseCLIChecksumManifest(contents string) map[string]string {
	manifest := make(map[string]string)
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		checksum := strings.TrimPrefix(fields[0], "\\")
		filename := strings.TrimLeft(fields[len(fields)-1], "*")
		if filepath.Base(filename) != filename || len(checksum) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(checksum); err == nil {
			manifest[filename] = strings.ToLower(checksum)
		}
	}
	return manifest
}

func cliPlatformKey(name string) (string, bool) {
	if filepath.Base(name) != name || strings.ContainsAny(name, "/\\") || !strings.HasPrefix(name, "countries-cli-") {
		return "", false
	}
	platform := strings.TrimPrefix(name, "countries-cli-")
	if strings.HasPrefix(platform, "gui-") {
		return "", false
	}
	platform = strings.TrimSuffix(platform, ".exe")
	osName, arch, ok := strings.Cut(platform, "-")
	if !ok || arch == "" || strings.Contains(arch, "-") {
		return "", false
	}
	switch osName {
	case "linux", "darwin", "windows", "freebsd":
	default:
		return "", false
	}
	switch arch {
	case "amd64", "arm64":
	default:
		return "", false
	}
	if osName == "windows" && !strings.HasSuffix(name, ".exe") {
		return osName + "-" + arch, true
	}
	if osName != "windows" && strings.HasSuffix(name, ".exe") {
		return "", false
	}
	return osName + "-" + arch, true
}

func isStaticCLIBinaryName(name string) bool {
	_, valid := cliPlatformKey(name)
	return valid
}

func (s *Server) cliBinaryDirectories() []string {
	s.configMu.RLock()
	directory := s.config.CLI.BinaryDownload.Dir
	s.configMu.RUnlock()
	if strings.TrimSpace(directory) == "" {
		directory = "{data_dir}/cli"
	}
	directory = strings.ReplaceAll(directory, "{data_dir}", s.dataDir)
	return []string{filepath.Clean(directory)}
}

// SetDataDir sets the runtime data directory used for provisioned CLI binaries.
func (s *Server) SetDataDir(directory string) {
	if directory != "" {
		s.dataDir = directory
	}
}

func (s *Server) writeCLIAuditEvent(r *http.Request, action string, details map[string]string, success bool) {
	if s.db == nil {
		return
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return
	}
	if err := database.Write(r.Context(), s.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(r.Context(), `INSERT INTO audit_log (level, category, action, actor_ip, target_type, target_id, details, success) VALUES ('info', 'cli', ?, ?, 'binary', '', ?, ?)`, action, requestClientIP(r.RemoteAddr), string(encoded), success)
		return err
	}); err != nil {
		log.Printf("cli audit write failed: action=%s error=%v", action, err)
	}
}

func cliTokenPrefix(token string) string {
	if separator := strings.IndexByte(token, '_'); separator > 0 {
		return token[:separator+1]
	}
	if len(token) > 4 {
		return token[:4] + "..."
	}
	return ""
}

// SetGeoIPManager enables country lookup for requests after startup setup.
func (s *Server) SetGeoIPManager(manager countryLookup) {
	s.geoIPManager = manager
	if s.healthStore != nil {
		s.healthStore.RefreshChecks(context.Background())
	}
}

// HealthStore exposes the cached health registry for scheduler and startup registration.
func (s *Server) HealthStore() *database.HealthStore { return s.healthStore }

// SetSchedulerRunning updates the public-safe scheduler health check.
func (s *Server) SetSchedulerRunning(running bool) {
	s.schedulerRunning.Store(running)
	if s.healthStore != nil {
		s.healthStore.RefreshChecks(context.Background())
	}
}

// SetScheduler exposes scheduler status to authenticated debug endpoints.
func (s *Server) SetScheduler(scheduler *backgroundscheduler.Scheduler) {
	s.scheduler = scheduler
	s.SetSchedulerRunning(scheduler != nil)
}

// SetMaintenanceMode marks health as unavailable while planned maintenance is active.
func (s *Server) SetMaintenanceMode(active bool) {
	s.maintenanceMu.Lock()
	if active {
		if !s.maintenance.Load() {
			s.maintenanceSince = time.Now().UTC()
		}
		if s.maintenanceReason == "" {
			s.maintenanceReason = "database_connection"
		}
	} else {
		s.maintenanceReason = ""
		s.maintenanceSince = time.Time{}
		s.maintenanceLastAttempt = time.Time{}
		s.maintenanceAttempts = 0
		s.maintenanceExhausted = false
	}
	s.maintenance.Store(active)
	s.maintenanceMu.Unlock()
}

func (s *Server) IsMaintenanceMode() bool {
	return s.maintenance.Load()
}

func (s *Server) SetSelfHealingState(reason string, since time.Time, attempts int, exhausted bool) {
	s.maintenanceMu.Lock()
	if reason == "" {
		reason = "database_connection"
	}
	if since.IsZero() {
		since = time.Now().UTC()
	}
	s.maintenanceReason = reason
	s.maintenanceSince = since.UTC()
	s.maintenanceLastAttempt = time.Now().UTC()
	s.maintenanceAttempts = attempts
	s.maintenanceExhausted = exhausted
	s.maintenance.Store(true)
	s.maintenanceMu.Unlock()
}

func (s *Server) maintenanceWriteMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.maintenance.Load() || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		s.maintenanceMu.RLock()
		reason := s.maintenanceReason
		since := s.maintenanceSince
		s.maintenanceMu.RUnlock()
		if reason == "" {
			reason = "database_connection"
		}
		retryAfter := 30
		if interval, err := time.ParseDuration(s.config.Server.Maintenance.SelfHealing.RetryInterval); err == nil && interval > 0 {
			retryAfter = max(1, int(interval.Seconds()))
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		w.Header().Set("X-Maintenance-Mode", "true")
		w.Header().Set("X-Maintenance-Reason", reason)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"ok": false, "error": "MAINTENANCE", "message": "Server is in read-only maintenance mode",
				"details": map[string]any{"reason": reason, "self_healing": true, "since": since.Format(time.RFC3339)},
			})
			return
		}
		s.errorResponse(w, r, "Server is in read-only maintenance mode", http.StatusServiceUnavailable)
	})
}

// SetPendingRestart exposes config changes that require a process restart.
func (s *Server) SetPendingRestart(pending bool, reasons []string) {
	s.healthStateMu.Lock()
	if pending {
		s.restartReasons = append([]string(nil), reasons...)
	} else {
		s.restartReasons = nil
	}
	s.healthStateMu.Unlock()
	s.pendingRestart.Store(pending)
}

// SetDiskHealthStatus registers a public-safe status source for disk monitoring.
func (s *Server) SetDiskHealthStatus(status func() string) {
	s.diskHealthStatus = status
	if s.healthStore != nil {
		s.healthStore.RefreshChecks(context.Background())
	}
}

// RefreshHealth refreshes cached checks and persistent request counters.
func (s *Server) RefreshHealth(ctx context.Context) error {
	if s.healthStore == nil {
		return nil
	}
	return s.healthStore.Refresh(ctx)
}

// FlushHealth persists pending request counters during shutdown.
func (s *Server) FlushHealth(ctx context.Context) error {
	if s.healthStore == nil {
		return nil
	}
	return s.healthStore.FlushStats(ctx)
}

// SetMetricsDataDir configures the path used by system disk metrics.
func (s *Server) SetMetricsDataDir(directory string) {
	s.healthDataDir = directory
	if s.metrics != nil {
		s.metrics.SetDataDir(directory)
	}
	if s.healthStore != nil {
		s.healthStore.RefreshChecks(context.Background())
	}
}

// SetNotificationManager wires the configured notification channels into report delivery.
func (s *Server) SetNotificationManager(manager *notifications.Manager) {
	s.notificationManager = manager
}

// RecordScheduledTaskStart exposes a task start to the configured metrics collector.
func (s *Server) RecordScheduledTaskStart(task string) {
	if s.metrics != nil {
		s.metrics.RecordSchedulerStart(task)
	}
}

// RecordScheduledTaskEnd exposes a task completion to the configured metrics collector.
func (s *Server) RecordScheduledTaskEnd(task string, duration time.Duration, err error, endedAt time.Time) {
	if s.metrics != nil {
		s.metrics.RecordSchedulerEnd(task, duration, err, endedAt)
	}
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origins, wildcard, disabled := s.resolveCORSOrigins(r)
		origin := normalizeCORSOrigin(r.Header.Get("Origin"))
		matched := !disabled && origin != "" && (wildcard || containsOrigin(origins, origin))
		if matched {
			if wildcard {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, X-Requested-With, Authorization, X-CSRF-Token, X-XSRF-Token, MCP-Protocol-Version, Mcp-Method, Mcp-Name")
			if !wildcard && s.config.Server.CORS.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
		}
		preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
		if preflight {
			if matched && s.config.Server.CORS.MaxAge > 0 {
				w.Header().Set("Access-Control-Max-Age", strconv.Itoa(s.config.Server.CORS.MaxAge))
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) resolveCORSOrigins(r *http.Request) (map[string]struct{}, bool, bool) {
	configured := s.config.Server.CORS.AllowedOrigins
	if len(configured) == 1 && strings.TrimSpace(configured[0]) == "" {
		return nil, false, true
	}
	origins := make(map[string]struct{}, len(configured)+len(network.GetAllDomains())+4)
	wildcard := false
	add := func(value string) {
		if strings.TrimSpace(value) == "*" {
			wildcard = true
			return
		}
		if normalized := normalizeCORSOrigin(value); normalized != "" {
			origins[normalized] = struct{}{}
		}
	}
	for _, value := range configured {
		add(value)
	}
	for _, domain := range network.GetAllDomains() {
		add("https://" + strings.TrimSpace(domain))
	}
	if s.isTrustedPeer(r) {
		forwardedHost := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
		if forwardedHost != "" {
			learned := normalizeCORSOrigin("https://" + forwardedHost)
			if learned != "" {
				s.learnedCORSOriginsMu.Lock()
				s.learnedCORSOrigins[learned] = struct{}{}
				s.learnedCORSOriginsMu.Unlock()
			}
		}
	}
	s.learnedCORSOriginsMu.RLock()
	for origin := range s.learnedCORSOrigins {
		origins[origin] = struct{}{}
	}
	s.learnedCORSOriginsMu.RUnlock()
	if !wildcard && len(origins) == 0 {
		wildcard = true
	}
	return origins, wildcard, false
}

func normalizeCORSOrigin(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "null" || value == "*" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return ""
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}

func containsOrigin(origins map[string]struct{}, origin string) bool {
	_, ok := origins[origin]
	return ok
}

func (s *Server) corsOriginAllowed(r *http.Request) bool {
	origin := normalizeCORSOrigin(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	origins, wildcard, disabled := s.resolveCORSOrigins(r)
	return !disabled && (wildcard || containsOrigin(origins, origin))
}

func (s *Server) Run(ctx context.Context, addr string) error {
	return s.RunListeners(ctx, addr, "", nil, nil)
}

// RunListeners serves HTTP and HTTPS on their configured listeners.
func (s *Server) RunListeners(ctx context.Context, httpAddr, httpsAddr string, manager *ssl.Manager, domains []string) error {
	return s.RunListenersWithPrebound(ctx, httpAddr, httpsAddr, manager, domains, nil)
}

// RunListenersWithPrebound serves configured endpoints, reusing sockets bound
// during privileged startup when their address is present in prebound.
func (s *Server) RunListenersWithPrebound(ctx context.Context, httpAddr, httpsAddr string, manager *ssl.Manager, domains []string, prebound map[string]net.Listener) error {
	if s.metrics != nil && s.db != nil {
		updateDatabaseMetrics := func() {
			stats := s.db.Stats()
			s.metrics.SetDatabaseConnections(stats.OpenConnections, stats.InUse)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			primary, linked, err := database.CountActiveOwnerTokens(ctx, s.db, time.Now().UTC())
			cancel()
			if err == nil { s.metrics.SetActiveOwnerTokens(primary, linked) }
		}
		updateDatabaseMetrics()
		go func() {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					updateDatabaseMetrics()
				}
			}
		}()
	}
	if s.metrics != nil && s.config.Server.Metrics.IncludeSystem {
		s.metrics.StartSystemCollector(ctx, 30*time.Second)
		defer s.metrics.StopSystemCollector()
	}
	type listenerServer struct {
		server   *http.Server
		listener net.Listener
	}
	servers := make([]listenerServer, 0, 4)
	startedServing := false
	defer func() {
		if !startedServing {
			for _, item := range servers {
				_ = item.listener.Close()
			}
		}
	}()
	listen := func(address string) (net.Listener, error) {
		if listener := prebound[address]; listener != nil {
			delete(prebound, address)
			return listener, nil
		}
		return net.Listen("tcp", address)
	}
	if manager != nil {
		if err := manager.Validate(); err != nil {
			return err
		}
	}
	if httpAddr != "" {
		var handler http.Handler = s.router
		if manager != nil {
			handler = manager.GetHTTPHandler(handler)
		}
		listener, err := listen(httpAddr)
		if err != nil {
			return fmt.Errorf("listen on HTTP address %s: %w", httpAddr, err)
		}
		servers = append(servers, listenerServer{server: &http.Server{Handler: handler}, listener: listener})
	}
	if httpsAddr != "" {
		var tlsConfig *tls.Config
		var err error
		if manager == nil {
			return fmt.Errorf("HTTPS listener requires an SSL manager")
		}
		tlsConfig, err = manager.GetTLSConfig(domains)
		if err != nil {
			return err
		}
		listener, err := listen(httpsAddr)
		if err != nil {
			return fmt.Errorf("listen on HTTPS address %s: %w", httpsAddr, err)
		}
		servers = append(servers, listenerServer{server: &http.Server{Handler: s.router}, listener: tls.NewListener(listener, tlsConfig)})
	}
	if len(servers) == 0 {
		return fmt.Errorf("at least one HTTP or HTTPS listener is required")
	}
	errChan := make(chan error, len(servers))
	for _, item := range servers {
		item := item
		go func() {
			if err := item.server.Serve(item.listener); err != nil && err != http.ErrServerClosed {
				errChan <- err
			}
		}()
	}
	startedServing = true

	select {
	case <-ctx.Done():
		s.shuttingDown.Store(true)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var shutdownErr error
		for _, item := range servers {
			if err := item.server.Shutdown(shutdownCtx); err != nil && shutdownErr == nil {
				shutdownErr = err
			}
		}
		return shutdownErr
	case err := <-errChan:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, item := range servers {
			_ = item.server.Shutdown(shutdownCtx)
		}
		return err
	}
}

// Web UI handlers
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		renderFallbackError(w, r, http.StatusInternalServerError, s)
		return
	}

	data := s.pageContext(w, r, "/")
	data["TotalCountries"] = s.countriesService.Count()
	data["Version"] = s.version
	data["BaseURL"] = s.reportOrigin(r)

	s.renderTemplateResponse(w, r, "home.html", data, http.StatusOK)
}

func (s *Server) handleSearchPage(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		renderFallbackError(w, r, http.StatusInternalServerError, s)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	data := s.pageContext(w, r, "/search")
	data["Query"], data["HasSearch"] = query, query != ""
	if query != "" {
		filters, err := searchFiltersFromRequest(r)
		if err != nil {
			s.errorResponse(w, r, err.Error(), http.StatusBadRequest)
			return
		}
		data["Results"] = s.countriesService.SearchFiltered(filters)
	}

	s.renderTemplateResponse(w, r, "search.html", data, http.StatusOK)
}

func (s *Server) handleCoordinatesPage(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		renderFallbackError(w, r, http.StatusInternalServerError, s)
		return
	}

	data := s.pageContext(w, r, "/coordinates")
	latText, lonText := strings.TrimSpace(r.URL.Query().Get("lat")), strings.TrimSpace(r.URL.Query().Get("lon"))
	if latText != "" || lonText != "" {
		lat, latErr := strconv.ParseFloat(latText, 64)
		lon, lonErr := strconv.ParseFloat(lonText, 64)
		if latErr != nil || lonErr != nil || !validCoordinates(lat, lon) {
			data["Error"] = "Enter a latitude from -90 to 90 and a longitude from -180 to 180."
		} else {
			data["Latitude"], data["Longitude"] = latText, lonText
			data["Nearest"] = s.countriesService.FindNearest(lat, lon)
		}
	}

	status := http.StatusOK
	if data["Error"] != nil {
		status = http.StatusBadRequest
	}
	s.renderTemplateResponse(w, r, "coordinates.html", data, status)
}

func (s *Server) handleOpenAPIPage(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		renderFallbackError(w, r, http.StatusInternalServerError, s)
		return
	}

	data := s.pageContext(w, r, "/server/docs/swagger")

	s.renderTemplateResponse(w, r, "openapi.html", data, http.StatusOK)
}

// PWA handlers
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	s.setDynamicAssetCacheHeaders(w)
	manifest := map[string]interface{}{
		"name":             "Countries API",
		"short_name":       "Countries",
		"description":      "World countries database with geolocation",
		"start_url":        s.requestURLPrefix(r) + "/",
		"scope":            s.requestURLPrefix(r) + "/",
		"display":          "standalone",
		"background_color": "#1a1a2e",
		"theme_color":      "#0f3460",
		"icons": []map[string]interface{}{
			{"src": s.requestURLPrefix(r) + "/static/icons/icon-192.png?v=" + s.assetStamp(), "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": s.requestURLPrefix(r) + "/static/icons/icon-512.png?v=" + s.assetStamp(), "sizes": "512x512", "type": "image/png", "purpose": "any"},
			{"src": s.requestURLPrefix(r) + "/static/icons/icon-maskable-192.png?v=" + s.assetStamp(), "sizes": "192x192", "type": "image/png", "purpose": "maskable"},
			{"src": s.requestURLPrefix(r) + "/static/icons/icon-maskable-512.png?v=" + s.assetStamp(), "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
			{"src": s.requestURLPrefix(r) + "/static/icons/icon.svg?v=" + s.assetStamp(), "sizes": "any", "type": "image/svg+xml"},
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		log.Printf("write web manifest: %v", err)
	}
}

func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	s.setDynamicAssetCacheHeaders(w)
	prefix := s.requestURLPrefix(r)
	stamp := s.assetStamp()
	base := strings.TrimSuffix(prefix, "/")
	offlineStringsCache, _ := json.Marshal(s.appName() + "-offline-strings")
	sw := `const CACHE_VERSION = '` + stamp + `';
const CACHE_PREFIX = 'Countries API-cache-';
const CACHE_NAME = CACHE_PREFIX + CACHE_VERSION;
const STRINGS_CACHE = ` + string(offlineStringsCache) + `;
const APP_BASE = '` + base + `';
const NAV_TIMEOUT_MS = 8000;
const PRECACHE_ASSETS = [
  APP_BASE + '/static/css/common.css?v=' + CACHE_VERSION,
  APP_BASE + '/static/css/components.css?v=' + CACHE_VERSION,
  APP_BASE + '/static/css/public.css?v=' + CACHE_VERSION,
  APP_BASE + '/static/js/app.js?v=' + CACHE_VERSION,
  APP_BASE + '/static/icons/icon-192.png?v=' + CACHE_VERSION,
  APP_BASE + '/static/icons/icon-512.png?v=' + CACHE_VERSION,
  APP_BASE + '/static/icons/icon-maskable-192.png?v=' + CACHE_VERSION,
  APP_BASE + '/static/icons/icon-maskable-512.png?v=' + CACHE_VERSION,
  APP_BASE + '/static/icons/icon.svg?v=' + CACHE_VERSION
];

self.addEventListener('install', event => {
  event.waitUntil(caches.open(CACHE_NAME).then(cache => cache.addAll(PRECACHE_ASSETS)));
});

self.addEventListener('activate', event => {
  event.waitUntil(caches.keys()
    .then(keys => Promise.all(keys.filter(key => key.startsWith(CACHE_PREFIX) && key !== CACHE_NAME && key !== STRINGS_CACHE).map(key => caches.delete(key))))
    .then(() => self.clients.claim()));
});

self.addEventListener('message', event => {
  if (event.data && event.data.type === 'SKIP_WAITING') self.skipWaiting();
});

function withTimeout(promise, ms) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('network timeout')), ms);
    promise.then(value => { clearTimeout(timer); resolve(value); }, error => { clearTimeout(timer); reject(error); });
  });
}

async function offlineFallbackResponse() {
  let copy = { lang: 'en', title: 'Offline', heading: 'You are offline', message: 'You are offline.' };
  try {
    const cache = await caches.open(STRINGS_CACHE);
    const stored = await cache.match(new URL(APP_BASE + '/offline-strings.json', self.location.origin));
    if (stored) copy = Object.assign(copy, await stored.json());
  } catch (_) {}
  const lang = copy.lang || 'en';
  const title = copy.title || 'Offline';
  const heading = copy.heading || 'You are offline';
  const message = copy.message || 'You are offline.';
  const escapeHTML = value => value.replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char]));
  const safeTitle = escapeHTML(title);
  const safeHeading = escapeHTML(heading);
  const safeMessage = escapeHTML(message);
  const direction = lang === 'ar' ? 'rtl' : 'ltr';
  return new Response('<!doctype html><html lang="' + lang + '" dir="' + direction + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>' + safeTitle + '</title></head><body><main><h1>' + safeHeading + '</h1><p>' + safeMessage + '</p></main></body></html>', {
    status: 503, statusText: 'Service Unavailable', headers: { 'Content-Type': 'text/html; charset=utf-8' }
  });
}

function isAPIPath(path) {
  return path === APP_BASE + '/api' || path.startsWith(APP_BASE + '/api/');
}

self.addEventListener('fetch', event => {
  const request = event.request;
  const url = new URL(request.url);
  if (request.method !== 'GET' || url.origin !== self.location.origin || isAPIPath(url.pathname)) return;

  if (request.mode === 'navigate') {
    event.respondWith(withTimeout(fetch(request), NAV_TIMEOUT_MS)
      .then(response => {
        if (response.redirected) return response;
        if (!response.ok || response.headers.get('X-Offline-Cache') !== 'public') {
          return caches.open(CACHE_NAME).then(cache => cache.delete(request)).then(() => response);
        }
        return caches.open(CACHE_NAME).then(cache => cache.put(request, response.clone())).then(() => response);
      })
      .catch(async () => (await caches.match(request)) || offlineFallbackResponse())
      .catch(() => offlineFallbackResponse()));
    return;
  }

  if (url.pathname.startsWith(APP_BASE + '/static/')) {
    event.respondWith(caches.match(request).then(cached => cached || fetch(request).then(response => {
      if (!response.ok || response.redirected) return response;
      return caches.open(CACHE_NAME).then(cache => cache.put(request, response.clone())).then(() => response);
    })).catch(() => new Response('', { status: 504, statusText: 'Gateway Timeout' })));
    return;
  }

  event.respondWith(fetch(request).then(response => {
    return response;
  }).catch(async () => (await caches.match(request)) || new Response('', { status: 504, statusText: 'Gateway Timeout' }))
    .catch(() => new Response('', { status: 504, statusText: 'Gateway Timeout' })));
});`
	w.Header().Set("Content-Type", "application/javascript")
	if _, err := w.Write([]byte(sw + "\n")); err != nil {
		log.Printf("write service worker: %v", err)
	}
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
	builder.WriteString("Sitemap: " + s.BuildURL(r, "/sitemap.xml") + "\n")
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(builder.String()))
}

func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	paths := []string{"/", "/search", "/coordinates", "/server/about", "/server/help", "/server/privacy", "/server/terms", "/server/security", "/server/security/policy", "/server/security/thanks", "/server/dpo", "/server/docs/swagger", "/server/docs/graphql"}
	urls := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, registered := s.registeredPage(path); registered {
			continue
		}
		if sitemapPathAllowed(path, s.config.WebRobots) {
			urls = append(urls, s.BuildURL(r, path))
		}
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, xml.Header+"<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n")
	for _, location := range urls {
		encoded, err := xml.Marshal(struct {
			XMLName xml.Name `xml:"url"`
			Loc     string   `xml:"loc"`
		}{Loc: location})
		if err != nil {
			continue
		}
		_, _ = w.Write(append(encoded, '\n'))
	}
	_, _ = io.WriteString(w, "</urlset>\n")
}

func sitemapPathAllowed(path string, robots config.WebRobotsConfig) bool {
	allowed := false
	for _, rule := range robots.Allow {
		if rule == "/" || path == rule || strings.HasPrefix(path, strings.TrimSuffix(rule, "*")) {
			allowed = true
			break
		}
	}
	for _, rule := range robots.Deny {
		if rule != "" && (path == rule || strings.HasPrefix(path, strings.TrimSuffix(rule, "*"))) {
			return false
		}
	}
	return allowed
}

func (s *Server) handleSecurity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	security := "Contact: https://github.com/apimgr/countries/security/advisories/new\n"
	if s.config.Server.Pages.Contact.Enabled {
		security += "Contact: " + s.BuildURL(r, "/server/contact") + "?security_id=" + securityIDFor(s.contactKey, time.Now().Unix()/172800) + "\n"
	}
	address := strings.TrimSpace(s.config.Server.Contact.Security.Email)
	if torRequest(r) || strings.HasSuffix(strings.ToLower(r.Host), ".onion") {
		address = strings.TrimSpace(s.config.Server.Tor.ContactEmail)
	}
	if address != "" {
		if parsed, err := mail.ParseAddress(address); err == nil && parsed.Address == address {
			security += "Contact: mailto:" + address + "\n"
		}
	}
	security += "Policy: " + s.BuildURL(r, "/server/security/policy") + "\n" +
		"Expires: " + time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339) + "\n"
	if _, fingerprint, err := s.readSecurityPublicKey(); err == nil {
		security += "Encryption: " + s.BuildURL(r, "/.well-known/pgp-key.asc") + "\n"
		security += "# PGP fingerprint: " + fingerprint + "\n"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(security)))
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(security))
	}
}

func (s *Server) handleLLMS(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(s.config.Server.Branding.Title)
	if name == "" {
		name = "Countries"
	}
	description := strings.TrimSpace(s.config.Server.Branding.Description)
	if description == "" {
		description = "Public, read-only country reference data and geographic lookups."
	}
	var content strings.Builder
	readLimit := s.config.Server.RateLimit.Read
	requestsPerMinute := float64(readLimit.Requests) * 60 / float64(readLimit.Window)
	fmt.Fprintf(&content, "# %s\n> %s\n\n## API\nBase URL: %s\nAuthentication: Public country reads require no authentication; protected operations are marked below.\nRate limit: %.2f requests/minute for public reads (configured %d requests per %d seconds).\nMCP: %s (Streamable HTTP; bearer token required)\n\n", name, description, s.BuildURL(r, api.APIBasePath()), requestsPerMinute, readLimit.Requests, readLimit.Window, s.BuildURL(r, "/mcp"))
	if s.config.Web.LLMS.IncludeEndpoints {
		specification, err := swagger.Spec(s.version, s.router)
		if err != nil {
			http.Error(w, "AI discovery is temporarily unavailable", http.StatusInternalServerError)
			return
		}
		paths, _ := specification["paths"].(map[string]interface{})
		pathNames := make([]string, 0, len(paths))
		for path := range paths {
			if strings.HasPrefix(path, "/api/") || path == "/mcp" {
				if strings.Contains(path, "/metrics") || strings.Contains(path, "/debug/") {
					continue
				}
				pathNames = append(pathNames, path)
			}
		}
		sort.Strings(pathNames)
		content.WriteString("## Endpoints\n")
		content.WriteString("- GET /server/healthz — Health check (no authentication)\n- GET /server/about — Server information (no authentication)\n")
		for _, path := range pathNames {
			operations, _ := paths[path].(map[string]interface{})
			methods := make([]string, 0, len(operations))
			for method := range operations {
				methods = append(methods, method)
			}
			sort.Strings(methods)
			for _, method := range methods {
				operation, _ := operations[method].(map[string]interface{})
				if operation == nil {
					continue
				}
				summary, _ := operation["summary"].(string)
				security, protected := operation["security"].([]interface{})
				if protected && len(security) == 0 {
					protected = false
				}
				fmt.Fprintf(&content, "- %s %s", strings.ToUpper(method), path)
				if summary != "" {
					fmt.Fprintf(&content, " — %s", summary)
				}
				if protected {
					content.WriteString(" (bearer token required)")
				}
				content.WriteByte('\n')
				if s.config.Web.LLMS.IncludeSchemas {
					details := make(map[string]interface{})
					for _, key := range []string{"parameters", "requestBody", "responses"} {
						if value, ok := operation[key]; ok {
							details[key] = value
						}
					}
					if encoded, err := json.MarshalIndent(details, "  ", "  "); err == nil {
						content.WriteString("  ```json\n  ")
						content.Write(encoded)
						content.WriteString("\n  ```\n")
					}
				}
			}
		}
		if s.config.Web.LLMS.IncludeSchemas {
			if components, ok := specification["components"].(map[string]interface{}); ok {
				if schemas, ok := components["schemas"]; ok {
					if encoded, err := json.MarshalIndent(schemas, "", "  "); err == nil {
						content.WriteString("\n## Schemas\n```json\n")
						content.Write(encoded)
						content.WriteString("\n```\n")
					}
				}
			}
		}
	}
	content.WriteString("\n## Capabilities\n- Browse and search the embedded country dataset.\n- Read country records and geographic lookup results through REST and GraphQL.\n- Use the Streamable HTTP MCP endpoint with a bearer token.\n")
	for _, section := range s.config.Web.LLMS.CustomSections {
		section = strings.TrimSpace(section)
		if section != "" {
			content.WriteString("\n")
			content.WriteString(section)
			content.WriteString("\n")
		}
	}
	content.WriteString("\n## Contact\n")
	apiContact := strings.TrimSpace(s.config.Web.APIContact)
	securityContact := strings.TrimSpace(s.config.Web.Security.Contact)
	if apiContact != "" {
		fmt.Fprintf(&content, "API issues: %s\n", apiContact)
	}
	if securityContact != "" {
		fmt.Fprintf(&content, "Security: %s\n", securityContact)
	}
	fmt.Fprintf(&content, "\nComplete API reference: %s\n", s.BuildURL(r, "/server/docs/swagger"))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, content.String())
}

// API handlers
func (s *Server) handleGetCountries(w http.ResponseWriter, r *http.Request) {
	filters, err := searchFiltersFromRequest(r)
	if err != nil {
		s.errorResponse(w, r, err.Error(), http.StatusBadRequest)
		return
	}
	allCountries := s.countriesService.GetAll()
	if filters != (countries.SearchFilters{}) {
		allCountries = s.countriesService.SearchFiltered(filters)
	}
	page, limit, err := paginationFromRequest(r)
	if err != nil {
		s.errorResponse(w, r, err.Error(), http.StatusBadRequest)
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
		s.errorResponse(w, r, err.Error(), http.StatusBadRequest)
		return
	}
	if filters == (countries.SearchFilters{}) {
		s.errorResponse(w, r, "At least one search filter is required", http.StatusBadRequest)
		return
	}
	results := s.countriesService.SearchFiltered(filters)
	page, limit, err := paginationFromRequest(r)
	if err != nil {
		s.errorResponse(w, r, err.Error(), http.StatusBadRequest)
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
		s.errorResponse(w, r, "Country code is required", http.StatusBadRequest)
		return
	}

	country := s.countriesService.GetByCode(code)
	if country == nil {
		s.errorResponse(w, r, "Country not found", http.StatusNotFound)
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
		s.errorResponse(w, r, "Latitude and longitude are required", http.StatusBadRequest)
		return
	}

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		s.errorResponse(w, r, "Invalid latitude", http.StatusBadRequest)
		return
	}

	lon, err := strconv.ParseFloat(lonStr, 64)
	if err != nil {
		s.errorResponse(w, r, "Invalid longitude", http.StatusBadRequest)
		return
	}
	if !validCoordinates(lat, lon) {
		s.errorResponse(w, r, "Latitude or longitude is outside its valid range", http.StatusBadRequest)
		return
	}

	nearest := s.countriesService.FindNearest(lat, lon)
	if nearest == nil {
		s.errorResponse(w, r, "No countries found", http.StatusNotFound)
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

	defer func() { _ = r.Body.Close() }()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		s.errorResponse(w, r, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Lat == nil || req.Lon == nil {
		s.errorResponse(w, r, "Latitude and longitude are required", http.StatusBadRequest)
		return
	}
	if !validCoordinates(*req.Lat, *req.Lon) {
		s.errorResponse(w, r, "Latitude or longitude is outside its valid range", http.StatusBadRequest)
		return
	}
	nearest := s.countriesService.FindNearest(*req.Lat, *req.Lon)
	if nearest == nil {
		s.errorResponse(w, r, "No countries found", http.StatusNotFound)
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
		s.errorResponse(w, r, "Latitude and longitude are required", http.StatusBadRequest)
		return
	}

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		s.errorResponse(w, r, "Invalid latitude", http.StatusBadRequest)
		return
	}

	lon, err := strconv.ParseFloat(lonStr, 64)
	if err != nil {
		s.errorResponse(w, r, "Invalid longitude", http.StatusBadRequest)
		return
	}
	if !validCoordinates(lat, lon) {
		s.errorResponse(w, r, "Latitude or longitude is outside its valid range", http.StatusBadRequest)
		return
	}

	radius := 500.0 // Default 500km
	if radiusStr != "" {
		radius, err = strconv.ParseFloat(radiusStr, 64)
		if err != nil {
			s.errorResponse(w, r, "Invalid radius", http.StatusBadRequest)
			return
		}
	}

	if math.IsNaN(radius) || math.IsInf(radius, 0) || radius < 0 || radius > 20038 {
		s.errorResponse(w, r, "Radius must be between 0 and 20038 kilometers", http.StatusBadRequest)
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
	_, _ = w.Write([]byte("Total Countries: " + strconv.Itoa(s.countriesService.Count()) + "\nVersion: " + s.version + "\nData Source: embedded country dataset\n"))
}

func (s *Server) handleCount(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, map[string]interface{}{
		"ok":   true,
		"data": map[string]interface{}{"count": s.countriesService.Count()},
	})
}

func (s *Server) handleCountTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(strconv.Itoa(s.countriesService.Count())))
}

func validCoordinates(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsInf(lat, 0) && lat >= -90 && lat <= 90 &&
		!math.IsNaN(lon) && !math.IsInf(lon, 0) && lon >= -180 && lon <= 180
}

func (s *Server) jsonResponse(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) errorResponse(w http.ResponseWriter, r *http.Request, message string, status int) {
	code := "BAD_REQUEST"
	switch status {
	case http.StatusNotFound:
		code = "NOT_FOUND"
	case http.StatusForbidden:
		code = "FORBIDDEN"
	case http.StatusMethodNotAllowed:
		code = "METHOD_NOT_ALLOWED"
	case http.StatusServiceUnavailable:
		code = "SERVICE_UNAVAILABLE"
	case http.StatusInternalServerError:
		code = "INTERNAL_ERROR"
	}
	if acceptsHTML(r) && !strings.HasPrefix(r.URL.Path, "/api/") && s.templates != nil {
		data := s.pageContext(w, r, r.URL.Path)
		data["StatusCode"] = status
		if status == http.StatusNotFound {
			data["ErrorMessage"] = i18n.Translate(data["Lang"].(string), "error.not_found")
		} else if status >= http.StatusInternalServerError {
			data["ErrorMessage"] = i18n.Translate(data["Lang"].(string), "error.generic")
		} else {
			data["ErrorMessage"] = message
		}
		var rendered bytes.Buffer
		if err := s.executeTemplate(&rendered, "error.html", data); err != nil {
			log.Printf("Error page template: %v", err)
			renderFallbackError(w, r, status, s)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		s.setHTMLResponseHeaders(w, r, status)
		w.WriteHeader(status)
		_, _ = w.Write(rendered.Bytes())
		return
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
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"ok":false,"error":"INTERNAL_ERROR"}` + "\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
