package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	sharedformat "github.com/apimgr/countries/src/common/format"
	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/mode"
)

type HealthResponse struct {
	Project        HealthProjectInfo `json:"project"`
	Status         string            `json:"status"`
	PendingRestart bool              `json:"pending_restart,omitempty"`
	RestartReason  []string          `json:"restart_reason,omitempty"`
	Version        string            `json:"version"`
	GoVersion      string            `json:"go_version"`
	Build          HealthBuildInfo   `json:"build"`
	Uptime         string            `json:"uptime"`
	Mode           string            `json:"mode"`
	Timestamp      time.Time         `json:"timestamp"`
	Cluster        HealthClusterInfo `json:"cluster"`
	Features       HealthFeatures    `json:"features"`
	Checks         HealthChecks      `json:"checks"`
	Stats          HealthStats       `json:"stats"`
}

type HealthProjectInfo struct {
	Name        string `json:"name"`
	Tagline     string `json:"tagline"`
	Description string `json:"description"`
}

type HealthBuildInfo struct {
	Commit string `json:"commit"`
	Date   string `json:"date"`
}

type HealthClusterInfo struct {
	Enabled bool `json:"enabled"`
}

type HealthFeatures struct {
	Tor   HealthOverlayInfo `json:"tor"`
	I2P   HealthI2PInfo     `json:"i2p"`
	GeoIP bool              `json:"geoip"`
}

type HealthOverlayInfo struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Status   string `json:"status"`
	Hostname string `json:"hostname"`
}

type HealthI2PInfo struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Status   string `json:"status"`
	State    string `json:"state"`
	Hostname string `json:"hostname"`
	Provider string `json:"provider"`
}

type HealthChecks struct {
	Database  string
	Cache     string
	Disk      string
	Scheduler string
	Tor       string
	I2P       string
	Datasets  []HealthCheck
}

type HealthCheck struct {
	Key      string
	Status   string
	Critical bool
}

type HealthStats struct {
	RequestsTotal int64             `json:"requests_total"`
	Requests24h   int64             `json:"requests_24h"`
	ActiveConns   int64             `json:"active_connections"`
	Project       []HealthStatValue `json:"-"`
}

type HealthStatValue struct {
	Key   string
	Value int64
}

func (s HealthStats) MarshalJSON() ([]byte, error) {
	var output bytes.Buffer
	output.WriteByte('{')
	first := true
	for _, field := range []struct {
		key   string
		value int64
	}{{"requests_total", s.RequestsTotal}, {"requests_24h", s.Requests24h}, {"active_connections", s.ActiveConns}} {
		if err := writeHealthJSONIntField(&output, &first, field.key, field.value); err != nil {
			return nil, err
		}
	}
	for _, field := range s.Project {
		if err := writeHealthJSONIntField(&output, &first, field.Key, field.Value); err != nil {
			return nil, err
		}
	}
	output.WriteByte('}')
	return output.Bytes(), nil
}

func writeHealthJSONIntField(output *bytes.Buffer, first *bool, key string, value int64) error {
	if !*first {
		output.WriteByte(',')
	}
	*first = false
	encodedKey, err := json.Marshal(key)
	if err != nil {
		return err
	}
	output.Write(encodedKey)
	output.WriteByte(':')
	if value < 0 {
		value = 0
	}
	output.WriteString(strconv.FormatInt(value, 10))
	return nil
}

