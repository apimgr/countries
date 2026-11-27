package notifications

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/countries/src/config"
)

type webhookMessage struct {
	Role       string `json:"role"`
	Event      string `json:"event"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	Severity   string `json:"severity"`
	Timestamp  string `json:"timestamp"`
	Project    string `json:"project_name"`
	Version    string `json:"project_version"`
	AppURL     string `json:"app_url"`
	TrackingID string `json:"tracking_id,omitempty"`
}

type webhookDelivery struct {
	role      string
	transport string
	endpoint  string
	secret    string
	id        string
	message   webhookMessage
}

var (
	webhookFailureMu     sync.Mutex
	webhookFailureLogged = map[string]time.Time{}
)

var webhookRetryDelays = []time.Duration{
	time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

func (m *Manager) notifyAdminWebhooks(event string, values map[string]string) error {
	return m.NotifyRoleWebhooks("admin", event, values)
}

// NotifyRoleWebhooks queues a privacy-safe summary for every configured transport
// resolved through the selected role's fallback chain.
func (m *Manager) NotifyRoleWebhooks(role, event string, values map[string]string) error {
	if m == nil || m.Config == nil {
		return fmt.Errorf("webhook manager is unavailable")
	}
	targets := resolveWebhookTargets(m.Config.Server.Contact, role)
	if len(targets) == 0 {
		return nil
	}
	now := time.Now().UTC().Truncate(time.Second)
	message := webhookMessage{
		Role: role, Event: event, Subject: webhookSubject(event, values),
		Body: webhookSummary(values), Severity: webhookSeverity(event, values),
		Timestamp: now.Format(time.RFC3339), Project: m.ProjectName,
		Version: m.Version, AppURL: webhookAppURL(m.FQDN),
		TrackingID: strings.TrimSpace(values["tracking_id"]),
	}
	if appURL := strings.TrimSpace(values["app_url"]); appURL != "" {
		message.AppURL = appURL
	}

	m.startWebhookWorkers()
	var firstErr error
	for transport, target := range targets {
		id, err := webhookUUIDv7(now)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		delivery := webhookDelivery{
			role: role, transport: transport, endpoint: target.endpoint,
			secret: target.secret, id: id, message: message,
		}
		select {
		case m.webhookQueue <- delivery:
		default:
			if firstErr == nil {
				firstErr = fmt.Errorf("webhook delivery queue is full")
			}
		}
	}
	return firstErr
}

type webhookTarget struct {
	endpoint string
	secret   string
}

func resolveWebhookTargets(contact config.ContactConfig, role string) map[string]webhookTarget {
	roles := map[string]config.PublicContactConfig{
		"admin": contact.Admin, "security": contact.Security,
		"abuse": contact.Abuse, "general": contact.General,
	}
	fallbacks := map[string][]string{
		"admin":    {"admin"},
		"security": {"security", "admin"},
		"abuse":    {"abuse", "general", "admin"},
		"general":  {"general", "admin"},
	}
	chain, ok := fallbacks[role]
	if !ok {
		return nil
	}
	result := make(map[string]webhookTarget)
	for _, fallbackRole := range chain {
		configured := roles[fallbackRole]
		for transport, endpoint := range configured.Webhooks {
			if _, exists := result[transport]; exists {
				continue
			}
			endpoint = strings.TrimSpace(endpoint)
			secret := strings.TrimSpace(configured.WebhookSecrets[transport])
			if endpoint != "" && secret != "" && !strings.HasSuffix(transport, "_secret") {
				result[transport] = webhookTarget{endpoint: endpoint, secret: secret}
			}
		}
	}
	return result
}

func webhookSummary(values map[string]string) string {
	allowed := map[string]bool{
		"component": true, "tracking_id": true, "severity": true,
		"reason": true, "error": true, "version": true, "report_url": true,
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if allowed[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var body strings.Builder
	for _, key := range keys {
		value := safeWebhookText(values[key])
		value = notificationCredentialURL.ReplaceAllString(value, `${1}[redacted]@`)
		value = notificationCredentialQuery.ReplaceAllString(value, `${1}[redacted]`)
		fmt.Fprintf(&body, "%s: %s\n", key, value)
	}
	return strings.TrimSpace(body.String())
}

func webhookSeverity(event string, values map[string]string) string {
	if severity := strings.ToLower(strings.TrimSpace(values["severity"])); severity == "info" || severity == "warning" || severity == "critical" {
		return severity
	}
	lowerEvent := strings.ToLower(event)
	if strings.Contains(lowerEvent, "failed") || strings.Contains(lowerEvent, "error") || strings.Contains(lowerEvent, "alert") {
		return "critical"
	}
	if strings.Contains(lowerEvent, "warning") {
		return "warning"
	}
	return "info"
}

func webhookSubject(event string, values map[string]string) string {
	if subject := safeWebhookText(values["subject"]); subject != "" {
		return subject
	}
	return strings.ReplaceAll(strings.TrimSpace(event), "_", " ")
}

func safeWebhookText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 || (r < 0x20 && r != '\t') {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	runes := []rune(value)
	if len(runes) > 200 {
		value = string(runes[:200])
	}
	return value
}

func webhookAppURL(fqdn string) string {
	fqdn = strings.TrimSpace(fqdn)
	if fqdn == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(fqdn), ".onion") || strings.HasSuffix(strings.ToLower(fqdn), ".i2p") {
		return "http://" + fqdn
	}
	return "https://" + fqdn
}

func (m *Manager) startWebhookWorkers() {
	m.webhookOnce.Do(func() {
		m.webhookCtx, m.webhookStop = context.WithCancel(context.Background())
		m.webhookQueue = make(chan webhookDelivery, 128)
		for range 4 {
			m.webhookWG.Add(1)
			go m.runWebhookWorker()
		}
	})
}

func (m *Manager) runWebhookWorker() {
	defer m.webhookWG.Done()
	for {
		select {
		case <-m.webhookCtx.Done():
			return
		case delivery := <-m.webhookQueue:
			m.deliverWebhook(delivery)
		}
	}
}

func (m *Manager) deliverWebhook(delivery webhookDelivery) {
	var lastErr error
	for attempt := 0; attempt <= len(webhookRetryDelays); attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(webhookRetryDelays[attempt-1])
			select {
			case <-m.webhookCtx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		if err := sendWebhook(m.webhookCtx, delivery.transport, delivery.endpoint, delivery.secret, delivery.id, delivery.message); err == nil {
			return
		} else {
			lastErr = err
		}
	}
	logWebhookFailure(delivery.role, delivery.transport, delivery.message.Event, lastErr)
}

func logWebhookFailure(role, transport, event string, err error) {
	key := role + "." + transport
	now := time.Now().UTC()
	webhookFailureMu.Lock()
	defer webhookFailureMu.Unlock()
	if previous := webhookFailureLogged[key]; !previous.IsZero() && now.Sub(previous) < time.Minute {
		return
	}
	webhookFailureLogged[key] = now
	log.Printf("notify.webhook_failed role=%q transport=%q event=%q error=%q", role, transport, event, err.Error())
}

// Close cancels queued webhook retries and waits for their bounded workers to exit.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.webhookOnce.Do(func() {})
	if m.webhookStop != nil {
		m.webhookStop()
		m.webhookWG.Wait()
	}
}

func sendWebhook(ctx context.Context, transport, endpoint, secret, id string, message webhookMessage) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("webhook endpoint must be an HTTPS URL")
	}
	var payload []byte
	contentType := "application/json"
	switch transport {
	case "telegram":
		query := parsed.Query()
		query.Set("text", message.Subject+"\n"+message.Body)
		parsed.RawQuery = query.Encode()
		payload = []byte{}
	case "discord":
		payload, err = json.Marshal(map[string]string{"content": message.Subject + "\n" + message.Body, "username": message.Project})
	case "slack", "mattermost":
		payload, err = json.Marshal(map[string]string{"text": message.Subject + "\n" + message.Body})
	case "pushover":
		query := parsed.Query()
		form := url.Values{"user": {query.Get("user")}, "token": {query.Get("token")}, "title": {message.Subject}, "message": {message.Body}, "priority": {"0"}}
		if message.Severity == "critical" {
			form.Set("priority", "1")
		}
		query.Del("user")
		query.Del("token")
		parsed.RawQuery = query.Encode()
		payload = []byte(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	case "gotify":
		parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/message"
		priority := 2
		switch message.Severity {
		case "warning":
			priority = 5
		case "critical":
			priority = 8
		}
		payload, err = json.Marshal(map[string]interface{}{"title": message.Subject, "message": message.Body, "priority": priority})
	default:
		if transport != "generic" {
			return fmt.Errorf("unsupported webhook transport %q", transport)
		}
		payload, err = json.Marshal(message)
	}
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write(payload); err != nil {
		return errors.New("sign webhook payload")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, parsed.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	request.Header.Set("X-Webhook-Timestamp", fmt.Sprint(time.Now().UTC().Unix()))
	request.Header.Set("X-Webhook-ID", id)
	request.Header.Set("X-Webhook-Event", message.Event)
	request.Header.Set("User-Agent", message.Project+"/"+message.Version+" (+"+message.AppURL+")")
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("webhook request failed")
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			log.Printf("notify.webhook_response_close_failed transport=%q", transport)
		}
	}()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
		return errors.New("read webhook response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", response.StatusCode)
	}
	return nil
}

func webhookUUIDv7(now time.Time) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	millis := uint64(now.UnixMilli())
	value[0] = byte(millis >> 40)
	value[1] = byte(millis >> 32)
	value[2] = byte(millis >> 24)
	value[3] = byte(millis >> 16)
	value[4] = byte(millis >> 8)
	value[5] = byte(millis)
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
