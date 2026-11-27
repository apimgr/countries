// Package logging provides the server's per-kind rotating log files.
package logging

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/apimgr/countries/src/config"
)

type Event struct {
	Time    time.Time
	Level   string
	Kind    string
	Message string
	Fields  map[string]any
}

type file struct {
	path, kind, format, rotate, keep string
	handle                           *os.File
	opened                           time.Time
}

type Manager struct {
	mu       sync.Mutex
	level    string
	files    map[string]*file
	closed   bool
	rotation int
}

// Open prepares the enabled server, error, security, and debug logs.
func Open(dir string, cfg config.LoggingConfig, mode string) (*Manager, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("log directory is empty")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, fmt.Errorf("protect log directory: %w", err)
	}
	level := strings.ToLower(strings.TrimSpace(cfg.Level))
	if level == "" {
		level = "info"
		if mode == "development" || mode == "debug" {
			level = "debug"
		}
	}
	if _, ok := ranks[level]; !ok {
		return nil, fmt.Errorf("invalid log level %q", level)
	}
	m := &Manager{level: level, files: map[string]*file{}}
	settings := map[string]struct {
		name, format, rotate, keep string
		enabled                    bool
	}{
		"server":   {cfg.Server.Filename, fallback(cfg.Server.Format, "text"), cfg.Server.Rotate, cfg.Server.Keep, true},
		"error":    {cfg.Error.Filename, fallback(cfg.Error.Format, "text"), cfg.Error.Rotate, cfg.Error.Keep, true},
		"security": {cfg.Security.Filename, fallback(cfg.Security.Format, "fail2ban"), cfg.Security.Rotate, cfg.Security.Keep, true},
		"debug":    {cfg.Debug.Filename, fallback(cfg.Debug.Format, "text"), cfg.Debug.Rotate, cfg.Debug.Keep, cfg.Debug.Enabled},
	}
	for kind, opt := range settings {
		if !opt.enabled {
			continue
		}
		name := opt.name
		if name == "" {
			name = kind + ".log"
		}
		if filepath.Base(name) != name || name == "." || name == ".." {
			_ = m.Close()
			return nil, fmt.Errorf("invalid %s log filename %q", kind, name)
		}
		if err := validFormat(kind, opt.format); err != nil {
			_ = m.Close()
			return nil, err
		}
		rotate := opt.rotate
		if rotate == "" {
			rotate = cfg.Defaults.Rotate
		}
		if rotate == "" {
			rotate = "weekly,50MB"
		}
		keep := opt.keep
		if keep == "" {
			keep = cfg.Defaults.Keep
		}
		if keep == "" {
			keep = "none"
		}
		if _, err := parseRotation(rotate); err != nil {
			_ = m.Close()
			return nil, fmt.Errorf("invalid %s rotation: %w", kind, err)
		}
		if err := validRetention(keep); err != nil {
			_ = m.Close()
			return nil, fmt.Errorf("invalid %s retention: %w", kind, err)
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			_ = m.Close()
			return nil, fmt.Errorf("open %s log: %w", kind, err)
		}
		if err := f.Chmod(0600); err != nil {
			_ = f.Close()
			_ = m.Close()
			return nil, err
		}
		st, _ := f.Stat()
		opened := time.Now()
		if st != nil {
			opened = st.ModTime()
		}
		m.files[kind] = &file{path: path, kind: kind, format: opt.format, rotate: rotate, keep: keep, handle: f, opened: opened}
	}
	return m, nil
}

// Write accepts standard-library logger output and classifies it for file output.
func (m *Manager) Write(p []byte) (int, error) {
	line := clean(strings.TrimSpace(string(p)))
	if line == "" {
		return len(p), nil
	}
	level := infer(line)
	kind := "server"
	low := strings.ToLower(line)
	if strings.Contains(low, "security.") || strings.Contains(low, "auth.") || strings.Contains(low, "authentication") || strings.Contains(low, "rate limit") {
		kind = "security"
	}
	return len(p), m.Log(Event{Time: time.Now(), Level: level, Kind: kind, Message: line})
}