type healthTemplateData struct {
	Response               HealthResponse
	Title                  string
	StatusClass            string
	StatusIcon             string
	StatusText             string
	RestartNotice          string
	RestartReasons         []string
	UptimeText             string
	TimestampText          string
	BuildDateText          string
	ClusterText            string
	VersionLabel           string
	GoVersionLabel         string
	BuildLabel             string
	UptimeLabel            string
	ModeLabel              string
	ModeText               string
	TimestampLabel         string
	ClusterLabel           string
	RuntimeLabel           string
	FeaturesLabel          string
	ChecksLabel            string
	StatsLabel             string
	TorLabel               string
	I2PLabel               string
	GeoIPLabel             string
	TorStatus              string
	I2PStatus              string
	GeoIPStatus            string
	DatabaseLabel          string
	CacheLabel             string
	DiskLabel              string
	SchedulerLabel         string
	StatusLabel            string
	CheckRows              []healthTemplateRow
	RequestTotalLabel      string
	Requests24hLabel       string
	ActiveConnectionsLabel string
	RequestTotalText       string
	Requests24hText        string
	ActiveConnectionsText  string
	ProjectStats           []healthStatTemplateRow
}

type healthStatTemplateRow struct {
	Label string
	Value string
}

type healthTemplateRow struct {
	Key      string
	Label    string
	Status   string
	CSSClass string
}

func newHealthTemplateData(response HealthResponse, lang string) healthTemplateData {
	labels := func(key, fallback string) string { return healthLabel(lang, key, fallback) }
	data := healthTemplateData{
		Response:               response,
		Title:                  response.Project.Name + " - " + labels("health.title", "Health Status"),
		StatusClass:            healthStatusClass(response.Status),
		StatusIcon:             healthStatusIcon(response.Status),
		StatusText:             healthStatusText(lang, response.Status),
		RestartNotice:          healthLabel(lang, "health.pending_restart", "A restart is required for the changed settings."),
		UptimeText:             humanHealthUptimeForLang(response.Uptime, lang),
		TimestampText:          humanHealthTimestamp(response.Timestamp),
		ClusterText:            localizedEnabled(lang, response.Cluster.Enabled),
		ModeText:               healthLabel(lang, "health.mode."+response.Mode, humanizeHealthWord(response.Mode)),
		VersionLabel:           labels("health.label.version", "Version"),
		GoVersionLabel:         labels("health.label.go_version", "Go version"),
		BuildLabel:             labels("health.label.build", "Build"),
		UptimeLabel:            labels("health.label.uptime", "Uptime"),
		ModeLabel:              labels("health.label.mode", "Mode"),
		TimestampLabel:         labels("health.label.checked", "Checked"),
		ClusterLabel:           labels("health.section.cluster", "Cluster"),
		RuntimeLabel:           labels("health.section.runtime", "Runtime"),
		FeaturesLabel:          labels("health.section.features", "Features"),
		ChecksLabel:            labels("health.section.checks", "Component Status"),
		StatsLabel:             labels("health.section.stats", "Server Statistics"),
		TorLabel:               labels("health.feature.tor", "Tor"),
		I2PLabel:               labels("health.feature.i2p", "I2P"),
		GeoIPLabel:             labels("health.feature.geoip", "GeoIP"),
		TorStatus:              healthLabel(lang, "health.overlay."+response.Features.Tor.Status, response.Features.Tor.Status),
		I2PStatus:              healthLabel(lang, "health.overlay."+response.Features.I2P.Status, response.Features.I2P.Status),
		GeoIPStatus:            localizedEnabled(lang, response.Features.GeoIP),
		DatabaseLabel:          labels("health.check.database", "Database"),
		CacheLabel:             labels("health.check.cache", "Cache"),
		DiskLabel:              labels("health.check.disk", "Disk"),
		SchedulerLabel:         labels("health.check.scheduler", "Scheduler"),
		StatusLabel:            labels("health.label.status", "Status"),
		RequestTotalLabel:      labels("health.stat.requests_total", "Total requests"),
		Requests24hLabel:       labels("health.stat.requests_24h", "Requests (24 hours)"),
		ActiveConnectionsLabel: labels("health.stat.active_connections", "Active connections"),
		RequestTotalText:       sharedformat.Count(response.Stats.RequestsTotal, lang),
		Requests24hText:        sharedformat.Count(response.Stats.Requests24h, lang),
		ActiveConnectionsText:  sharedformat.Count(response.Stats.ActiveConns, lang),
	}
	for _, reason := range response.RestartReason {
		reason = strings.NewReplacer(".", " ", "_", " ").Replace(reason)
		data.RestartReasons = append(data.RestartReasons, humanizeHealthWord(reason))
	}
	for _, stat := range response.Stats.Project {
		data.ProjectStats = append(data.ProjectStats, healthStatTemplateRow{
			Label: healthLabel(lang, "health.stat."+stat.Key, strings.ReplaceAll(stat.Key, "_", " ")),
			Value: sharedformat.Count(stat.Value, lang),
		})
	}
	data.BuildDateText = response.Build.Date
	if parsed, err := time.Parse(time.RFC3339, response.Build.Date); err == nil {
		data.BuildDateText = humanHealthTimestamp(parsed)
	}
	for _, check := range response.Checks.ordered() {
		data.CheckRows = append(data.CheckRows, healthTemplateRow{
			Key:      check.Key,
			Label:    healthCheckLabel(lang, check.Key),
			Status:   healthCheckStatusText(lang, check.Status),
			CSSClass: healthCheckClass(check.Status),
		})
	}
	return data
}

