package server

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/common/logbuffer"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/geoip"
	"github.com/go-chi/chi/v5/middleware"
)

func requestLogMiddleware(s *Server) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(wrapped, r)
			duration := time.Since(started)
			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}
			recordRequest(duration, status)
			if r.Method == http.MethodGet && status >= http.StatusOK && status < http.StatusMultipleChoices && isSuccessfulHealthRequest(r.URL.Path) {
				return
			}
			country, asn := accessGeoIPValues(r, []countryLookup{s.geoIPManager})
			logging := s.loggingConfigSnapshot()
			line := formatAccessLog(logging.Access.Format, logging.Defaults.Custom, r, started, status, wrapped.BytesWritten(), duration, country, asn)
			if err := s.writeAccessLog("access", line, time.Now()); err != nil {
				log.Printf("ERROR: access log write failed: %v", err)
			}
			if (status < http.StatusOK || status >= http.StatusMultipleChoices) && (isHealthRequest(r.URL.Path) || status >= http.StatusInternalServerError) {
				errorLine := fmt.Sprintf("%s [ERROR] request path=%q status=%d request_id=%s", started.Format(time.RFC3339), cleanLogValue(r.URL.Path), status, cleanLogValue(RequestIDFromContext(r.Context())))
				if err := s.writeAccessLog("error", errorLine, time.Now()); err != nil {
					log.Printf("ERROR: health error log write failed: %v", err)
				}
				logbuffer.Default().Add("error", line)
				log.Printf("ERROR: %s", line)
			} else {
				logbuffer.Default().Add("info", line)
				fmt.Fprintln(os.Stderr, line)
			}
		})
	}
}

func isHealthRequest(path string) bool {
	base := api.APIBasePath()
	return path == "/server/healthz" || path == "/healthz" || path == "/api/healthz" || path == "/api/healthz.txt" || path == base+"/server/healthz" || path == base+"/server/healthz.txt"
}

func (s *Server) writeAccessLog(kind, line string, now time.Time) error {
	s.accessLogMu.Lock()
	defer s.accessLogMu.Unlock()
	if s.logDir == "" {
		return nil
	}
	logging := s.loggingConfigSnapshot()
	rotate, keep := logging.Defaults.Rotate, logging.Defaults.Keep
	filename := kind + ".log"
	switch kind {
	case "access":
		if strings.TrimSpace(logging.Access.Filename) != "" {
			filename = logging.Access.Filename
		}
		if strings.TrimSpace(logging.Access.Rotate) != "" {
			rotate = logging.Access.Rotate
		}
		if strings.TrimSpace(logging.Access.Keep) != "" {
			keep = logging.Access.Keep
		}
	case "error":
		if strings.TrimSpace(logging.Error.Filename) != "" {
			filename = logging.Error.Filename
		}
		if strings.TrimSpace(logging.Error.Rotate) != "" {
			rotate = logging.Error.Rotate
		}
		if strings.TrimSpace(logging.Error.Keep) != "" {
			keep = logging.Error.Keep
		}
	default:
		return fmt.Errorf("unsupported request log kind %q", kind)
	}
	if filepath.Base(filename) != filename || filename == "." || filename == ".." {
		return fmt.Errorf("invalid %s log filename %q", kind, filename)
	}
	return writeRotatingLog(s.logDir, filename, cleanLogValue(line), rotate, keep, now)
}

func (s *Server) loggingConfigSnapshot() config.LoggingConfig {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	if s.config == nil {
		return config.LoggingConfig{}
	}
	return s.config.Server.Logging
}

func writeRotatingLog(directory, filename, line, rotation, retention string, now time.Time) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	path := filepath.Join(directory, filename)
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect log file: %w", err)
	}
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("log path is not a regular file")
	}
	line = strings.TrimRight(cleanLogValue(line), "\n") + "\n"
	if err == nil && shouldRotateLog(info, len(line), rotation, now) {
		if err := rotateLogFile(directory, filename, path, retention, now); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure log file: %w", err)
	}
	if _, err := file.WriteString(line); err != nil {
		_ = file.Close()
		return fmt.Errorf("append log entry: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close log file: %w", err)
	}
	return nil
}

