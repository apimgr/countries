// Package metrics provides bounded Prometheus collectors for Countries.
package metrics

import (
	"bufio"
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

const (
	defaultMaxPaths    = 128
	maxConfiguredPaths = 512
	maxTaskLabels      = 64
	maxPathBytes       = 256
	maxPathSegments    = 16
)

var (
	methodValues = map[string]struct{}{
		http.MethodGet: {}, http.MethodPost: {}, http.MethodPut: {},
		http.MethodDelete: {}, http.MethodPatch: {}, http.MethodOptions: {},
		http.MethodHead: {},
	}
	statusValues = map[int]struct{}{
		200: {}, 201: {}, 202: {}, 204: {}, 206: {},
		301: {}, 302: {}, 303: {}, 304: {}, 307: {}, 308: {},
		400: {}, 401: {}, 403: {}, 404: {}, 405: {}, 406: {}, 408: {}, 409: {},
		413: {}, 415: {}, 422: {}, 429: {},
		500: {}, 501: {}, 502: {}, 503: {}, 504: {},
	}
	pathUUID    = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	pathLongHex = regexp.MustCompile(`(?i)^[0-9a-f]{16,}$`)
	pathDigits  = regexp.MustCompile(`^[0-9]{4,}$`)
)

// Config supplies fixed application metadata and optional collectors.
type Config struct {
	ProjectName      string
	Version          string
	Commit           string
	BuildDate        string
	DataDir          string
	IncludeSystem    bool
	IncludeRuntime   bool
	MaxPaths         int
	HTTPBuckets      []float64
	SizeBuckets      []float64
	DBBuckets        []float64
	SchedulerBuckets []float64
}

// Metrics owns an isolated registry and bounded metric vectors.
type Metrics struct {
	registry *prometheus.Registry
	started  time.Time
	project  string
	maxPaths int
	dataDir  string

	pathMu sync.Mutex
	paths  map[string]struct{}
	taskMu sync.Mutex
	tasks  map[string]struct{}

	httpRequests       *prometheus.CounterVec
	httpDuration       *prometheus.HistogramVec
	httpRequestSize    *prometheus.HistogramVec
	httpResponseSize   *prometheus.HistogramVec
	httpActiveRequests prometheus.Gauge

	dbQueries     *prometheus.CounterVec
	dbDuration    *prometheus.HistogramVec
	dbConnections prometheus.Gauge
	dbInUse       prometheus.Gauge
	dbErrors      *prometheus.CounterVec

	cacheHits      *prometheus.CounterVec
	cacheMisses    *prometheus.CounterVec
	cacheEvictions *prometheus.CounterVec
	cacheSize      *prometheus.GaugeVec
	cacheBytes     *prometheus.GaugeVec

	schedulerRuns     *prometheus.CounterVec
	schedulerDuration *prometheus.HistogramVec
	schedulerRunning  *prometheus.GaugeVec
	schedulerLastRun  *prometheus.GaugeVec

	authAttempts      *prometheus.CounterVec
	ownerTokensActive *prometheus.GaugeVec

	rateLimitRequests *prometheus.CounterVec
	rateLimitBlocked  *prometheus.CounterVec

	countryRecords prometheus.Gauge
	torEnabled     prometheus.Gauge
	torRunning     prometheus.Gauge
	torCircuit     prometheus.Gauge
	torRequests    prometheus.Counter
	torBandwidth   prometheus.Gauge

	systemCPUUsage    prometheus.Gauge
	systemMemoryUsage prometheus.Gauge
	systemMemoryUsed  prometheus.Gauge
	systemMemoryTotal prometheus.Gauge
	systemDiskUsage   *prometheus.GaugeVec
	systemDiskUsed    *prometheus.GaugeVec
	systemDiskTotal   *prometheus.GaugeVec

	startSystemOnce sync.Once
	startSystemStop context.CancelFunc
}

// New creates an isolated metrics registry. Runtime metrics are sampled at
// scrape time; system metrics are refreshed by StartSystemCollector.
func New(cfg Config) *Metrics {
	project := metricName(cfg.ProjectName)
	maxPaths := cfg.MaxPaths
	if maxPaths < 1 {
		maxPaths = defaultMaxPaths
	}
	if maxPaths > maxConfiguredPaths {
		maxPaths = maxConfiguredPaths
	}
	registry := prometheus.NewRegistry()
	metrics := &Metrics{
		registry: registry,
		started:  time.Now(),
		project:  project,
		maxPaths: maxPaths,
		dataDir:  cfg.DataDir,
		paths:    map[string]struct{}{"/": {}},
		tasks:    make(map[string]struct{}),
	}
	register := func(collector prometheus.Collector) {
		registry.MustRegister(collector)
	}
	newCounter := func(name, help string) prometheus.Counter {
		counter := prometheus.NewCounter(prometheus.CounterOpts{Namespace: project, Name: name, Help: help})
		register(counter)
		return counter
	}
	newGauge := func(name, help string) prometheus.Gauge {
		gauge := prometheus.NewGauge(prometheus.GaugeOpts{Namespace: project, Name: name, Help: help})
		register(gauge)
		return gauge
	}
	newGaugeVec := func(name, help string, labels []string) *prometheus.GaugeVec {
		gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: project, Name: name, Help: help}, labels)
		register(gauge)
		return gauge
	}
	newCounterVec := func(name, help string, labels []string) *prometheus.CounterVec {
		counter := prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: project, Name: name, Help: help}, labels)
		register(counter)
		return counter
	}
	newHistogramVec := func(name, help string, labels []string, buckets []float64) *prometheus.HistogramVec {
		histogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: project, Name: name, Help: help, Buckets: positiveBuckets(buckets)}, labels)
		register(histogram)
		return histogram
	}

	appInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: project,
		Name:      "app_info",
		Help:      "Application build information.",
	}, []string{"version", "commit", "build_date", "go_version"})
	register(appInfo)
	appInfo.WithLabelValues(cfg.Version, cfg.Commit, cfg.BuildDate, runtime.Version()).Set(1)
	register(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: project,
		Name:      "app_uptime_seconds",
		Help:      "Application uptime in seconds.",
	}, func() float64 { return time.Since(metrics.started).Seconds() }))
	startTimestamp := newGauge("app_start_timestamp", "Unix timestamp when the application started.")
	startTimestamp.Set(float64(metrics.started.Unix()))

	metrics.httpRequests = newCounterVec("http_requests_total", "Total number of HTTP requests.", []string{"method", "path", "status"})
	metrics.httpDuration = newHistogramVec("http_request_duration_seconds", "HTTP request duration in seconds.", []string{"method", "path"}, cfg.HTTPBuckets)
	metrics.httpRequestSize = newHistogramVec("http_request_size_bytes", "HTTP request size in bytes.", []string{"method", "path"}, cfg.SizeBuckets)
	metrics.httpResponseSize = newHistogramVec("http_response_size_bytes", "HTTP response size in bytes.", []string{"method", "path"}, cfg.SizeBuckets)
	metrics.httpActiveRequests = newGauge("http_active_requests", "Number of HTTP requests currently being processed.")

	metrics.dbQueries = newCounterVec("db_queries_total", "Total number of database queries.", []string{"operation", "table"})
	metrics.dbDuration = newHistogramVec("db_query_duration_seconds", "Database query duration in seconds.", []string{"operation", "table"}, cfg.DBBuckets)
	metrics.dbConnections = newGauge("db_connections_open", "Number of open database connections.")
	metrics.dbInUse = newGauge("db_connections_in_use", "Number of connections actively in use.")
	metrics.dbErrors = newCounterVec("db_errors_total", "Total number of database errors.", []string{"operation", "error_type"})

	metrics.cacheHits = newCounterVec("cache_hits_total", "Total number of cache hits.", []string{"cache"})
	metrics.cacheMisses = newCounterVec("cache_misses_total", "Total number of cache misses.", []string{"cache"})
	metrics.cacheEvictions = newCounterVec("cache_evictions_total", "Total number of cache evictions.", []string{"cache"})
	metrics.cacheSize = newGaugeVec("cache_size", "Current number of cache items.", []string{"cache"})
	metrics.cacheBytes = newGaugeVec("cache_bytes", "Current cache size in bytes.", []string{"cache"})

	metrics.schedulerRuns = newCounterVec("scheduler_tasks_total", "Total number of scheduled task executions.", []string{"task", "status"})
	metrics.schedulerDuration = newHistogramVec("scheduler_task_duration_seconds", "Scheduled task duration in seconds.", []string{"task"}, cfg.SchedulerBuckets)
	metrics.schedulerRunning = newGaugeVec("scheduler_tasks_running", "Number of currently running scheduled tasks.", []string{"task"})
	metrics.schedulerLastRun = newGaugeVec("scheduler_last_run_timestamp", "Unix timestamp of the last scheduled task run.", []string{"task"})

	metrics.authAttempts = newCounterVec("auth_attempts_total", "Total authentication attempts.", []string{"method", "status"})
	for _, method := range []string{"owner_token", "api_token"} {
		for _, status := range []string{"success", "failed"} {
			metrics.authAttempts.WithLabelValues(method, status).Add(0)
		}
	}
	metrics.ownerTokensActive = newGaugeVec("owner_tokens_active", "Number of active owner tokens.", []string{"kind"})
	metrics.ownerTokensActive.WithLabelValues("primary").Set(0)
	metrics.ownerTokensActive.WithLabelValues("linked").Set(0)

	metrics.rateLimitRequests = newCounterVec("ratelimit_requests_total", "Total rate-limited requests.", []string{"limit", "status"})
	metrics.rateLimitBlocked = newCounterVec("ratelimit_blocked_total", "Requests blocked by rate limiting.", []string{"limit"})
	metrics.countryRecords = newGauge("country_records", "Number of country records loaded.")
	metrics.torEnabled = newGauge("tor_enabled", "Whether a Tor binary was found and the hidden service can be used.")
	metrics.torRunning = newGauge("tor_running", "Whether the Tor process is running.")
	metrics.torCircuit = newGauge("tor_circuit_established", "Whether a Tor circuit is established.")
	metrics.torRequests = newCounter("tor_requests_total", "Total requests served through the Tor hidden service.")
	metrics.torBandwidth = newGauge("tor_bandwidth_usage_bytes", "Tor bandwidth used in the current accounting period.")

	if cfg.IncludeRuntime {
		register(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: project, Name: "go_goroutines", Help: "Number of current goroutines."}, func() float64 {
			return float64(runtime.NumGoroutine())
		}))
		register(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: project, Name: "go_mem_alloc_bytes", Help: "Bytes allocated and currently in use by Go."}, func() float64 {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			return float64(stats.Alloc)
		}))
		register(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: project, Name: "go_mem_sys_bytes", Help: "Bytes obtained from the system by Go."}, func() float64 {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			return float64(stats.Sys)
		}))
		register(prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: project, Name: "go_gc_runs_total", Help: "Total number of completed Go garbage collections."}, func() float64 {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			return float64(stats.NumGC)
		}))
		register(prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: project, Name: "go_gc_pause_total_seconds", Help: "Total time spent in Go garbage collection pauses."}, func() float64 {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			return float64(stats.PauseTotalNs) / float64(time.Second)
		}))
	}

	if cfg.IncludeSystem {
		metrics.systemCPUUsage = newGauge("system_cpu_usage_percent", "Current CPU usage percentage.")
		metrics.systemMemoryUsage = newGauge("system_memory_usage_percent", "Current system memory usage percentage.")
		metrics.systemMemoryUsed = newGauge("system_memory_used_bytes", "System memory currently in use, in bytes.")
		metrics.systemMemoryTotal = newGauge("system_memory_total_bytes", "Total system memory, in bytes.")
		metrics.systemDiskUsage = newGaugeVec("system_disk_usage_percent", "Disk usage percentage for the configured data directory.", []string{"path"})
		metrics.systemDiskUsed = newGaugeVec("system_disk_used_bytes", "Disk space used by the configured data directory.", []string{"path"})
		metrics.systemDiskTotal = newGaugeVec("system_disk_total_bytes", "Total disk space for the configured data directory.", []string{"path"})
	}
	return metrics
}