func healthStatusClass(status string) string {
	switch status {
	case "healthy":
		return "status-ok"
	case "degraded", "restart_required":
		return "status-warning"
	default:
		return "status-error"
	}
}

func healthStatusIcon(status string) string {
	switch status {
	case "healthy":
		return "✅"
	case "degraded":
		return "⚠️"
	case "restart_required":
		return "🔄"
	case "maintenance":
		return "🚧"
	case "shutting_down":
		return "🛑"
	default:
		return "❌"
	}
}

func healthCheckClass(status string) string {
	switch status {
	case "ok":
		return "status-ok"
	case "degraded":
		return "status-warning"
	default:
		return "status-error"
	}
}

func (c HealthChecks) MarshalJSON() ([]byte, error) {
	var output bytes.Buffer
	output.WriteByte('{')
	fields := []struct {
		key   string
		value string
	}{
		{key: "database", value: c.Database},
		{key: "cache", value: c.Cache},
		{key: "disk", value: c.Disk},
		{key: "scheduler", value: c.Scheduler},
		{key: "tor", value: c.Tor},
		{key: "i2p", value: c.I2P},
	}
	first := true
	for _, field := range fields {
		if field.value == "" {
			continue
		}
		if err := writeHealthJSONField(&output, &first, field.key, field.value); err != nil {
			return nil, err
		}
	}
	for _, check := range c.Datasets {
		if err := writeHealthJSONField(&output, &first, check.Key, check.Status); err != nil {
			return nil, err
		}
	}
	output.WriteByte('}')
	return output.Bytes(), nil
}

func writeHealthJSONField(output *bytes.Buffer, first *bool, key, value string) error {
	if !*first {
		output.WriteByte(',')
	}
	*first = false
	encodedKey, err := json.Marshal(key)
	if err != nil {
		return err
	}
	encodedValue, err := json.Marshal(value)
	if err != nil {
		return err
	}
	output.Write(encodedKey)
	output.WriteByte(':')
	output.Write(encodedValue)
	return nil
}

type healthHourBucket struct {
	Hour  int64
	Count int64
}

type healthCounters struct {
	mu      sync.Mutex
	total   int64
	active  int64
	buckets [24]healthHourBucket
}

func newHealthCounters() *healthCounters {
	return &healthCounters{}
}

func (c *healthCounters) enter() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.active++
	c.mu.Unlock()
}

func (c *healthCounters) leave(path string, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active--
	if isHealthProbePath(path) || path == "/server/metrics" || strings.HasPrefix(path, "/server/metrics/") {
		return
	}
	c.total++
	hour := now.UTC().Unix() / 3600
	index := hour % int64(len(c.buckets))
	if c.buckets[index].Hour != hour {
		c.buckets[index] = healthHourBucket{Hour: hour}
	}
	c.buckets[index].Count++
}

