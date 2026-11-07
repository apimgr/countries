package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/swagger"
)

type infoSection struct {
	Title   string
	Text    string
	Bullets []string
}

type infoPage struct {
	Title       string
	Description string
	Version     string
	BuildDate   string
	Sections    []infoSection
	Theme       string
	APIBasePath string
	Lang        string
	Dir         string
	Path        string
	Languages   []languageOption
}

func (s *Server) handleInfoPage(w http.ResponseWriter, r *http.Request) {
	page := s.infoForPath(r.URL.Path)
	if page.Title == "" {
		http.NotFound(w, r)
		return
	}
	if s.templates == nil {
		http.Error(w, "Page is unavailable", http.StatusInternalServerError)
		return
	}
	page.Theme = s.themeForRequest(r)
	page.Lang = s.languageForRequest(w, r)
	page.Dir = i18n.Direction(page.Lang)
	page.Path = r.URL.Path
	page.Languages = languageOptions()
	page.APIBasePath = api.APIBasePath()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "info.html", page); err != nil {
		http.Error(w, "Page is unavailable", http.StatusInternalServerError)
	}
}

func (s *Server) infoForPath(path string) infoPage {
	page := infoPage{Version: s.version, BuildDate: s.buildDate}
	switch path {
	case "/server/about":
		page.Title = "About Countries"
		page.Description = "A read-only country reference service with a versioned API and web interface."
		page.Sections = []infoSection{
			{Title: "What this service provides", Text: "Countries provides 249 embedded ISO 3166-1 records through public REST and GraphQL endpoints and server-rendered pages. Records include names, ISO codes, capitals, region, currencies, languages, population and observation year where available, area, flags, borders, time zones, top-level domains, and representative coordinates."},
			{Title: "Features", Bullets: []string{"Search country names", "Look up records by country code", "Find the nearest country centroid or countries within a radius", "Read the same data from the versioned REST API"}},
			{Title: "Project", Text: "Source code and project details are available at https://github.com/apimgr/countries."},
		}
	case "/server/help":
		page.Title = "Countries Help"
		page.Description = "Use the public country lookup pages and API."
		page.Sections = []infoSection{
			{Title: "Find countries", Text: "Search country names at /search or query the API with GET " + api.APIPath("countries/search") + "?q=united. Look up a country code at GET " + api.APIPath("countries/US") + "."},
			{Title: "Coordinates", Text: "Find the country with the nearest representative coordinate using GET " + api.APIPath("coordinates") + "?lat=40.7&lon=-74.0. Nearby results accept a radius in kilometers at GET " + api.APIPath("nearby") + "?lat=40.7&lon=-74.0&radius=500."},
			{Title: "API reference", Bullets: []string{"REST documentation: /server/docs/swagger", "GraphQL explorer: /server/docs/graphql", "Health status: /server/healthz"}},
			{Title: "Configuration", Text: "Operators configure the server through server.yml. This service has no account system or web administration panel."},
		}
	case "/server/privacy":
		page.Title = "Privacy"
		page.Description = "Privacy information for the Countries service."
		page.Sections = []infoSection{
			{Title: "Country data", Text: "Country records are static data embedded in the application. The service does not store user accounts or personal profiles."},
			{Title: "Request metadata", Text: "The HTTP server writes request access logs. Depending on the deployment and logging configuration, those logs may contain connection metadata such as client addresses, request paths, and response status. Operators control log handling and retention."},
			{Title: "External services", Text: "The application does not call external services to answer country lookups."},
			{Title: "Contact", Text: "For project questions, use the project issue tracker at https://github.com/apimgr/countries/issues."},
		}
	case "/server/terms":
		page.Title = "Terms"
		page.Description = "Conditions for using the Countries reference service."
		page.Sections = []infoSection{
			{Title: "Use of the service", Text: "The service provides read-only country reference data and makes no account or write API available."},
			{Title: "Accuracy", Text: "Country data is provided for reference. Verify important geographic, legal, and operational decisions with authoritative sources."},
			{Title: "Software license", Text: "The application code is distributed under the MIT License. Embedded data may have separate terms; consult the project documentation for current data-source attribution."},
			{Title: "Availability", Text: "A public deployment may be changed or discontinued by its operator."},
		}
	case "/server/security":
		page.Title = "Security"
		page.Description = "Security reporting and privacy information for the Countries service."
		page.Sections = []infoSection{
			{Title: "Report a project vulnerability", Text: "Use GitHub private vulnerability reporting at https://github.com/apimgr/countries/security/advisories/new. Do not post vulnerability details in public issues."},
			{Title: "Report an issue with this deployment", Text: "This service is read-only and stores no user accounts. Contact the operator that runs this deployment for service-specific concerns. The project security.txt file identifies the upstream reporting policy."},
			{Title: "Scope", Text: "Country records are embedded static data. The application does not call external services to answer requests."},
		}
	case "/server/contact":
		page.Title = "Contact"
		page.Description = "Contact the Countries project maintainers."
		page.Sections = []infoSection{
			{Title: "Project questions", Text: "Use the project issue tracker at https://github.com/apimgr/countries/issues."},
			{Title: "Security reports", Text: "Use GitHub private vulnerability reporting at https://github.com/apimgr/countries/security/advisories/new. Do not disclose vulnerabilities in public issues."},
		}
	default:
		return page
	}
	return page
}