func shouldRotateLog(info os.FileInfo, incomingBytes int, policy string, now time.Time) bool {
	if strings.TrimSpace(policy) == "" || strings.EqualFold(strings.TrimSpace(policy), "never") {
		return false
	}
	for _, option := range strings.Split(policy, ",") {
		option = strings.TrimSpace(option)
		switch strings.ToLower(option) {
		case "daily":
			if info.ModTime().UTC().Format("2006-01-02") != now.UTC().Format("2006-01-02") {
				return true
			}
		case "weekly":
			oldYear, oldWeek := info.ModTime().UTC().ISOWeek()
			newYear, newWeek := now.UTC().ISOWeek()
			if oldYear != newYear || oldWeek != newWeek {
				return true
			}
		case "monthly":
			old, current := info.ModTime().UTC(), now.UTC()
			if old.Year() != current.Year() || old.Month() != current.Month() {
				return true
			}
		case "yearly":
			if info.ModTime().UTC().Year() != now.UTC().Year() {
				return true
			}
		default:
			if limit, ok := parseLogSize(option); ok && info.Size()+int64(incomingBytes) > limit {
				return true
			}
		}
	}
	return false
}

func parseLogSize(value string) (int64, bool) {
	value = strings.ToUpper(strings.TrimSpace(value))
	unit := int64(0)
	switch {
	case strings.HasSuffix(value, "MB"):
		unit, value = 1<<20, strings.TrimSuffix(value, "MB")
	case strings.HasSuffix(value, "GB"):
		unit, value = 1<<30, strings.TrimSuffix(value, "GB")
	default:
		return 0, false
	}
	number, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || number == 0 || number > uint64(int64(^uint64(0)>>1)/unit) {
		return 0, false
	}
	return int64(number) * unit, true
}

func rotateLogFile(directory, filename, path, retention string, now time.Time) error {
	archive := ""
	for suffix := 0; ; suffix++ {
		name := strings.TrimSuffix(filename, filepath.Ext(filename)) + "-" + now.UTC().Format("20060102T150405.000000000Z")
		if suffix > 0 {
			name += "-" + strconv.Itoa(suffix)
		}
		archive = filepath.Join(directory, name+filepath.Ext(filename))
		if _, err := os.Stat(archive); os.IsNotExist(err) {
			break
		} else if err != nil {
			return fmt.Errorf("inspect rotated log: %w", err)
		}
	}
	if err := os.Rename(path, archive); err != nil {
		return fmt.Errorf("rotate log file: %w", err)
	}
	if err := os.Chmod(archive, 0o600); err != nil {
		return fmt.Errorf("secure rotated log file: %w", err)
	}
	return pruneRotatedLogs(directory, filename, retention, now)
}