func (c *healthCounters) snapshot(now time.Time) HealthStats {
	if c == nil {
		return HealthStats{ActiveConns: 1}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	hour := now.UTC().Unix() / 3600
	var recent int64
	for _, bucket := range c.buckets {
		if bucket.Hour >= hour-23 && bucket.Hour <= hour {
			recent += bucket.Count
		}
	}
	active := c.active
	if active < 1 {
		active = 1
	}
	return HealthStats{RequestsTotal: c.total, Requests24h: recent, ActiveConns: active}
}

func (s *Server) healthStatsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.healthStore != nil {
			s.healthStore.EnterRequest()
			defer func() {
				if !isHealthProbePath(r.URL.Path) && r.URL.Path != "/server/metrics" && !strings.HasPrefix(r.URL.Path, "/server/metrics/") {
					s.healthStore.RecordRequest(time.Now())
				}
				s.healthStore.LeaveRequest()
			}()
			next.ServeHTTP(w, r)
			return
		}
		if s.healthStats == nil {
			s.healthStats = newHealthCounters()
		}
		counters := s.healthStats
		counters.enter()
		defer counters.leave(r.URL.Path, time.Now())
		next.ServeHTTP(w, r)
	})
}

func isHealthProbePath(path string) bool {
	path = strings.TrimSuffix(path, ".txt")
	return path == "/server/healthz" || path == "/healthz" || path == "/api/healthz" || strings.HasSuffix(path, "/server/healthz")
}

func (s *Server) buildHealthResponse() HealthResponse {
	now := time.Now().UTC().Truncate(time.Second)
	started := s.startedAt
	if started.IsZero() || started.After(now) {
		started = now
	}
	databaseStatus, cacheStatus, diskStatus, schedulerStatus := "error", "ok", "ok", "ok"
	datasets := make([]HealthCheck, 0)
	if s.healthStore != nil {
		for _, registered := range s.healthStore.Checks() {
			check := HealthCheck{Key: registered.Key, Status: registered.Status, Critical: registered.Critical}
			switch registered.Key {
			case "database":
				databaseStatus = registered.Status
			case "cache":
				cacheStatus = registered.Status
			case "disk":
				diskStatus = registered.Status
			case "scheduler":
				schedulerStatus = registered.Status
			default:
				datasets = append(datasets, check)
			}
		}
	} else if s.db != nil {
		databaseStatus = "ok"
	}
	commit := strings.TrimSpace(s.commit)
	if len(commit) > 7 {
		commit = commit[:7]
	}
	buildDate := healthBuildTimestamp(s.buildDate)
	version := strings.TrimSpace(s.version)
	if version == "" || version == "devel" {
		version = commit
	}
	if version == "" {
		version = "dev"
	}
	modeName := string(mode.Get())
	if modeName != "production" && modeName != "development" && modeName != "debug" {
		modeName = "production"
	}
	pendingRestart := s.pendingRestart.Load()
	s.healthStateMu.RLock()
	restartReasons := append([]string(nil), s.restartReasons...)
	s.healthStateMu.RUnlock()
	s.overlayMu.RLock()
	torInfo, i2pInfo := s.torHealth, s.i2pHealth
	s.overlayMu.RUnlock()
	if torInfo.Status == "" {
		torInfo = HealthOverlayInfo{Status: "disabled"}
	}
	if i2pInfo.Status == "" {
		i2pInfo = HealthI2PInfo{Status: "disabled", State: "disabled", Provider: "none"}
	}
	torCheck, i2pCheck := "", ""
	if torInfo.Enabled {
		torCheck = "ok"
		if torInfo.Status == "unavailable" {
			torCheck = "degraded"
		}
	}
	if i2pInfo.Enabled {
		i2pCheck = "ok"
		if i2pInfo.Status == "unavailable" {
			i2pCheck = "degraded"
		}
	}
	status := overallHealthStatus(s.shuttingDown.Load(), s.maintenance.Load(), databaseStatus, cacheStatus, diskStatus, schedulerStatus, datasets, pendingRestart)
	if status == "healthy" || status == "restart_required" {
		if torCheck == "degraded" || i2pCheck == "degraded" {
			status = "degraded"
		}
	}
	checks := HealthChecks{Database: databaseStatus, Cache: cacheStatus, Disk: diskStatus, Scheduler: schedulerStatus, Tor: torCheck, I2P: i2pCheck, Datasets: datasets}
	stats := HealthStats{ActiveConns: 1}
	if s.healthStore != nil {
		stored := s.healthStore.Stats(now)
		stats = HealthStats{RequestsTotal: stored.RequestsTotal, Requests24h: stored.Requests24h, ActiveConns: stored.ActiveConns}
		for _, stat := range stored.Project {
			stats.Project = append(stats.Project, HealthStatValue{Key: stat.Key, Value: stat.Value})
		}
	} else if s.healthStats != nil {
		stats = s.healthStats.snapshot(now)
	}
	return HealthResponse{
		Project: HealthProjectInfo{Name: s.config.Server.Branding.Title, Tagline: s.config.Server.Branding.Tagline, Description: s.config.Server.Branding.Description},
		Status:  status, PendingRestart: pendingRestart, RestartReason: restartReasons, Version: version, GoVersion: runtime.Version(),
		Build: HealthBuildInfo{Commit: commit, Date: buildDate}, Uptime: compactHealthUptime(now.Sub(started)),
		Mode: modeName, Timestamp: now, Cluster: HealthClusterInfo{Enabled: false},
		Features: HealthFeatures{
			Tor:   torInfo,
			I2P:   i2pInfo,
			GeoIP: s.config.Server.GeoIP.Enabled,
		},
		Checks: checks, Stats: stats,
	}
}

