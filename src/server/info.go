package server

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/swagger"
)

type infoSection struct {
	ID         string
	Title      string
	TitleKey   string
	Text       string
	TextKey    string
	Bullets    []string
	BulletKeys []string
}

func renderSecurityPolicyMarkdown(markdown string) template.HTML {
	htmlBody := string(markdownToHTML(markdown))
	for _, heading := range [][2]string{{"h5", "h6"}, {"h4", "h5"}, {"h3", "h4"}, {"h2", "h3"}, {"h1", "h3"}} {
		htmlBody = strings.ReplaceAll(htmlBody, "<"+heading[0], "<"+heading[1])
		htmlBody = strings.ReplaceAll(htmlBody, "</"+heading[0]+">", "</"+heading[1]+">")
	}
	return template.HTML(htmlBody)
}

type MCPHelpTool struct {
	Name        string
	Description string
}

type MCPHelpData struct {
	Enabled          bool
	URL              string
	Aliases          []string
	AuthScheme       string
	ProtocolVersions []string
	Tools            []MCPHelpTool
	Project          template.HTML
	ProjectMarkdown  string
}

type infoPage struct {
	Request             *http.Request
	AppName             string
	Title               string
	TitleKey            string
	LocalizedTitle      string
	Description         string
	DescriptionKey      string
	Tagline             string
	Version             string
	BuildDate           string
	Sections            []infoSection
	Theme               string
	APIBasePath         string
	Lang                string
	Dir                 string
	Path                string
	URLPrefix           string
	Languages           []languageOption
	DPOName             string
	DPOEmail            string
	PrivacyURL          string
	Privacy             config.PrivacyConfig
	PrivacyContent      config.PrivacyContent
	TrackingEnabled     bool
	TrackingType        string
	TrackingTypeName    string
	TrackingMessage     string
	CCPAOptedOut        bool
	CSRFToken           string
	HasConsentCookie    bool
	SecurityFormURL     string
	ReportFormURL       string
	SecurityEmail       string
	SecurityExpires     string
	SecurityPGPURL      string
	SecurityFingerprint string
	HelpPages           []pageRegistryEntry
	PreferencePages     []pageRegistryEntry
	MCP                 MCPHelpData
	HealthPage          healthTemplateData
	SecurityPolicyHTML  template.HTML
	SecurityThanks      []securityThanksEntry
}

func (s *Server) dpoContact() (string, string) {
	name := strings.TrimSpace(s.config.Server.Contact.DPO.Name)
	email := strings.TrimSpace(s.config.Server.Contact.DPO.Email)
	if email == "" {
		email = strings.TrimSpace(s.config.Server.Contact.General.Email)
	}
	if email != "" {
		address, err := mail.ParseAddress(email)
		if err != nil || address.Address != email {
			return name, ""
		}
	}
	return name, email
}

func (s *Server) handleDPOPage(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		renderFallbackError(w, r, http.StatusInternalServerError)
		return
	}
	name, email := s.dpoContact()
	lang := s.languageForRequest(w, r)
	page := infoPage{
		Request:     r,
		AppName:     s.appName(),
		Title:       i18n.Translate(lang, "dpo.title"),
		Description: i18n.Translate(lang, "dpo.description"),
		Version:     s.version, BuildDate: s.buildDate, Theme: s.themeForRequest(r),
		Lang: lang, Path: r.URL.Path, URLPrefix: s.requestURLPrefix(r), Languages: languageOptions(),
		DPOName: name, DPOEmail: email, PrivacyURL: s.requestURLPrefix(r) + "/server/privacy",
		CSRFToken:        csrfToken(w, r, strings.HasPrefix(s.reportOrigin(r), "https://")),
		HasConsentCookie: s.hasConsentCookie(r),
	}
	page.Dir = i18n.Direction(page.Lang)
	s.renderTemplateResponse(w, r, "info.html", page, http.StatusOK)
}

