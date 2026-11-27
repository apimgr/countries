package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/config"
)

func TestMarkdownToHTMLSanitizesUnsafeContent(t *testing.T) {
	got := string(markdownToHTML("## Safe\n\n<script>alert(1)</script><img src=x onerror=alert(1)>\n\n[bad](javascript:alert(1)) [good](/server/about)"))
	for _, forbidden := range []string{"<script", "onerror", "javascript:"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Errorf("rendered markdown contains %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(got, "<h2>Safe</h2>") || !strings.Contains(got, `href="/server/about"`) {
		t.Fatalf("rendered markdown lost safe content: %s", got)
	}
}

func TestMarkdownTopLevelHeadingRendersAsH2(t *testing.T) {
	got := string(markdownToHTML("# Heading"))
	if strings.Contains(got, "<h1") || !strings.Contains(got, "<h2>Heading</h2>") {
		t.Fatalf("top-level markdown heading = %q, want h2 and no h1", got)
	}
}

func TestSanitizePageHTMLLimitsDataURLsToRasterImages(t *testing.T) {
	got := sanitizePageHTML(`<img src="data:image/png;base64,AAAA"><img src="data:image/svg+xml,%3Csvg%3E"><a href="data:text/html,hello">unsafe</a>`, richHTMLPolicy)
	if !strings.Contains(got, `src="data:image/png;base64,AAAA"`) {
		t.Fatalf("safe raster image was removed: %s", got)
	}
	if strings.Contains(got, "data:image/svg+xml") || strings.Contains(got, "href=\"data:") {
		t.Fatalf("unsafe data URL survived sanitization: %s", got)
	}
}

func TestSecurityPolicyMarkdownUsesNestedHeadingLevels(t *testing.T) {
	got := string(renderSecurityPolicyMarkdown("## Scope\nContent.\n\n### Boundaries\nMore content."))
	if !strings.Contains(got, `<h3>Scope</h3>`) || !strings.Contains(got, `<h4>Boundaries</h4>`) {
		t.Fatalf("security policy headings = %q, want headings nested below the card heading", got)
	}
}

func TestOperatorHTMLResolvesLanguageAndRejectsContactFormLinks(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Content["about"] = config.PageContentConfig{
		Format: "md",
		Body:   map[string]any{"default": "Default", "fr": "Bonjour [contact](/server/contact) [security](/server/security)"},
	}
	server := &Server{config: cfg, version: "1.2.3", buildDate: "2026-01-02T03:04:05Z"}
	request := httptest.NewRequest("GET", "/server/about?lang=fr", nil)
	got := string(server.operatorHTML(request, "about"))
	if !strings.Contains(got, "Bonjour") || !strings.Contains(got, `href="/server/security"`) {
		t.Fatalf("localized content or safe link is missing: %s", got)
	}
	if strings.Contains(got, "/server/contact") {
		t.Fatalf("operator content links to the contact form: %s", got)
	}
}

func TestOperatorHTMLUsesDefaultLanguageAndSubstitutesVariables(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Content["terms"] = config.PageContentConfig{
		Format: "txt",
		Body:   map[string]any{"default": "{app_name} {project_version} {current_year}", "fr": "Conditions"},
	}
	server := &Server{config: cfg, version: "2.4.0", buildDate: "unknown"}
	request := httptest.NewRequest("GET", "/server/terms?lang=de-CH", nil)
	got := string(server.operatorHTML(request, "terms"))
	if !strings.Contains(got, "Countries API 2.4.0") || !strings.Contains(got, "2026") {
		t.Fatalf("default language/variable substitution result = %s", got)
	}
}

func TestOperatorHTMLNeverEmitsH1(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Content["about"] = config.PageContentConfig{Format: "md", Body: map[string]any{"default": "# Custom heading"}}
	server := &Server{config: cfg, version: "1.2.3", buildDate: "2026-01-02T03:04:05Z"}
	got := string(server.operatorHTML(httptest.NewRequest("GET", "/server/about", nil), "about"))
	if strings.Contains(got, "<h1") || !strings.Contains(got, "<h3>Custom heading</h3>") {
		t.Fatalf("operator markdown heading = %q, want h3 and no h1", got)
	}
	cfg.Server.Branding.Content["about"] = config.PageContentConfig{Format: "html", Body: map[string]any{"default": "<h1>Unsafe heading</h1>"}}
	if got = string(server.operatorHTML(httptest.NewRequest("GET", "/server/about", nil), "about")); strings.Contains(got, "<h1") || !strings.Contains(got, "<h3>Unsafe heading</h3>") {
		t.Fatalf("operator HTML heading was not shifted: %q", got)
	}
}

func TestOperatorContentHeadingsShiftTwoLevelsAndCapAtH6(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Content["about"] = config.PageContentConfig{Format: "md", Body: map[string]any{"default": "# One\n\n## Two\n\n#### Four\n\n###### Six"}}
	server := &Server{config: cfg}
	got := string(server.operatorHTML(httptest.NewRequest("GET", "/server/about", nil), "about"))
	for _, heading := range []string{"<h3>One</h3>", "<h4>Two</h4>", "<h6>Four</h6>", "<h6>Six</h6>"} {
		if !strings.Contains(got, heading) {
			t.Errorf("operator content is missing shifted heading %q: %s", heading, got)
		}
	}
}

func TestLocalizedPageBodyRequiresStringForSelectedLanguage(t *testing.T) {
	body, ok := localizedPageBody(map[string]any{"fr": 123}, "fr")
	if ok || body != "" {
		t.Fatalf("localizedPageBody() = (%q, %t), want (empty, false)", body, ok)
	}
	body, ok = localizedPageBody(map[string]any{"default": "Fallback"}, "fr")
	if !ok || body != "Fallback" {
		t.Fatalf("localizedPageBody() = (%q, %t), want default fallback", body, ok)
	}
	body, ok = localizedPageBody(map[string]any{"fr": "Français", "default": "Fallback", "en": "English"}, "fr")
	if !ok || body != "Français" {
		t.Fatalf("localizedPageBody() = (%q, %t), want exact language match", body, ok)
	}
	body, ok = localizedPageBody(map[string]any{"fr": "Français", "default": "Fallback", "en": "English"}, "es")
	if !ok || body != "Fallback" {
		t.Fatalf("localizedPageBody() = (%q, %t), want default before first entry", body, ok)
	}
	body, ok = localizedPageBody(map[string]any{"zh": "Chinese", "de": "German"}, "es")
	if ok || body != "" {
		t.Fatalf("localizedPageBody() = (%q, %t), want no first-entry fallback", body, ok)
	}
	body, ok = localizedPageBody("One string for every language", "fr")
	if !ok || body != "One string for every language" {
		t.Fatalf("localizedPageBody() = (%q, %t), want scalar string compatibility", body, ok)
	}
}

func TestBrandingUsesBuiltInPageWhenNoLanguageOrDefaultEntryExists(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Content["about"] = config.PageContentConfig{
		Format: "md",
		Body:   map[string]any{"zh": "Chinese override", "de": "German override"},
	}
	server := newTestServer(t)
	server.config.Server.Branding.Content["about"] = cfg.Server.Branding.Content["about"]
	request := httptest.NewRequest("GET", "/server/about?lang=es", nil)
	if content := string(server.operatorHTML(request, "about")); content != "" {
		t.Fatalf("operatorHTML() = %q, want empty so the built-in page remains visible", content)
	}
	response := httptest.NewRecorder()
	server.handleInfoPage(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Countries API") || strings.Contains(response.Body.String(), "override") {
		t.Fatalf("branding fallback page status/content = %d/%q, want built-in content without unmatched overrides", response.Code, response.Body.String())
	}
}
