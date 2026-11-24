// Package auditlog writes append-only, tamper-evident JSONL audit events.
package auditlog

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var writeMu sync.Mutex

type Actor struct {
	Type string  `json:"type"`
	ID   string  `json:"id,omitempty"`
	IP   *string `json:"ip"`
}

type Target struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Event struct {
	ID       string         `json:"id"`
	Time     time.Time      `json:"time"`
	Event    string         `json:"event"`
	Category string         `json:"category"`
	Severity string         `json:"severity"`
	Actor    Actor          `json:"actor"`
	Target   *Target        `json:"target,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
	Result   string         `json:"result"`
	Source   string         `json:"source,omitempty"`
	Previous string         `json:"previous_checksum"`
	Checksum string         `json:"checksum"`
}

type hashInput struct {
	ID       string         `json:"id"`
	Time     time.Time      `json:"time"`
	Event    string         `json:"event"`
	Category string         `json:"category"`
	Severity string         `json:"severity"`
	Actor    Actor          `json:"actor"`
	Target   *Target        `json:"target,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
	Result   string         `json:"result"`
	Source   string         `json:"source,omitempty"`
	Previous string         `json:"previous_checksum"`
}

// Write appends one audit event. System events may use a nil IP when no client
// address exists; request events must provide the observed client address.
func Write(logDir string, event Event, now time.Time) error {
	if strings.TrimSpace(logDir) == "" || strings.TrimSpace(event.Event) == "" || strings.TrimSpace(event.Category) == "" || strings.TrimSpace(event.Severity) == "" || strings.TrimSpace(event.Actor.Type) == "" || strings.TrimSpace(event.Result) == "" {
		return errors.New("audit event is missing a required field")
	}
	if event.Actor.Type != "system" && (event.Actor.IP == nil || strings.TrimSpace(*event.Actor.IP) == "") {
		return errors.New("request audit event is missing its actor IP")
	}
	if now.IsZero() {
		now = time.Now()
	}
	eventID, err := newULID(now)
	if err != nil {
		return err
	}
	event.ID = eventID
	event.Time = now.UTC().Truncate(time.Millisecond)
	path := filepath.Join(logDir, "audit.log")
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := os.MkdirAll(logDir, 0750); err != nil {
		return fmt.Errorf("create audit log directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("audit log path is not a regular file")
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect audit log: %w", err)
	}
	previous, err := lastChecksum(path)
	if err != nil {
		return err
	}
	event.Previous = previous
	encoded, err := json.Marshal(hashInput{
		ID: event.ID, Time: event.Time, Event: event.Event, Category: event.Category,
		Severity: event.Severity, Actor: event.Actor, Target: event.Target,
		Details: event.Details, Result: event.Result, Source: event.Source, Previous: event.Previous,
	})
	if err != nil {
		return fmt.Errorf("encode audit event for checksum: %w", err)
	}
	checksum := sha256.Sum256(encoded)
	event.Checksum = hex.EncodeToString(checksum[:])
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode audit event: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	if err := file.Chmod(0640); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure audit log: %w", err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("append audit event: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync audit event: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close audit log: %w", err)
	}
	return nil
}

// Verify checks the event checksum chain and reports any modified or truncated line.
func Verify(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	previous := ""
	lineNumber := 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineNumber++
			if line[len(line)-1] != '\n' {
				return fmt.Errorf("audit log line %d is incomplete", lineNumber)
			}
			var event Event
			if err := json.Unmarshal(line, &event); err != nil {
				return fmt.Errorf("decode audit log line %d: %w", lineNumber, err)
			}
			if event.Previous != previous {
				return fmt.Errorf("audit log line %d does not follow the previous checksum", lineNumber)
			}
			encoded, err := json.Marshal(hashInput{
				ID: event.ID, Time: event.Time, Event: event.Event, Category: event.Category,
				Severity: event.Severity, Actor: event.Actor, Target: event.Target,
				Details: event.Details, Result: event.Result, Source: event.Source, Previous: event.Previous,
			})
			if err != nil {
				return fmt.Errorf("encode audit log line %d: %w", lineNumber, err)
			}
			checksum := sha256.Sum256(encoded)
			if event.Checksum != hex.EncodeToString(checksum[:]) {
				return fmt.Errorf("audit log line %d checksum mismatch", lineNumber)
			}
			previous = event.Checksum
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read audit log: %w", readErr)
		}
	}
	return nil
}

func lastChecksum(path string) (string, error) {
	if _, err := os.Stat(path); err == nil {
		if err := Verify(path); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect audit log for chaining: %w", err)
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open audit log for chaining: %w", err)
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	previous := ""
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if line[len(line)-1] != '\n' {
				return "", errors.New("audit log ends with an incomplete line")
			}
			var item struct {
				Previous string `json:"previous_checksum"`
				Checksum string `json:"checksum"`
			}
			if err := json.Unmarshal(line, &item); err != nil {
				return "", fmt.Errorf("decode last audit event: %w", err)
			}
			if item.Previous != previous || item.Checksum == "" {
				return "", errors.New("audit log checksum chain is invalid")
			}
			previous = item.Checksum
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("read audit log for chaining: %w", readErr)
		}
	}
	return previous, nil
}

func newULID(now time.Time) (string, error) {
	var raw [16]byte
	milliseconds := uint64(now.UnixMilli())
	raw[0] = byte(milliseconds >> 40)
	raw[1] = byte(milliseconds >> 32)
	raw[2] = byte(milliseconds >> 24)
	raw[3] = byte(milliseconds >> 16)
	raw[4] = byte(milliseconds >> 8)
	raw[5] = byte(milliseconds)
	if _, err := rand.Read(raw[6:]); err != nil {
		return "", fmt.Errorf("generate audit event ID: %w", err)
	}
	return base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding).EncodeToString(raw[:]), nil
}