func (s *Server) handleDPOAPI(w http.ResponseWriter, r *http.Request) {
	name, email := s.dpoContact()
	s.jsonResponse(w, map[string]interface{}{
		"ok": true,
		"data": map[string]interface{}{
			"name": name, "email": email, "privacy_url": s.BuildURL(r, "/server/privacy"),
		},
	})
}

func (s *Server) handleInfoPage(w http.ResponseWriter, r *http.Request) {
	page := s.infoForPath(r.URL.Path)
	if page.Title == "" {
		s.errorResponse(w, r, "Page not found", http.StatusNotFound)
		return
	}
	if s.templates == nil {
		renderFallbackError(w, r, http.StatusInternalServerError)
		return
	}
	page.Theme = s.themeForRequest(r)
	page.Request = r
	page.AppName = s.appName()
	page.Lang = s.languageForRequest(w, r)
	page.LocalizedTitle = page.Title
	if page.TitleKey != "" {
		page.LocalizedTitle = i18n.Translate(page.Lang, page.TitleKey)
		if page.TitleKey == "about.title" {
			appName := strings.TrimSpace(s.config.Server.Branding.Title)
			if appName == "" {
				appName = "Countries API"
			}
			page.LocalizedTitle = strings.ReplaceAll(page.LocalizedTitle, "{app_name}", appName)
		}
	}
	if page.DescriptionKey != "" {
		page.Description = i18n.Translate(page.Lang, page.DescriptionKey)
	}
	for i := range page.Sections {
		section := &page.Sections[i]
		if section.TitleKey != "" {
			section.Title = i18n.Translate(page.Lang, section.TitleKey)
		}
		if section.TextKey != "" {
			section.Text = i18n.Translate(page.Lang, section.TextKey)
			appName := strings.TrimSpace(s.config.Server.Branding.Title)
			if appName == "" {
				appName = "Countries API"
			}
			section.Text = strings.ReplaceAll(section.Text, "{app_name}", appName)
		}
		for j, key := range section.BulletKeys {
			if j < len(section.Bullets) {
				section.Bullets[j] = i18n.Translate(page.Lang, key)
			}
		}
	}
	page.Dir = i18n.Direction(page.Lang)
	page.Path = r.URL.Path
	page.URLPrefix = s.requestURLPrefix(r)
	page.Languages = languageOptions()
	page.APIBasePath = page.URLPrefix + api.APIBasePath()
	page.CSRFToken = csrfToken(w, r, strings.HasPrefix(s.reportOrigin(r), "https://"))
	page.HasConsentCookie = s.hasConsentCookie(r)
	if page.Path == "/server/help" {
		page.HelpPages = s.registeredPages("/server/help")
	}
	if page.Path == "/server/help/mcp" {
		page.MCP = s.mcpHelpData(r)
	}
	if page.Path == "/server/privacy" {
		page.DPOName, page.DPOEmail = s.dpoContact()
		page.Privacy = s.config.Server.Privacy
		page.PrivacyContent = page.Privacy.BuiltInContent(page.Lang)
		page.TrackingType = s.configuredTrackingType()
		page.TrackingEnabled = s.trackingEnabled()
		if page.TrackingEnabled {
			page.TrackingTypeName = s.trackingTypeName(page.Lang, page.TrackingType)
			page.TrackingMessage = strings.ReplaceAll(i18n.Translate(page.Lang, "privacy.analytics_provider"), "{provider}", page.TrackingTypeName)
		}
		if cookie, err := r.Cookie("ccpa_opt_out"); err == nil && cookie.Value == "true" {
			page.CCPAOptedOut = true
		}
	}
	if page.Path == "/server/security" {
		if s.config.Server.Pages.Contact.Enabled {
			contactPath := s.requestURLPrefix(r) + "/server/contact"
			page.SecurityFormURL = contactPath + "?security_id=" + securityIDFor(s.contactKey, time.Now().Unix()/172800)
			page.ReportFormURL = contactPath
		}
		page.SecurityEmail = strings.TrimSpace(s.config.Server.Contact.Security.Email)
		page.SecurityExpires = time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339)
		if _, fingerprint, err := s.readSecurityPublicKey(); err == nil {
			page.SecurityPGPURL = s.BuildURL(r, "/.well-known/pgp-key.asc")
			page.SecurityFingerprint = fingerprint
		}
	}
	if page.Path == "/server/security/policy" {
		page.SecurityPolicyHTML = renderSecurityPolicyMarkdown(s.securityPolicy(r))
	}
	if page.Path == "/server/security/thanks" {
		entries, err := s.securityThanks()
		if err != nil {
			s.errorResponse(w, r, "Acknowledgments are unavailable", http.StatusInternalServerError)
			return
		}
		page.SecurityThanks = entries
	}
	s.renderTemplateResponse(w, r, "info.html", page, http.StatusOK)
}