// Handler exposes the registry in Prometheus text exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Registry exposes the isolated registry for service-specific collectors.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// SetDataDir sets the application data directory reported by system metrics.
func (m *Metrics) SetDataDir(directory string) {
	m.dataDir = directory
}

// Middleware records HTTP metrics while preserving common ResponseWriter interfaces.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := normalizeMethod(r.Method)
		path := m.normalizePath(r.URL.Path)
		body := r.Body
		var counter *countingBody
		if body != nil {
			counter = &countingBody{ReadCloser: body}
			r.Body = counter
		}
		writer := &responseRecorder{ResponseWriter: w}
		m.httpActiveRequests.Inc()
		started := time.Now()
		defer func() {
			if writer.status == 0 {
				writer.status = http.StatusOK
			}
			requestSize := int64(0)
			if r.ContentLength >= 0 {
				requestSize = r.ContentLength
			} else if counter != nil {
				requestSize = counter.read.Load()
			}
			m.RecordHTTP(method, path, writer.status, requestSize, writer.bytes, time.Since(started))
			m.httpActiveRequests.Dec()
		}()
		next.ServeHTTP(writer, r)
	})
}

// RecordHTTP records one HTTP request. Request bodies and query values must not
// be passed as labels; path is normalized and capped internally.
func (m *Metrics) RecordHTTP(method, path string, status int, requestBytes, responseBytes int64, duration time.Duration) {
	method = normalizeMethod(method)
	path = m.normalizePath(path)
	statusLabel := normalizeStatus(status)
	if requestBytes < 0 {
		requestBytes = 0
	}
	if responseBytes < 0 {
		responseBytes = 0
	}
	seconds := duration.Seconds()
	if seconds < 0 {
		seconds = 0
	}
	m.httpRequests.WithLabelValues(method, path, statusLabel).Inc()
	m.httpDuration.WithLabelValues(method, path).Observe(seconds)
	m.httpRequestSize.WithLabelValues(method, path).Observe(float64(requestBytes))
	m.httpResponseSize.WithLabelValues(method, path).Observe(float64(responseBytes))
}