func overallHealthStatus(shuttingDown, maintenance bool, database, cache, disk, scheduler string, datasets []HealthCheck, pendingRestart bool) string {
	if shuttingDown {
		return "shutting_down"
	}
	if maintenance {
		return "maintenance"
	}
	if database == "error" || disk == "error" {
		return "unhealthy"
	}
	degraded := cache != "ok" || disk != "ok" || scheduler != "ok"
	for _, check := range datasets {
		if check.Status == "ok" {
			continue
		}
		if check.Critical {
			return "unhealthy"
		}
		degraded = true
	}
	if degraded {
		return "degraded"
	}
	if pendingRestart {
		return "restart_required"
	}
	return "healthy"
}

func healthBuildTimestamp(value string) string {
	value = strings.TrimSpace(value)
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		parsed, err = time.Parse("2006-01-02", value)
		if err != nil {
			return ""
		}
	}
	return parsed.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func compactHealthUptime(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	seconds := int64(duration.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	parts := make([]string, 0, 3)
	if days := minutes / (24 * 60); days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
		minutes %= 24 * 60
	}
	if hours := minutes / 60; hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
		minutes %= 60
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	return strings.Join(parts, " ")
}

func humanHealthUptime(value string) string {
	return humanHealthUptimeForLang(value, "en")
}

func humanHealthUptimeForLang(value, lang string) string {
	var duration time.Duration
	for _, unit := range strings.Fields(value) {
		if len(unit) < 2 {
			return value
		}
		amount, err := strconv.Atoi(unit[:len(unit)-1])
		if err != nil {
			return value
		}
		multiplier := time.Duration(0)
		switch unit[len(unit)-1] {
		case 'd':
			multiplier = 24 * time.Hour
		case 'h':
			multiplier = time.Hour
		case 'm':
			multiplier = time.Minute
		case 's':
			multiplier = time.Second
		default:
			return value
		}
		if amount < 0 || time.Duration(amount) > (time.Duration(1<<63-1)-duration)/multiplier {
			return value
		}
		duration += time.Duration(amount) * multiplier
	}
	return sharedformat.Duration(duration, lang, i18n.TranslatePlural)
}

func humanHealthTimestamp(value time.Time) string {
	return value.UTC().Format("January 2, 2006 at 15:04:05 MST")
}