func (s *Server) infoForPath(path string) infoPage {
	page := infoPage{Version: s.version, BuildDate: s.buildDate}
	appName := strings.TrimSpace(s.config.Server.Branding.Title)
	if appName == "" {
		appName = "Countries API"
	}
	switch path {
	case "/server/about":
		page.Title = "About " + appName
		page.TitleKey = "about.title"
		page.Tagline = strings.TrimSpace(s.config.Server.Branding.Tagline)
		page.Description = strings.TrimSpace(s.config.Server.Branding.Description)
		if page.Description == "" {
			page.DescriptionKey = "about.description"
			page.Description = i18n.Translate("en", page.DescriptionKey)
		}
		page.Sections = []infoSection{
			{ID: "about-description", Title: "About this service", TitleKey: "about.description_heading", Text: page.Description, TextKey: page.DescriptionKey},
			{ID: "about-service", Title: "What this service provides", TitleKey: "about.service_heading", Text: "Countries provides 249 embedded ISO 3166-1 records through public REST and GraphQL endpoints and server-rendered pages. Records include names, ISO codes, capitals, region, currencies, languages, population and observation year where available, area, flags, borders, time zones, top-level domains, and representative coordinates.", TextKey: "about.service_description"},
			{ID: "about-features", Title: "Features", TitleKey: "about.features_heading", Bullets: []string{"Search country names and filter by code, region, currency, or language", "Look up records by alpha-2, alpha-3, or numeric country code", "Find the nearest country centroid or countries within a radius", "Read country records through REST or GraphQL", "Use the countries-cli command-line client"}, BulletKeys: []string{"about.feature_search", "about.feature_lookup", "about.feature_nearest", "about.feature_api", "about.feature_cli"}},
			{ID: "about-attribution", Title: "Data attribution", TitleKey: "about.attribution_heading", Bullets: []string{"Country names, codes, capitals, regions, currencies, languages, area, coordinates, borders, and top-level domains use mledoze/countries data under the Open Database License 1.0 (ODbL).", "2024 population values are from the World Bank World Development Indicators, indicator SP.POP.TOTL, under CC BY 4.0.", "Time zone identifiers are from the IANA time zone database (tzdb) and are public domain.", "The application links to flag-icons SVG assets served by flagcdn.io; those assets are MIT licensed and are not embedded in the binary.", "Full source versions, field provenance, and license notices: https://github.com/apimgr/countries/blob/main/src/data/README.md"}, BulletKeys: []string{"about.attribution.country_data", "about.attribution.population", "about.attribution.timezones", "about.attribution.flags", "about.attribution.sources"}},
			{ID: "about-project", Title: "Project", TitleKey: "about.project_heading", Text: "Source code and project details are available at https://github.com/apimgr/countries.", TextKey: "about.project_description"},
		}
	case "/server/help":
		page.Title = "Countries Help"
		page.TitleKey = "help.title"
		page.Description = i18n.Translate("en", "help.description")
		page.DescriptionKey = "help.description"
		page.Sections = []infoSection{
			{ID: "help-search", Title: "Find countries", TitleKey: "help.find_countries", Text: "Search country names at /search or query the API with GET " + api.APIPath("countries/search") + "?q=united. Narrow the results with code, region, currency, or language filters. Look up a country by alpha-2, alpha-3, or numeric code at GET " + api.APIPath("countries/US") + ".", TextKey: "help.search_content"},
			{ID: "help-coordinates", Title: "Coordinates", TitleKey: "help.coordinates", Text: "Find the country with the nearest representative coordinate using GET " + api.APIPath("coordinates") + "?lat=40.7&lon=-74.0. Nearby results accept a radius in kilometers at GET " + api.APIPath("nearby") + "?lat=40.7&lon=-74.0&radius=500.", TextKey: "help.coordinates_content"},
			{ID: "help-api", Title: "API reference", TitleKey: "help.api_reference", Bullets: []string{"REST documentation: /server/docs/swagger (OpenAPI JSON: " + api.APIPath("server/swagger") + ")", "GraphQL explorer: /server/docs/graphql (POST queries to " + api.APIPath("server/graphql") + ")", "Dataset counts and version: GET " + api.APIPath("stats"), "Health status: /server/healthz"}, BulletKeys: []string{"help.rest_docs", "help.graphql_docs", "help.stats", "help.health"}},
			{ID: "help-cli", Title: "Command-line client", TitleKey: "help.cli", Text: "Use countries-cli list, get US, search united, nearest 40.7 -74.0, nearby 40.7 -74.0 500, or stats. Run countries-cli --help for the current flags and output formats.", TextKey: "help.cli_content"},
			{ID: "help-faq", Title: "Common questions", TitleKey: "help.faq", Bullets: []string{"The API is public and read-only; it does not require an API key.", "Country records are embedded in the server binary. The API does not fetch live population, exchange-rate, or political data.", "Population values include an observation year when available. Missing source observations are omitted rather than estimated.", "A 429 response means the deployment's request limit was reached. Wait for the Retry-After interval before retrying."}, BulletKeys: []string{"help.faq.no_auth", "help.faq.static_data", "help.faq.population", "help.faq.rate_limit"}},
			{ID: "help-configuration", Title: "Configuration", TitleKey: "help.configuration", Text: "Operators configure the server through server.yml. This service has no account system or web administration panel.", TextKey: "help.configuration_content"},
		}
	case "/server/help/mcp":
		page.Title = "MCP help"
		page.TitleKey = "help.mcp.title"
		page.Description = i18n.Translate("en", "help.mcp.description")
		page.DescriptionKey = "help.mcp.description"
	case "/server/privacy":
		page.Title = "Privacy"
		page.TitleKey = "privacy.title"
		page.Description = i18n.Translate("en", "privacy.description")
		page.DescriptionKey = "privacy.description"
	case "/server/terms":
		page.Title = "Terms of Service"
		page.TitleKey = "terms.title"
		page.Description = i18n.Translate("en", "terms.description")
		page.DescriptionKey = "terms.description"
		page.Sections = []infoSection{
			{ID: "terms-use", Title: "Acceptance", TitleKey: "terms.acceptance_title", Text: "Using " + appName + " means accepting these terms. Stop using the service if you do not agree.", TextKey: "terms.acceptance"},
			{ID: "terms-acceptable-use", Title: "Acceptable use", TitleKey: "terms.acceptable_use_title", Text: "Do not use the service unlawfully, abuse other people, or attempt to bypass rate limits or access controls. Security testing must follow the policy at /server/security.", TextKey: "terms.acceptable_use"},
			{ID: "terms-liability", Title: "Liability", TitleKey: "terms.liability_title", Text: "The service is provided as is, without warranty of any kind. To the extent the law allows, the operator is not liable for loss arising from its use.", TextKey: "terms.liability"},
			{ID: "terms-changes", Title: "Changes", TitleKey: "terms.changes_title", Text: "The terms may change. The current text on this page applies from the moment it is published.", TextKey: "terms.changes"},
			{ID: "terms-jurisdiction", Title: "Governing law", TitleKey: "terms.governing_law_title", Text: "The laws of the United States apply, without regard to conflict-of-law rules, unless the operator's own terms text names another jurisdiction.", TextKey: "terms.governing_law"},
		}
	case "/server/security":
		page.Title = "Security"
		page.TitleKey = "security.title"
		page.Description = i18n.Translate("en", "security.description")
		page.DescriptionKey = "security.description"
		page.Sections = []infoSection{
			{ID: "security-project-reporting", Title: "Report a project vulnerability", TitleKey: "security.project_reporting_title", Text: "Use GitHub private vulnerability reporting at https://github.com/apimgr/countries/security/advisories/new. Do not post vulnerability details in public issues.", TextKey: "security.project_reporting"},
			{ID: "security-deployment-reporting", Title: "Report an issue with this deployment", TitleKey: "security.deployment_reporting_title", Text: "This service is read-only and stores no user accounts. Contact the operator that runs this deployment for service-specific concerns. The project security.txt file identifies the upstream reporting policy.", TextKey: "security.deployment_reporting"},
			{ID: "security-scope", Title: "Scope", TitleKey: "security.scope_title", Text: "Country records are embedded static data. The application does not call external services to answer requests.", TextKey: "security.scope"},
		}
	case "/server/security/policy":
		page.Title = "Security Disclosure Policy"
		page.TitleKey = "server.page.security.policy_title"
		page.Description = i18n.Translate("en", "server.page.security.policy_description")
		page.DescriptionKey = "server.page.security.policy_description"
	case "/server/security/thanks":
		page.Title = "Security Acknowledgments"
		page.TitleKey = "server.page.security.thanks_title"
		page.Description = i18n.Translate("en", "server.page.security.thanks_description")
		page.DescriptionKey = "server.page.security.thanks_description"
	default:
		return page
	}
	return page
}

