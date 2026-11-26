package server

import (
	"encoding/json"
	"html"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/apimgr/countries/src/common/i18n"
)

type analyticsTracker struct {
	tags         string
	body         string
	noscript     string
	scriptHosts  []string
	connectHosts []string
	imageHosts   []string
}

func trackingString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func (s *Server) configuredTrackingType() string {
	typeName := strings.ToLower(strings.TrimSpace(s.config.Server.Tracking.Type))
	if typeName == "none" {
		return ""
	}
	return typeName
}

func (s *Server) analyticsTracker() (analyticsTracker, bool) {
	cfg := s.config.Server.Tracking
	cfg.Type = s.configuredTrackingType()
	if cfg.Type == "" {
		return analyticsTracker{}, false
	}
	id := html.EscapeString(cfg.ID)
	base := strings.TrimSuffix(cfg.URL, "/")
	var tracker analyticsTracker
	switch cfg.Type {
	case "google":
		if strings.HasPrefix(cfg.ID, "G-") {
			tracker.tags = `<script async src="https://www.googletagmanager.com/gtag/js?id=` + url.QueryEscape(cfg.ID) + `"></script>`
			tracker.body = "window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments);}" +
				"gtag('js',new Date());gtag('config'," + trackingString(cfg.ID) + ");"
			tracker.scriptHosts = []string{"https://www.googletagmanager.com"}
			tracker.connectHosts = []string{"https://www.google-analytics.com"}
		} else {
			tracker.tags = `<script async src="https://www.google-analytics.com/analytics.js"></script>`
			tracker.body = "window.ga=window.ga||function(){(ga.q=ga.q||[]).push(arguments)};ga.l=+new Date;" +
				"ga('create'," + trackingString(cfg.ID) + ",'auto');ga('send','pageview');"
			tracker.scriptHosts = []string{"https://www.google-analytics.com"}
			tracker.connectHosts = []string{"https://www.google-analytics.com"}
		}
	case "matomo", "piwik":
		tracker.body = "var _paq=window._paq=window._paq||[];_paq.push(['trackPageView']);" +
			"_paq.push(['enableLinkTracking']);_paq.push(['setTrackerUrl'," + trackingString(base+"/matomo.php") + "]);" +
			"_paq.push(['setSiteId'," + trackingString(cfg.ID) + "]);var g=document.createElement('script');" +
			"g.async=true;g.src=" + trackingString(base+"/matomo.js") + ";document.head.appendChild(g);"
		tracker.scriptHosts, tracker.connectHosts = []string{trackingOrigin(cfg.URL)}, []string{trackingOrigin(cfg.URL)}
	case "owa":
		tracker.body = "var owa_baseUrl=" + trackingString(base+"/") + ";var owa_cmds=window.owa_cmds=window.owa_cmds||[];" +
			"owa_cmds.push(['setSiteId'," + trackingString(cfg.ID) + "]);owa_cmds.push(['trackPageView']);owa_cmds.push(['trackClicks']);" +
			"var o=document.createElement('script');o.async=true;o.src=owa_baseUrl+'modules/base/js/owa.tracker-combined-min.js';document.head.appendChild(o);"
		tracker.scriptHosts, tracker.connectHosts = []string{trackingOrigin(cfg.URL)}, []string{trackingOrigin(cfg.URL)}
	case "fathom":
		source := "https://cdn.usefathom.com/script.js"
		if cfg.URL != "" {
			source = base + "/tracker.js"
		}
		tracker.tags = `<script src="` + html.EscapeString(source) + `" data-site="` + id + `" defer></script>`
		tracker.scriptHosts = []string{trackingOrigin(source)}
		tracker.connectHosts = []string{trackingOrigin(source)}
	case "plausible":
		source := "https://plausible.io/js/script.js"
		if cfg.URL != "" {
			source = base + "/js/script.js"
		}
		tracker.tags = `<script defer data-domain="` + id + `" src="` + html.EscapeString(source) + `"></script>`
		tracker.scriptHosts = []string{trackingOrigin(source)}
		tracker.connectHosts = []string{trackingOrigin(source)}
	case "umami":
		source := base + "/script.js"
		tracker.tags = `<script defer src="` + html.EscapeString(source) + `" data-website-id="` + id + `"></script>`
		tracker.scriptHosts = []string{trackingOrigin(source)}
		tracker.connectHosts = []string{trackingOrigin(source)}
	case "simple":
		tracker.tags = `<script async defer src="https://scripts.simpleanalyticscdn.com/latest.js"></script>`
		tracker.noscript = `<noscript><img src="https://queue.simpleanalyticscdn.com/noscript.gif" alt="" referrerpolicy="no-referrer-when-downgrade" /></noscript>`
		tracker.scriptHosts = []string{"https://scripts.simpleanalyticscdn.com"}
		tracker.connectHosts = []string{"https://queue.simpleanalyticscdn.com"}
		tracker.imageHosts = []string{"https://queue.simpleanalyticscdn.com"}
	case "cloudflare":
		tracker.tags = `<script defer src="https://static.cloudflareinsights.com/beacon.min.js" data-cf-beacon='{"token": "` + id + `"}'></script>`
		tracker.scriptHosts = []string{"https://static.cloudflareinsights.com"}
		tracker.connectHosts = []string{"https://cloudflareinsights.com"}
	default:
		return analyticsTracker{}, false
	}
	if tracker.body != "" {
		tracker.tags += `<script async src="/server/tracking.js"></script>`
	}
	return tracker, true
}