// ObserveDB records an operation and its duration. Unknown operation/table
// values are collapsed into a bounded "other" label.
func (m *Metrics) ObserveDB(operation, table string, duration time.Duration, errType string) {
	operation = normalizeDBOperation(operation)
	table = normalizeTable(table)
	seconds := duration.Seconds()
	if seconds < 0 {
		seconds = 0
	}
	m.dbQueries.WithLabelValues(operation, table).Inc()
	m.dbDuration.WithLabelValues(operation, table).Observe(seconds)
	if errType != "" {
		m.dbErrors.WithLabelValues(operation, normalizeDBError(errType)).Inc()
	}
}

// SetDatabaseConnections updates database pool gauges.
func (m *Metrics) SetDatabaseConnections(open, inUse int) {
	if open < 0 {
		open = 0
	}
	if inUse < 0 {
		inUse = 0
	}
	m.dbConnections.Set(float64(open))
	m.dbInUse.Set(float64(inUse))
}

// RecordCache records a cache hit or miss for a known cache name.
func (m *Metrics) RecordCache(cache string, hit bool) {
	cache = normalizeCache(cache)
	if hit {
		m.cacheHits.WithLabelValues(cache).Inc()
	} else {
		m.cacheMisses.WithLabelValues(cache).Inc()
	}
}