func healthResponseStatusCode(status string) int {
	switch status {
	case "unhealthy", "maintenance", "shutting_down":
		return http.StatusServiceUnavailable
	default:
		return http.StatusOK
	}
}

func healthAPIFormat(r *http.Request) string {
	if strings.HasSuffix(r.URL.Path, ".txt") {
		return "text"
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/json") {
		return "json"
	}
	if strings.Contains(accept, "text/plain") {
		return "text"
	}
	if nonInteractiveHealthClient(r) {
		return "text"
	}
	return "json"
}

func healthFrontendFormat(r *http.Request) string {
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/json") {
		return "json"
	}
	if strings.Contains(accept, "text/html") {
		return "html"
	}
	if strings.Contains(accept, "text/plain") {
		return "text"
	}
	if acceptsHTML(r) || !nonInteractiveHealthClient(r) {
		return "html"
	}
	return "text"
}

func nonInteractiveHealthClient(r *http.Request) bool {
	userAgent := strings.ToLower(strings.TrimSpace(r.UserAgent()))
	if userAgent == "" {
		return true
	}
	for _, name := range []string{"curl/", "wget/", "httpie/", "python-requests/", "go-http-client/"} {
		if strings.Contains(userAgent, name) {
			return true
		}
	}
	return false
}

func (s *Server) handleHealthAPI(w http.ResponseWriter, r *http.Request) {
	s.writeHealthResult(w, r, healthAPIFormat(r), false)
}

func (s *Server) writeHealthResult(w http.ResponseWriter, r *http.Request, format string, frontend bool) {
	response := s.buildHealthResponse()
	statusCode := healthResponseStatusCode(response.Status)
	switch format {
	case "json":
		writeJSON(w, statusCode, response)
	case "html":
		if s.templates == nil {
			writeJSON(w, http.StatusServiceUnavailable, response)
			return
		}
		data := s.pageContext(w, r, "/server/healthz")
		data["HealthPage"] = newHealthTemplateData(response, data["Lang"].(string))
		data["Title"] = "Countries - Health Status"
		data["Description"] = response.Project.Description
		s.renderTemplateResponse(w, r, "info.html", data, statusCode)
	case "text":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(statusCode)
		var output string
		if frontend {
			output = response.frontendText(s.languageForRequest(w, r))
		} else {
			output = response.apiText()
		}
		_, _ = w.Write([]byte(output))
	default:
		writeJSON(w, statusCode, response)
	}
}

func (h HealthResponse) apiText() string {
	var output strings.Builder
	fmt.Fprintf(&output, "project.name: %s\nproject.tagline: %s\nproject.description: %s\n", h.Project.Name, h.Project.Tagline, h.Project.Description)
	fmt.Fprintf(&output, "status: %s\n", h.Status)
	if h.PendingRestart {
		fmt.Fprintf(&output, "pending_restart: true\n")
		for _, reason := range h.RestartReason {
			fmt.Fprintf(&output, "restart_reason: %s\n", reason)
		}
	}
	fmt.Fprintf(&output, "version: %s\ngo_version: %s\nbuild.commit: %s\nbuild.date: %s\n", h.Version, h.GoVersion, h.Build.Commit, h.Build.Date)
	fmt.Fprintf(&output, "uptime: %s\nmode: %s\ntimestamp: %s\ncluster.enabled: %t\n", h.Uptime, h.Mode, h.Timestamp.UTC().Format(time.RFC3339), h.Cluster.Enabled)
	fmt.Fprintf(&output, "features.tor: %s\nfeatures.i2p: %s\nfeatures.geoip: %s\n", overlayText(h.Features.Tor.Status, h.Features.Tor.Hostname), i2pText(h.Features.I2P), enabledText(h.Features.GeoIP))
	fmt.Fprintf(&output, "checks.database: %s\nchecks.cache: %s\nchecks.disk: %s\nchecks.scheduler: %s\n", h.Checks.Database, h.Checks.Cache, h.Checks.Disk, h.Checks.Scheduler)
	if h.Checks.Tor != "" {
		fmt.Fprintf(&output, "checks.tor: %s\n", h.Checks.Tor)
	}
	if h.Checks.I2P != "" {
		fmt.Fprintf(&output, "checks.i2p: %s\n", h.Checks.I2P)
	}
	for _, check := range h.Checks.Datasets {
		fmt.Fprintf(&output, "checks.%s: %s\n", check.Key, check.Status)
	}
	fmt.Fprintf(&output, "stats.requests_total: %d\nstats.requests_24h: %d\nstats.active_connections: %d\n", h.Stats.RequestsTotal, h.Stats.Requests24h, h.Stats.ActiveConns)
	for _, stat := range h.Stats.Project {
		fmt.Fprintf(&output, "stats.%s: %d\n", stat.Key, stat.Value)
	}
	return output.String()
}