func trackingOrigin(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func (s *Server) trackingAllowed(r *http.Request) bool {
	if requestPrivacySignal(r) != "" || !s.config.Server.Privacy.Cookies.Analytics.Enabled || isOverlayRequest(s, r) {
		return false
	}
	cookie, err := r.Cookie("cookie_consent")
	if err != nil {
		return false
	}
	value, err := url.PathUnescape(cookie.Value)
	if err != nil {
		return false
	}
	var consent struct {
		Analytics bool `json:"analytics"`
	}
	return json.Unmarshal([]byte(value), &consent) == nil && consent.Analytics
}

type privacySignalContextKey struct{}

func requestPrivacySignal(r *http.Request) string {
	signal, _ := r.Context().Value(privacySignalContextKey{}).(string)
	return signal
}

func (s *Server) trackingEnabled() bool {
	return s.configuredTrackingType() != "" && s.config.Server.Privacy.Cookies.Analytics.Enabled
}

func (s *Server) trackingTypeName(language, trackingType string) string {
	if trackingType == "" {
		return ""
	}
	return i18n.Translate(language, "tracking.provider."+trackingType)
}

func isOverlayRequest(s *Server, r *http.Request) bool {
	host := strings.ToLower(strings.TrimSpace(r.Host))
	if s.isTrustedPeer(r) {
		if forwarded := firstHeaderValue(r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Real-Host"), r.Header.Get("X-Original-Host")); forwarded != "" {
			host = strings.ToLower(forwarded)
		}
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	host = strings.TrimSuffix(host, ".")
	return strings.HasSuffix(host, ".onion") || strings.HasSuffix(host, ".i2p")
}

func (s *Server) trackingScript(r *http.Request) template.HTML {
	tracker, ok := s.analyticsTracker()
	if !ok || !s.trackingAllowed(r) {
		return ""
	}
	scriptURL := html.EscapeString(strings.TrimSuffix(s.requestURLPrefix(r), "/") + "/server/tracking.js")
	tags := strings.ReplaceAll(tracker.tags, `src="/server/tracking.js"`, `src="`+scriptURL+`"`)
	return template.HTML(tags)
}

func (s *Server) trackingNoscript(r *http.Request) template.HTML {
	tracker, ok := s.analyticsTracker()
	if !ok || !s.trackingAllowed(r) {
		return ""
	}
	return template.HTML(tracker.noscript)
}

func (s *Server) handleTrackingScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Add("Vary", "Cookie")
	if !s.trackingAllowed(r) {
		http.NotFound(w, r)
		return
	}
	tracker, ok := s.analyticsTracker()
	if !ok || tracker.body == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write([]byte(tracker.body))
}