func (s *Server) handleInfoAPI(w http.ResponseWriter, r *http.Request) {
	page := s.infoForPath(strings.TrimSuffix(r.URL.Path, "/"))
	if page.Title == "" {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "Page not found")
		return
	}
	page.Version = s.version
	page.BuildDate = s.buildDate
	s.jsonResponse(w, map[string]interface{}{
		"ok": true,
		"data": map[string]interface{}{
			"title": page.Title, "description": page.Description, "sections": page.Sections,
		},
	})
}

func (s *Server) handleHealthPage(w http.ResponseWriter, r *http.Request) {
	if acceptsHTML(r) && s.templates != nil {
		lang := s.languageForRequest(w, r)
		page := infoPage{
			Title:       "Service health",
			Description: "Countries is responding to HTTP requests.",
			Version:     s.version,
			BuildDate:   s.buildDate,
			Theme:       s.themeForRequest(r),
			Lang:        lang,
			Dir:         i18n.Direction(lang),
			Path:        "/server/healthz",
			Languages:   languageOptions(),
			Sections: []infoSection{{
				Title:   "Status",
				Bullets: []string{"Status: healthy", "Country records loaded: " + strconv.Itoa(s.countriesService.Count()), "Version: " + s.version},
			}},
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := s.templates.ExecuteTemplate(w, "info.html", page); err != nil {
			http.Error(w, "Health page is unavailable", http.StatusInternalServerError)
		}
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/plain") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("OK: Countries is healthy\n"))
		return
	}
	s.handleHealthz(w, r)
}

func (s *Server) handleGraphQLPage(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		http.Error(w, "Page is unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "graphql.html", s.pageContext(w, r, "/server/docs/graphql")); err != nil {
		http.Error(w, "Page is unavailable", http.StatusInternalServerError)
	}
}

func (s *Server) graphqlEndpoint() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.graphqlHandler == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "GraphQL is unavailable")
			return
		}
		s.graphqlHandler.ServeHTTP(w, r)
	}
}

func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	data, err := json.MarshalIndent(swagger.Spec(s.version), "", "  ")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to generate the API document")
		return
	}
	_, _ = w.Write(append(data, '\n'))
}

func acceptsHTML(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "text/html") {
		return true
	}
	if strings.Contains(accept, "application/json") || strings.Contains(accept, "text/plain") {
		return false
	}
	ua := strings.ToLower(r.UserAgent())
	return strings.Contains(ua, "mozilla/") || strings.Contains(ua, "chrome/") || strings.Contains(ua, "safari/") || strings.Contains(ua, "firefox/")
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]interface{}{"ok": false, "error": code, "message": message})
}