func (s *Server) localizedInfoPage(page infoPage, lang string) infoPage {
	page.Lang = lang
	page.LocalizedTitle = page.Title
	if page.TitleKey != "" {
		page.LocalizedTitle = i18n.Translate(lang, page.TitleKey)
		appName := s.appName()
		page.LocalizedTitle = strings.ReplaceAll(page.LocalizedTitle, "{app_name}", appName)
	}
	if page.DescriptionKey != "" {
		page.Description = i18n.Translate(lang, page.DescriptionKey)
	}
	for i := range page.Sections {
		section := &page.Sections[i]
		if section.TitleKey != "" {
			section.Title = i18n.Translate(lang, section.TitleKey)
		}
		if section.TextKey != "" {
			section.Text = i18n.Translate(lang, section.TextKey)
			section.Text = strings.ReplaceAll(section.Text, "{app_name}", s.appName())
		}
		for j, key := range section.BulletKeys {
			if j < len(section.Bullets) {
				section.Bullets[j] = i18n.Translate(lang, key)
			}
		}
	}
	return page
}

func (s *Server) handleInfoAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	path = strings.TrimPrefix(path, api.APIBasePath())
	if path == "/server/security" {
		s.jsonResponse(w, map[string]interface{}{"ok": true, "data": s.securityContactData(r)})
		return
	}
	if path == "/server/security/policy" {
		s.handleSecurityPolicyAPI(w, r)
		return
	}
	if path == "/server/security/thanks" {
		s.handleSecurityThanksAPI(w, r)
		return
	}
	if path == "/server/privacy" {
		privacy := s.config.Server.Privacy
		lang := s.languageForRequest(w, r)
		content := privacy.BuiltInContent(lang)
		trackingType := s.configuredTrackingType()
		trackingEnabled := s.trackingEnabled()
		trackingTypeName := ""
		if trackingEnabled {
			trackingTypeName = s.trackingTypeName(lang, trackingType)
		}
		userOptedOut := false
		if cookie, err := r.Cookie("ccpa_opt_out"); err == nil && cookie.Value == "true" {
			userOptedOut = true
		}
		s.jsonResponse(w, map[string]interface{}{
			"ok": true,
			"data": map[string]interface{}{
				"summary": map[string]interface{}{
					"data_stored_on_server": privacy.Data.StoredOnServer,
					"data_sold":             privacy.Data.Sold,
					"user_control":          true,
				},
				"cookies": map[string]interface{}{
					"essential":   map[string]interface{}{"enabled": privacy.Cookies.Essential.Enabled, "description": privacy.Cookies.Essential.Description},
					"preferences": map[string]interface{}{"enabled": privacy.Cookies.Preferences.Enabled, "description": privacy.Cookies.Preferences.Description},
					"analytics":   map[string]interface{}{"enabled": privacy.Cookies.Analytics.Enabled, "description": privacy.GetAnalyticsDescription()},
				},
				"data": map[string]interface{}{
					"sold":             privacy.Data.Sold,
					"stored_on_server": privacy.Data.StoredOnServer,
					"sharing":          privacy.Data.Sharing,
				},
				"tracking": map[string]interface{}{"enabled": trackingEnabled, "type": trackingType, "type_name": trackingTypeName},
				"retention": map[string]interface{}{
					"period":             privacy.Retention.Period,
					"export_available":   privacy.Retention.ExportAvailable,
					"deletion_available": privacy.Retention.DeletionAvailable,
				},
				"third_party": map[string]interface{}{"services": privacy.ThirdParty.Services},
				"ccpa": map[string]interface{}{
					"applicable":     privacy.IsCCPAApplicable(),
					"opt_out_url":    "/server/privacy#ccpa-opt-out",
					"user_opted_out": userOptedOut,
				},
				"content": map[string]interface{}{
					"consent_message": privacy.GetConsentMessage(),
					"data_collection": content.DataCollection,
					"data_usage":      privacy.GetDataUsageContent(lang),
					"data_security":   content.DataSecurity,
				},
			},
		})
		return
	}
	page := s.infoForPath(path)
	if page.Title == "" {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "Page not found")
		return
	}
	lang := s.languageForRequest(w, r)
	page = s.localizedInfoPage(page, lang)
	page.Version = s.version
	page.BuildDate = s.buildDate
	data := map[string]interface{}{
		"title": page.Title, "description": page.Description, "sections": page.Sections,
	}
	if path == "/server/help" {
		data["pages"] = s.registeredPagePayload("/server/help", lang)
	}
	s.jsonResponse(w, map[string]interface{}{
		"ok":   true,
		"data": data,
	})
}