// RecordCacheEviction records an eviction for a known cache name.
func (m *Metrics) RecordCacheEviction(cache string) {
	m.cacheEvictions.WithLabelValues(normalizeCache(cache)).Inc()
}

// SetCacheSize updates current cache item and byte counts.
func (m *Metrics) SetCacheSize(cache string, items, bytes int64) {
	if items < 0 {
		items = 0
	}
	if bytes < 0 {
		bytes = 0
	}
	cache = normalizeCache(cache)
	m.cacheSize.WithLabelValues(cache).Set(float64(items))
	m.cacheBytes.WithLabelValues(cache).Set(float64(bytes))
}

// RecordSchedulerStart increments the current executions for a task.
func (m *Metrics) RecordSchedulerStart(task string) {
	m.schedulerRunning.WithLabelValues(m.normalizeTask(task)).Inc()
}

// RecordSchedulerEnd records a completed task and its result.
func (m *Metrics) RecordSchedulerEnd(task string, duration time.Duration, err error, endedAt time.Time) {
	task = m.normalizeTask(task)
	m.schedulerRunning.WithLabelValues(task).Dec()
	seconds := duration.Seconds()
	if seconds < 0 {
		seconds = 0
	}
	m.schedulerDuration.WithLabelValues(task).Observe(seconds)
	m.schedulerLastRun.WithLabelValues(task).Set(float64(endedAt.Unix()))
	status := "success"
	if err != nil {
		status = "error"
	}
	m.schedulerRuns.WithLabelValues(task, status).Inc()
}