func pruneRotatedLogs(directory, filename, retention string, now time.Time) error {
	base := strings.TrimSuffix(filename, filepath.Ext(filename)) + "-"
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read rotated logs: %w", err)
	}
	type rotatedLog struct {
		path string
		mod  time.Time
	}
	logs := make([]rotatedLog, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), base) || filepath.Ext(entry.Name()) != filepath.Ext(filename) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect rotated log %s: %w", entry.Name(), err)
		}
		logs = append(logs, rotatedLog{path: filepath.Join(directory, entry.Name()), mod: info.ModTime()})
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].mod.After(logs[j].mod) })
	keep := strings.ToLower(strings.TrimSpace(retention))
	if keep == "forever" {
		return nil
	}
	keepCount := -1
	var cutoff time.Time
	period := ""
	if keep == "none" || keep == "" {
		keepCount = 0
	} else if strings.HasPrefix(keep, "weekly:") || strings.HasPrefix(keep, "monthly:") {
		parts := strings.SplitN(keep, ":", 2)
		period = parts[0]
		if count, err := strconv.Atoi(parts[1]); err == nil && count >= 0 {
			keepCount = count
		} else {
			keepCount = 0
		}
	} else if count, err := strconv.Atoi(keep); err == nil {
		if count >= 0 {
			keepCount = count
		}
	} else if len(keep) > 1 {
		count, err := strconv.Atoi(keep[:len(keep)-1])
		if err == nil && count >= 0 {
			switch keep[len(keep)-1] {
			case 'd':
				cutoff = now.AddDate(0, 0, -count)
			case 'w':
				cutoff = now.AddDate(0, 0, -7*count)
			case 'm':
				cutoff = now.AddDate(0, -count, 0)
			default:
				keepCount = 0
			}
		}
	}
	allowedPeriods := make(map[string]struct{})
	if period != "" && keepCount >= 0 {
		for _, entry := range logs {
			key := entry.mod.UTC().Format("2006-01")
			if period == "weekly" {
				year, week := entry.mod.UTC().ISOWeek()
				key = fmt.Sprintf("%04d-W%02d", year, week)
			}
			if _, exists := allowedPeriods[key]; !exists && len(allowedPeriods) >= keepCount {
				continue
			}
			allowedPeriods[key] = struct{}{}
		}
	}
	for index, entry := range logs {
		remove := false
		switch {
		case keepCount >= 0 && period == "":
			remove = index >= keepCount
		case period != "":
			key := entry.mod.UTC().Format("2006-01")
			if period == "weekly" {
				year, week := entry.mod.UTC().ISOWeek()
				key = fmt.Sprintf("%04d-W%02d", year, week)
			}
			_, keepPeriod := allowedPeriods[key]
			remove = !keepPeriod
		case !cutoff.IsZero():
			remove = entry.mod.Before(cutoff)
		}
		if remove {
			if err := os.Remove(entry.path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove expired log %s: %w", filepath.Base(entry.path), err)
			}
		}
	}
	return nil
}