// Log writes an event to its selected kind; security is mirrored to server and
// errors to error.log. Debug events also reach debug.log when it is enabled.
func (m *Manager) Log(e Event) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Level = normalize(e.Level)
	e.Kind = strings.ToLower(strings.TrimSpace(e.Kind))
	if e.Kind == "" {
		e.Kind = "server"
	}
	if e.Kind != "server" && e.Kind != "error" && e.Kind != "security" && e.Kind != "debug" {
		return fmt.Errorf("unknown log kind %q", e.Kind)
	}
	if ranks[e.Level] < ranks[m.level] {
		return nil
	}
	e.Message = clean(e.Message)
	cleanFields := map[string]any{}
	for k, v := range e.Fields {
		if !sensitive(k) {
			cleanFields[k] = clean(fmt.Sprint(v))
		}
	}
	e.Fields = cleanFields
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return os.ErrClosed
	}
	kinds := []string{e.Kind}
	if e.Kind == "security" {
		kinds = append(kinds, "server")
	}
	if e.Level == "error" && e.Kind != "error" {
		kinds = append(kinds, "error")
	}
	if e.Level == "debug" && e.Kind != "debug" {
		kinds = append(kinds, "debug")
	}
	var errs []error
	for _, kind := range kinds {
		if f := m.files[kind]; f != nil {
			if err := m.write(f, e); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	var errs []error
	for _, f := range m.files {
		if err := f.handle.Sync(); err != nil {
			errs = append(errs, err)
		}
		if err := f.handle.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) write(f *file, e Event) error {
	if err := m.rotate(f, e.Time); err != nil {
		return err
	}
	var line string
	switch f.format {
	case "text":
		line = e.Time.Format(time.RFC3339) + " [" + strings.ToUpper(e.Level) + "] " + e.Message
	case "json":
		fields := map[string]any{"time": e.Time.UTC().Format(time.RFC3339Nano), "level": strings.ToUpper(e.Level), "msg": e.Message}
		for k, v := range e.Fields {
			fields[k] = v
		}
		b, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		line = string(b)
	case "fail2ban":
		line = e.Time.Format(time.RFC3339) + " [security] " + e.Message
	case "syslog":
		line = fmt.Sprintf("<%d>1 %s countries - - - %s", priority(e.Level), e.Time.UTC().Format(time.RFC3339), e.Message)
	case "cef":
		line = fmt.Sprintf("CEF:0|apimgr|countries|1|%s|%s|%d|msg=%s", escape(e.Kind), escape(e.Message), cefSeverity(e.Level), escape(e.Message))
	}
	if _, err := io.WriteString(f.handle, line+"\n"); err != nil {
		return fmt.Errorf("write %s log: %w", f.kind, err)
	}
	return nil
}

func (m *Manager) rotate(f *file, now time.Time) error {
	p, err := parseRotation(f.rotate)
	if err != nil {
		return err
	}
	st, err := f.handle.Stat()
	if err != nil {
		return err
	}
	due := p.period != "never" && elapsed(p.period, f.opened, now)
	if p.size > 0 && st.Size() >= p.size {
		due = true
	}
	if !due || st.Size() == 0 {
		return nil
	}
	if err := f.handle.Sync(); err != nil {
		return err
	}
	if err := f.handle.Close(); err != nil {
		return err
	}
	m.rotation++
	rotated := fmt.Sprintf("%s.%s.%03d", f.path, now.UTC().Format("20060102T150405Z"), m.rotation)
	if err := os.Rename(f.path, rotated); err != nil {
		return err
	}
	f.handle, err = os.OpenFile(f.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.handle.Chmod(0600); err != nil {
		return err
	}
	f.opened = now
	return prune(f.path, f.keep)
}

var ranks = map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "warning" {
		s = "warn"
	}
	if _, ok := ranks[s]; !ok {
		return "info"
	}
	return s
}
func infer(s string) string {
	u := strings.ToUpper(s)
	if strings.Contains(u, "FATAL") || strings.Contains(u, "[ERROR]") || strings.HasPrefix(u, "ERROR:") || strings.Contains(u, " FAILED") || strings.Contains(u, " FAILURE") || strings.Contains(u, "ERROR:") {
		return "error"
	}
	if strings.Contains(u, "WARN") {
		return "warn"
	}
	if strings.Contains(u, "DEBUG") {
		return "debug"
	}
	return "info"
}
func fallback(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return strings.ToLower(strings.TrimSpace(s))
}
func validFormat(kind, format string) error {
	allowed := map[string]string{"server": "text json", "error": "text json", "debug": "text json", "security": "fail2ban syslog cef json text"}
	if !strings.Contains(" "+allowed[kind]+" ", " "+strings.ToLower(format)+" ") {
		return fmt.Errorf("unsupported %s log format %q", kind, format)
	}
	return nil
}
func sensitive(s string) bool {
	s = strings.ToLower(s)
	for _, part := range []string{"password", "secret", "token", "authorization", "credential", "private_key", "api_key"} {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func clean(s string) string {
	s = ansi.ReplaceAllString(s, "")
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteByte(' ')
			continue
		}
		if r < 0x20 || r == 0x7f || !unicode.IsPrint(r) {
			continue
		}
		if r > 0x7e {
			b.WriteByte('?')
		} else {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
func escape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "=", "\\=")
}
func priority(s string) int {
	return 16 + map[string]int{"error": 3, "warn": 4, "info": 6, "debug": 7}[s]
}
func cefSeverity(s string) int {
	return map[string]int{"error": 10, "warn": 6, "info": 3, "debug": 1}[s]
}

type rotationPolicy struct {
	period string
	size   int64
}

func parseRotation(s string) (rotationPolicy, error) {
	var p rotationPolicy
	for _, part := range strings.Split(strings.ToLower(strings.TrimSpace(s)), ",") {
		part = strings.TrimSpace(part)
		switch part {
		case "never", "daily", "weekly", "monthly", "yearly":
			p.period = part
		case "":
		default:
			var mult int64
			var suffix string
			if strings.HasSuffix(part, "mb") {
				mult = 1 << 20
				suffix = "mb"
			} else if strings.HasSuffix(part, "gb") {
				mult = 1 << 30
				suffix = "gb"
			} else {
				return p, fmt.Errorf("unsupported rotation option %q", part)
			}
			n, err := strconv.ParseInt(strings.TrimSuffix(part, suffix), 10, 64)
			if err != nil || n < 1 {
				return p, fmt.Errorf("invalid rotation size %q", part)
			}
			p.size = n * mult
		}
	}
	if p.period == "" && p.size == 0 {
		return p, errors.New("empty rotation policy")
	}
	return p, nil
}
func elapsed(period string, a, b time.Time) bool {
	switch period {
	case "daily":
		return a.Year() != b.Year() || a.YearDay() != b.YearDay()
	case "weekly":
		y1, w1 := a.ISOWeek()
		y2, w2 := b.ISOWeek()
		return y1 != y2 || w1 != w2
	case "monthly":
		return a.Year() != b.Year() || a.Month() != b.Month()
	case "yearly":
		return a.Year() != b.Year()
	}
	return false
}
func validRetention(s string) error {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "none" || s == "forever" {
		return nil
	}
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) != 2 {
			return fmt.Errorf("invalid retention %q", s)
		}
		s = parts[1]
	}
	if n, e := strconv.Atoi(s); e == nil && n >= 0 {
		return nil
	}
	if len(s) < 2 {
		return fmt.Errorf("invalid retention %q", s)
	}
	n, e := strconv.Atoi(s[:len(s)-1])
	if e != nil || n < 0 || !strings.Contains("dwm", s[len(s)-1:]) {
		return fmt.Errorf("invalid retention %q", s)
	}
	return nil
}
func prune(path, keep string) error {
	entries, err := filepath.Glob(filepath.Join(filepath.Dir(path), filepath.Base(path)+".*"))
	if err != nil {
		return err
	}
	sort.Strings(entries)
	keep = strings.ToLower(strings.TrimSpace(keep))
	if keep == "forever" {
		return nil
	}
	if keep == "none" {
		for _, p := range entries {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	if strings.Contains(keep, ":") {
		keep = strings.SplitN(keep, ":", 2)[1]
	}
	if n, e := strconv.Atoi(keep); e == nil {
		for len(entries) > n {
			if err := os.Remove(entries[0]); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			entries = entries[1:]
		}
		return nil
	}
	n, e := strconv.Atoi(keep[:len(keep)-1])
	if e != nil {
		return e
	}
	unit := keep[len(keep)-1]
	duration := time.Duration(n) * 24 * time.Hour
	if unit == 'w' {
		duration *= 7
	}
	if unit == 'm' {
		duration *= 30
	}
	cutoff := time.Now().Add(-duration)
	for _, p := range entries {
		if st, e := os.Stat(p); e == nil && st.ModTime().Before(cutoff) {
			if e := os.Remove(p); e != nil && !errors.Is(e, os.ErrNotExist) {
				return e
			}
		}
	}
	return nil
}