// RecordAuth records authentication attempt metrics without identity labels.
func (m *Metrics) RecordAuth(method, status string) {
	method = normalizeAuthMethod(method)
	if status != "success" && status != "failed" {
		status = "other"
	}
	m.authAttempts.WithLabelValues(method, status).Inc()
}

// SetActiveOwnerTokens updates the required owner-token gauge series.
func (m *Metrics) SetActiveOwnerTokens(primary, linked int) {
	if primary < 0 {
		primary = 0
	}
	if linked < 0 {
		linked = 0
	}
	m.ownerTokensActive.WithLabelValues("primary").Set(float64(primary))
	m.ownerTokensActive.WithLabelValues("linked").Set(float64(linked))
}

// RecordRateLimit records an aggregate rate-limit outcome; it never receives IPs.
func (m *Metrics) RecordRateLimit(limit string, limited bool) {
	limit = normalizeLimit(limit)
	status := "allowed"
	if limited {
		status = "limited"
		m.rateLimitBlocked.WithLabelValues(limit).Inc()
	}
	m.rateLimitRequests.WithLabelValues(limit, status).Inc()
}

// SetCountryRecords updates the application-specific country-record gauge.
func (m *Metrics) SetCountryRecords(count int) {
	if count < 0 {
		count = 0
	}
	m.countryRecords.Set(float64(count))
}

// SetTorState updates the optional Tor gauges.
func (m *Metrics) SetTorState(enabled, running, circuitEstablished bool) {
	m.torEnabled.Set(boolFloat(enabled))
	m.torRunning.Set(boolFloat(running))
	m.torCircuit.Set(boolFloat(circuitEstablished))
}

// RecordTorRequest counts one request served through the Tor hidden service.
func (m *Metrics) RecordTorRequest() {
	m.torRequests.Inc()
}

// SetTorBandwidthUsage updates aggregate Tor accounting usage in bytes.
func (m *Metrics) SetTorBandwidthUsage(bytes uint64) {
	m.torBandwidth.Set(float64(bytes))
}

// StartSystemCollector periodically refreshes optional system metrics.
func (m *Metrics) StartSystemCollector(parent context.Context, interval time.Duration) {
	if m.systemCPUUsage == nil {
		return
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	m.startSystemOnce.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		m.startSystemStop = cancel
		go m.collectSystem(ctx, interval)
	})
}

// StopSystemCollector stops the optional system collector.
func (m *Metrics) StopSystemCollector() {
	if m.startSystemStop != nil {
		m.startSystemStop()
	}
}

func (m *Metrics) collectSystem(ctx context.Context, interval time.Duration) {
	m.collectSystemOnce()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.collectSystemOnce()
		}
	}
}

func (m *Metrics) collectSystemOnce() {
	if percent, err := cpu.Percent(0, false); err == nil && len(percent) > 0 {
		m.systemCPUUsage.Set(percent[0])
	}
	if memory, err := mem.VirtualMemory(); err == nil {
		m.systemMemoryUsage.Set(memory.UsedPercent)
		m.systemMemoryUsed.Set(float64(memory.Used))
		m.systemMemoryTotal.Set(float64(memory.Total))
	}
	if m.dataDir == "" {
		return
	}
	if usage, err := disk.Usage(m.dataDir); err == nil {
		m.systemDiskUsage.WithLabelValues(m.dataDir).Set(usage.UsedPercent)
		m.systemDiskUsed.WithLabelValues(m.dataDir).Set(float64(usage.Used))
		m.systemDiskTotal.WithLabelValues(m.dataDir).Set(float64(usage.Total))
	}
}