func (s *Server) handleHealthPage(w http.ResponseWriter, r *http.Request) {
	s.writeHealthResult(w, r, healthFrontendFormat(r), true)
}

func (s *Server) handleGraphQLPage(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		renderFallbackError(w, r, http.StatusInternalServerError)
		return
	}
	query := "{ countries { name { common official } cca2 capital region currencies { code name symbol } } }"
	result := ""
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		if err := r.ParseForm(); err != nil {
			s.errorResponse(w, r, "Invalid GraphQL request", http.StatusBadRequest)
			return
		}
		query = strings.TrimSpace(r.FormValue("query"))
		if query == "" || len(query) > 32768 {
			s.errorResponse(w, r, "GraphQL query is required and must be at most 32768 bytes", http.StatusBadRequest)
			return
		}
		payload, err := json.Marshal(map[string]string{"query": query})
		if err != nil {
			s.errorResponse(w, r, "Unable to encode GraphQL query", http.StatusInternalServerError)
			return
		}
		request := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(payload)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		response := httptest.NewRecorder()
		if s.graphqlHandler == nil {
			s.errorResponse(w, r, "GraphQL is unavailable", http.StatusServiceUnavailable)
			return
		}
		s.graphqlHandler.ServeHTTP(response, request)
		result = response.Body.String()
	}
	data := s.pageContext(w, r, "/server/docs/graphql")
	data["GraphQLQuery"], data["GraphQLResult"] = query, result
	s.renderTemplateResponse(w, r, "graphql.html", data, http.StatusOK)
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
	spec, err := swagger.Spec(s.version, s.router)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to generate the API document")
		return
	}
	data, err := json.MarshalIndent(spec, "", "  ")
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