func formatAccessLog(format, custom string, r *http.Request, started time.Time, status, size int, duration time.Duration, geoValues ...string) string {
	remoteIP := requestClientIP(r.RemoteAddr)
	userAgent := r.UserAgent()
	referer := sanitizeLoggedURL(r.Referer())
	protocol := r.Proto
	tlsVersion := ""
	if r.TLS != nil {
		tlsVersion = tlsVersionName(r.TLS.Version)
	}

	country, asn := "", ""
	if len(geoValues) > 0 {
		country = geoValues[0]
	}
	if len(geoValues) > 1 {
		asn = geoValues[1]
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "custom" && strings.TrimSpace(custom) == "" {
		format = "apache"
	}
	switch format {
	case "nginx":
		return fmt.Sprintf("%s - - [%s] %q %d %d", cleanLogValue(remoteIP), started.Format("02/Jan/2006:15:04:05 -0700"), cleanLogValue(r.Method+" "+sanitizedRequestURI(r.URL)+" "+protocol), status, size)
	case "json":
		line, err := json.Marshal(map[string]any{
			"ip": cleanLogValue(remoteIP), "time": started.UTC().Format(time.RFC3339Nano), "method": cleanLogValue(r.Method),
			"path": cleanLogValue(r.URL.Path), "status": status, "size": size, "ua": cleanLogValue(userAgent),
		})
		if err != nil {
			return "access log encoding failed"
		}
		return string(line)
	case "custom":
		{
			values := map[string]string{
				"{time}": started.Format("15:04:05"), "{date}": started.Format("2006-01-02"),
				"{datetime}": started.Format(time.RFC3339Nano), "{remote_ip}": remoteIP,
				"{method}": r.Method, "{path}": r.URL.Path, "{query}": redactSensitiveQuery(r.URL.RawQuery),
				"{status}": strconv.Itoa(status), "{bytes}": strconv.Itoa(size),
				"{latency}": duration.String(), "{latency_ms}": strconv.FormatFloat(float64(duration)/float64(time.Millisecond), 'f', 3, 64),
				"{user_agent}": userAgent, "{referer}": referer, "{request_id}": RequestIDFromContext(r.Context()),
				"{fqdn}": r.Host, "{protocol}": protocol, "{tls_version}": tlsVersion,
				"{country}": country, "{asn}": asn,
			}
			var builder strings.Builder
			for i := 0; i < len(custom); {
				if custom[i] == '{' {
					end := strings.IndexByte(custom[i:], '}')
					if end >= 0 {
						end += i + 1
						key := custom[i:end]
						if value, ok := values[key]; ok {
							builder.WriteString(cleanLogValue(value))
							i = end
							continue
						}
						builder.WriteString(key)
						i = end
						continue
					}
				}
				builder.WriteByte(custom[i])
				i++
			}
			return cleanLogValue(builder.String())
		}
	}
	return fmt.Sprintf("%s - - [%s] %q %d %d %q %q", cleanLogValue(remoteIP), started.Format("02/Jan/2006:15:04:05 -0700"), cleanLogValue(r.Method+" "+sanitizedRequestURI(r.URL)+" "+protocol), status, size, cleanLogValue(referer), cleanLogValue(userAgent))
}

var sensitiveLoggedQueryParameters = map[string]struct{}{
	"token": {}, "session": {}, "code": {}, "key": {}, "password": {}, "secret": {},
	"auth": {}, "pwd": {}, "api_key": {}, "apikey": {}, "access_token": {}, "refresh_token": {},
}

func redactSensitiveQuery(query string) string {
	if query == "" {
		return ""
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return "[redacted]"
	}
	for key := range values {
		if _, sensitive := sensitiveLoggedQueryParameters[strings.ToLower(key)]; sensitive {
			values[key] = []string{"[redacted]"}
		}
	}
	return values.Encode()
}

func sanitizedRequestURI(requestURL *url.URL) string {
	if requestURL == nil {
		return ""
	}
	safeURL := *requestURL
	safeURL.RawQuery = redactSensitiveQuery(safeURL.RawQuery)
	return safeURL.RequestURI()
}

func sanitizeLoggedURL(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "[redacted]"
	}
	parsed.RawQuery = redactSensitiveQuery(parsed.RawQuery)
	return parsed.String()
}

func accessGeoIPValues(r *http.Request, lookups []countryLookup) (string, string) {
	if len(lookups) == 0 || lookups[0] == nil {
		return "", ""
	}
	manager, ok := lookups[0].(interface {
		LookupASN(net.IP) (geoip.ASNRecord, bool, error)
	})
	if !ok {
		return "", ""
	}
	ip := net.ParseIP(requestClientIP(r.RemoteAddr))
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return "", ""
	}
	country := ""
	if record, found, err := lookups[0].LookupCountry(ip); err == nil && found {
		country = strings.TrimSpace(record.Country.ISOCode)
		if country == "" {
			country = strings.TrimSpace(record.CountryCode)
		}
	}
	asn := ""
	if record, found, err := manager.LookupASN(ip); err == nil && found && record.ASN > 0 {
		asn = strconv.FormatUint(uint64(record.ASN), 10)
	}
	return country, asn
}

func cleanLogValue(value string) string {
	return strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f {
			return ' '
		}
		if char > 0x7e {
			return '?'
		}
		return char
	}, value)
}

func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS1.0"
	case tls.VersionTLS11:
		return "TLS1.1"
	case tls.VersionTLS12:
		return "TLS1.2"
	case tls.VersionTLS13:
		return "TLS1.3"
	default:
		return ""
	}
}

func isSuccessfulHealthRequest(path string) bool {
	return isHealthRequest(path)
}