func (m *Metrics) normalizePath(path string) string {
	path = strings.SplitN(path, "?", 2)[0]
	path = strings.SplitN(path, "#", 2)[0]
	if path == "" {
		path = "/"
	}
	if len(path) > maxPathBytes || !strings.HasPrefix(path, "/") {
		return "/other"
	}
	segments := strings.Split(path, "/")
	if len(segments) > maxPathSegments {
		return "/other"
	}
	for index := 1; index < len(segments); index++ {
		segment := segments[index]
		if segment == "" {
			continue
		}
		if pathUUID.MatchString(segment) || pathLongHex.MatchString(segment) || pathDigits.MatchString(segment) || strings.Contains(segment, "@") || strings.Contains(segment, "%") {
			segments[index] = ":id"
		}
	}
	path = strings.Join(segments, "/")
	m.pathMu.Lock()
	defer m.pathMu.Unlock()
	if _, exists := m.paths[path]; exists {
		return path
	}
	if len(m.paths) >= m.maxPaths {
		return "/other"
	}
	m.paths[path] = struct{}{}
	return path
}

func (m *Metrics) normalizeTask(task string) string {
	task = normalizeLabel(task, 64)
	if task == "" {
		task = "other"
	}
	m.taskMu.Lock()
	defer m.taskMu.Unlock()
	if _, exists := m.tasks[task]; exists {
		return task
	}
	if len(m.tasks) >= maxTaskLabels {
		return "other"
	}
	m.tasks[task] = struct{}{}
	return task
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseRecorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

func (w *responseRecorder) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("underlying ResponseWriter does not support hijacking")
	}
	if w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return hijacker.Hijack()
}

func (w *responseRecorder) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

func (w *responseRecorder) ReadFrom(reader io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(reader)
		w.bytes += n
		return n, err
	}
	n, err := io.Copy(struct{ io.Writer }{w.ResponseWriter}, reader)
	w.bytes += n
	return n, err
}

func (w *responseRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type countingBody struct {
	io.ReadCloser
	read atomic.Int64
}

func (b *countingBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	b.read.Add(int64(n))
	return n, err
}

func normalizeMethod(method string) string {
	method = strings.ToUpper(method)
	if _, ok := methodValues[method]; !ok {
		return "OTHER"
	}
	return method
}

func normalizeStatus(status int) string {
	if status < 100 || status > 599 {
		return "unknown"
	}
	if _, ok := statusValues[status]; !ok {
		return "other"
	}
	return strconv.Itoa(status)
}

func normalizeDBOperation(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "select", "insert", "update", "delete":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func normalizeTable(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !validLabel(value, 64) {
		return "other"
	}
	switch value {
	case "app_secrets", "backups", "captcha_challenges", "countries", "rate_limits", "scheduler_tasks", "scheduler_history", "security_reports", "security_thanks":
		return value
	default:
		return "other"
	}
}

func normalizeDBError(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "connection", "timeout", "constraint", "duplicate":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func normalizeCache(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "items", "config", "rate_limit", "geoip", "database":
		return value
	default:
		return "other"
	}
}

func normalizeAuthMethod(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "owner_token", "api_token", "bearer", "session", "basic":
		return value
	default:
		return "other"
	}
}

func normalizeLimit(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "global", "per_ip", "per_user", "per_endpoint":
		return value
	default:
		return "other"
	}
}

func normalizeLabel(value string, max int) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !validLabel(value, max) {
		return "other"
	}
	return value
}

func validLabel(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || (index > 0 && (char == '_' || char == '-')) {
			continue
		}
		return false
	}
	return true
}

func metricName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	if !validLabel(value, 128) {
		return "countries"
	}
	return value
}

func positiveBuckets(buckets []float64) []float64 {
	if len(buckets) == 0 {
		return []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	}
	result := make([]float64, 0, min(len(buckets), 64))
	for _, bucket := range buckets {
		if bucket > 0 && !math.IsInf(bucket, 0) && !math.IsNaN(bucket) && len(result) < 64 {
			result = append(result, bucket)
		}
	}
	if len(result) == 0 {
		return []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	}
	sort.Float64s(result)
	unique := result[:0]
	for _, bucket := range result {
		if len(unique) == 0 || unique[len(unique)-1] != bucket {
			unique = append(unique, bucket)
		}
	}
	return unique
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
