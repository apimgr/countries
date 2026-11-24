// Package logbuffer stores bounded, sanitized application log events.
package logbuffer

import (
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

const bufferCapacity = 4096

var (
	secretAssignment = regexp.MustCompile(`(?i)(["']?(?:password|passwd|token|secret|api[_-]?key|client[_-]?secret|encryption[_-]?key|captcha[_-]?secret[_-]?key|security_id)["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,}]+)`)
	bearerCredential = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]+`)
	metricCredential = regexp.MustCompile(`\bmtr_[A-Za-z0-9]{32}\b`)
	urlCredential    = regexp.MustCompile(`(?i)(://)[^/@\s:]+:[^/@\s]+@`)
	levelPattern     = regexp.MustCompile(`(?i)\b(error|warn(?:ing)?|info|debug)\b`)
)

// Entry is a sanitized application log event held for the authenticated Loki endpoint.
type Entry struct {
	Time    time.Time
	Level   string
	Message string
}

// Buffer stores a bounded, concurrency-safe set of sanitized log events.
type Buffer struct {
	mu      sync.RWMutex
	entries []Entry
}

// New creates an empty bounded log buffer.
func New() *Buffer {
	return &Buffer{entries: make([]Entry, 0, bufferCapacity)}
}

// Add stores one event after redacting credential-like values.
func (b *Buffer) Add(level, message string) {
	message = sanitize(message)
	if message == "" {
		return
	}
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "error", "warn", "warning", "info", "debug":
	default:
		level = "info"
	}
	if level == "warning" {
		level = "warn"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.entries) == bufferCapacity {
		copy(b.entries, b.entries[1:])
		b.entries = b.entries[:bufferCapacity-1]
	}
	b.entries = append(b.entries, Entry{Time: time.Now(), Level: level, Message: message})
}

// Recent returns newest eligible events, in chronological order.
func (b *Buffer) Recent(maxEntries int, maxAge time.Duration) []Entry {
	if maxEntries < 1 {
		return []Entry{}
	}
	if maxEntries > bufferCapacity {
		maxEntries = bufferCapacity
	}
	cutoff := time.Time{}
	if maxAge > 0 {
		cutoff = time.Now().Add(-maxAge)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	entries := make([]Entry, 0, min(maxEntries, len(b.entries)))
	for index := len(b.entries) - 1; index >= 0 && len(entries) < maxEntries; index-- {
		entry := b.entries[index]
		if !cutoff.IsZero() && entry.Time.Before(cutoff) {
			continue
		}
		entries = append(entries, entry)
	}
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
	return entries
}

// Writer tees sanitized standard-library log output into the buffer.
type Writer struct {
	Buffer *Buffer
	Output io.Writer
}

func (w Writer) Write(data []byte) (int, error) {
	text := sanitize(string(data))
	if w.Output != nil {
		if _, err := io.WriteString(w.Output, text); err != nil {
			return 0, err
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		w.Buffer.Add(detectLevel(line), line)
	}
	return len(data), nil
}

var defaultBuffer = New()

// Default returns the process-wide log buffer.
func Default() *Buffer { return defaultBuffer }

// Install captures standard-library logs and emits only sanitized output.
func Install() {
	log.SetOutput(Writer{Buffer: defaultBuffer, Output: os.Stderr})
}

func detectLevel(message string) string {
	match := levelPattern.FindStringSubmatch(message)
	if len(match) != 2 {
		return "info"
	}
	return strings.ToLower(match[1])
}

func sanitize(message string) string {
	message = strings.Map(func(value rune) rune {
		if unicode.IsControl(value) && value != '\n' {
			return ' '
		}
		return value
	}, message)
	message = secretAssignment.ReplaceAllString(message, "$1[redacted]")
	message = bearerCredential.ReplaceAllString(message, "$1[redacted]")
	message = metricCredential.ReplaceAllString(message, "[redacted]")
	message = urlCredential.ReplaceAllString(message, "$1[redacted]@")
	return strings.TrimSpace(message)
}