func (h HealthResponse) frontendText(lang string) string {
	var output strings.Builder
	fmt.Fprintf(&output, "%s - %s\n\n", h.Project.Name, h.Project.Tagline)
	fmt.Fprintf(&output, "%s:    %s\n", healthLabel(lang, "health.label.status", "Status"), healthStatusText(lang, h.Status))
	if h.PendingRestart {
		fmt.Fprintf(&output, "%s\n", healthLabel(lang, "health.pending_restart", "A restart is required for the changed settings."))
		for _, reason := range h.RestartReason {
			name := humanizeHealthWord(strings.NewReplacer(".", " ", "_", " ").Replace(reason))
			fmt.Fprintf(&output, "  %s\n", name)
		}
	}
	fmt.Fprintf(&output, "%s:   %s\n", healthLabel(lang, "health.label.version", "Version"), h.Version)
	if h.Build.Commit != "" || h.Build.Date != "" {
		date := h.Build.Date
		if parsed, err := time.Parse(time.RFC3339, h.Build.Date); err == nil {
			date = humanHealthTimestamp(parsed)
		}
		fmt.Fprintf(&output, "%s:     %s (%s)\n", healthLabel(lang, "health.label.build", "Build"), h.Build.Commit, date)
	}
	fmt.Fprintf(&output, "%s:      %s\n%s:    %s\n%s:       %s\n%s:      %s\n", healthLabel(lang, "health.label.go_version", "Go"), h.GoVersion, healthLabel(lang, "health.label.uptime", "Uptime"), humanHealthUptimeForLang(h.Uptime, lang), healthLabel(lang, "health.label.mode", "Mode"), healthLabel(lang, "health.mode."+h.Mode, humanizeHealthWord(h.Mode)), healthLabel(lang, "health.label.checked", "Checked"), humanHealthTimestamp(h.Timestamp))
	fmt.Fprintf(&output, "%s:   %s\n\n", healthLabel(lang, "health.section.cluster", "Cluster"), localizedEnabled(lang, h.Cluster.Enabled))
	fmt.Fprintf(&output, "%s\n  %s: %s\n  %s: %s\n  %s: %s\n\n", healthLabel(lang, "health.section.features", "Features"), healthLabel(lang, "health.feature.tor", "Tor"), localizedOverlayText(lang, h.Features.Tor.Status, h.Features.Tor.Hostname), healthLabel(lang, "health.feature.i2p", "I2P"), localizedI2PText(lang, h.Features.I2P), healthLabel(lang, "health.feature.geoip", "GeoIP"), localizedEnabled(lang, h.Features.GeoIP))
	fmt.Fprintf(&output, "%s\n", healthLabel(lang, "health.section.checks", "Checks"))
	for _, check := range h.Checks.ordered() {
		fmt.Fprintf(&output, "  %-20s %s\n", healthCheckLabel(lang, check.Key), healthCheckStatusText(lang, check.Status))
	}
	fmt.Fprintf(&output, "\n%s\n  %-20s %s\n  %-20s %s\n  %-20s %s\n", healthLabel(lang, "health.section.stats", "Statistics"), healthLabel(lang, "health.stat.requests_total", "Total requests"), sharedformat.Count(h.Stats.RequestsTotal, lang), healthLabel(lang, "health.stat.requests_24h", "Requests (24 hours)"), sharedformat.Count(h.Stats.Requests24h, lang), healthLabel(lang, "health.stat.active_connections", "Active connections"), sharedformat.Count(h.Stats.ActiveConns, lang))
	for _, stat := range h.Stats.Project {
		label := humanizeHealthWord(strings.ReplaceAll(stat.Key, "_", " "))
		fmt.Fprintf(&output, "  %-20s %s\n", healthLabel(lang, "health.stat."+stat.Key, label), sharedformat.Count(stat.Value, lang))
	}
	return output.String()
}

func (c HealthChecks) ordered() []HealthCheck {
	checks := []HealthCheck{{Key: "database", Status: c.Database}, {Key: "cache", Status: c.Cache}, {Key: "disk", Status: c.Disk}, {Key: "scheduler", Status: c.Scheduler}}
	if c.Tor != "" {
		checks = append(checks, HealthCheck{Key: "tor", Status: c.Tor})
	}
	if c.I2P != "" {
		checks = append(checks, HealthCheck{Key: "i2p", Status: c.I2P})
	}
	checks = append(checks, c.Datasets...)
	return checks
}

func overlayText(status, hostname string) string {
	if hostname != "" {
		return status + " - " + hostname
	}
	return status
}

func i2pText(info HealthI2PInfo) string {
	line := info.Status
	if info.Hostname != "" {
		line += " - " + info.Hostname
	}
	if info.Provider != "" && info.Provider != "none" {
		line += " provider=" + info.Provider
	}
	if info.State != "" && info.State != "disabled" {
		line += " state=" + info.State
	}
	return line
}

func enabledText(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func localizedEnabled(lang string, enabled bool) string {
	return healthLabel(lang, "health.feature."+enabledText(enabled), enabledText(enabled))
}

func humanizeHealthWord(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func localizedOverlayText(lang, status, hostname string) string {
	status = healthLabel(lang, "health.overlay."+status, status)
	if hostname != "" {
		return status + " - " + hostname
	}
	return status
}

func localizedI2PText(lang string, info HealthI2PInfo) string {
	line := healthLabel(lang, "health.overlay."+info.Status, info.Status)
	if info.Hostname != "" {
		line += " - " + info.Hostname
	}
	if info.Provider != "" && info.Provider != "none" {
		line += " provider=" + info.Provider
	}
	if info.State != "" && info.State != "disabled" {
		line += " state=" + info.State
	}
	return line
}

func healthLabel(lang, key, fallback string) string {
	value := i18n.Translate(lang, key)
	if value == key || value == "" {
		return fallback
	}
	return value
}

func healthStatusText(lang, status string) string {
	fallback := strings.ReplaceAll(status, "_", " ")
	if fallback != "" {
		fallback = strings.ToUpper(fallback[:1]) + fallback[1:]
	}
	return healthLabel(lang, "health.status."+status, fallback)
}

func healthCheckLabel(lang, key string) string {
	fallback := strings.ReplaceAll(key, "_", " ")
	if strings.HasPrefix(key, "data_") {
		fallback = "Data: " + strings.TrimPrefix(fallback, "data ")
	} else if strings.HasPrefix(key, "geoip_") {
		fallback = "GeoIP " + strings.TrimPrefix(fallback, "geoip ")
	} else if strings.HasPrefix(key, "blocklist_") {
		fallback = "Blocklist: " + strings.TrimPrefix(fallback, "blocklist ")
	}
	return healthLabel(lang, "health.check."+key, fallback)
}

func healthCheckStatusText(lang, status string) string {
	fallback := status
	if status == "ok" {
		fallback = "ok"
	}
	return healthLabel(lang, "health.check_status."+status, fallback)
}
