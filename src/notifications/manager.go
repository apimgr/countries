package notifications

import (
	"context"
	"fmt"
	"html"
	"io/fs"
	"log"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/config"
)

type Manager struct {
	Config      *config.Config
	Templates   fs.FS
	OverrideDir string
	ProjectName string
	FQDN        string
	Hostname    string
	Version     string
	Mode        string
	Language    string
	Sender      Sender
	Sent        atomic.Int64
	webhookOnce  sync.Once
	webhookQueue chan webhookDelivery
	webhookCtx   context.Context
	webhookStop  context.CancelFunc
	webhookWG    sync.WaitGroup
}

var templateVariable = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)
var notificationCredentialURL = regexp.MustCompile(`(?i)(://)[^/@\s]+:[^/@\s]+@`)
var notificationCredentialQuery = regexp.MustCompile(`(?i)((?:auth[_-]?)?(?:token|password|passwd|secret|key)\s*[=:]\s*)[^&#\s,;]+`)

var templateVariables = map[string]bool{
	"app_name": true, "app_url": true, "fqdn": true, "onion_url": true, "onion_address": true,
	"i2p_url": true, "i2p_address": true, "notification_reply_to": true, "timestamp": true, "year": true,
	"event": true, "ip": true, "details": true, "filename": true, "size": true, "error": true,
	"expires_in": true, "expiry_date": true, "valid_until": true, "next_retry": true, "hostname": true,
	"version": true, "mode": true, "reason": true, "path": true, "used_percent": true, "free": true,
	"threshold_percent": true, "since": true, "task_name": true, "next_run": true, "current_version": true,
	"new_version": true, "channel": true, "previous_version": true, "command": true,
}

func (m *Manager) CanSend() bool {
	return m != nil && m.Config != nil && m.Config.Server.Notifications.Email.Enabled && m.Sender.Config.Host != "" && strings.TrimSpace(m.FQDN) != ""
}

func (m *Manager) Notify(event string, values map[string]string) error {
	if m == nil || m.Config == nil {
		return fmt.Errorf("notification manager is unavailable")
	}
	log.Printf("notification.event=%q", event)
	m.Sent.Add(1)
	webhookErr := m.NotifyRoleWebhooks("admin", event, values)
	if !m.CanSend() || !m.eventEnabled(event) {
		return webhookErr
	}
	recipient := strings.TrimSpace(m.Config.Server.Contact.Admin.Email)
	if parsed, err := mail.ParseAddress(recipient); err != nil || parsed.Address != recipient {
		return fmt.Errorf("admin notification recipient is not configured")
	}
	vars := m.globalVariables()
	for key, value := range values {
		if strings.ContainsAny(key, "{}\r\n") {
			continue
		}
		if event == "database_error" && key == "error" {
			value = sanitizeDatabaseNotificationError(value)
		}
		if key == "timestamp" || key == "since" || key == "expiry_date" || key == "valid_until" {
			value = formatNotificationTime(value)
		}
		vars[key] = value
	}
	templateText, err := m.loadTemplateForLanguage(event, m.notificationLanguage())
	if err != nil {
		return err
	}
	subject, body, err := RenderTemplate(templateText, vars)
	if err != nil {
		return err
	}
	if err := validateRenderedTemplate(subject, body); err != nil {
		return err
	}
	from := m.Config.Server.Notifications.Email.From
	if from.Name == "" {
		from.Name = vars["app_name"]
	}
	if from.Email == "" {
		from.Email = "no-reply@" + m.FQDN
	}
	textBody := strings.TrimSpace(body)
	message := Message{
		FromName: from.Name, FromEmail: from.Email, To: recipient,
		ReplyTo: m.Config.Server.Notifications.Email.ReplyTo, Subject: subject,
		Text: textBody, HTML: "<!doctype html><html><body><pre>" + html.EscapeString(textBody) + "</pre></body></html>", FQDN: m.FQDN,
	}
	if err := m.Sender.Send(message); err != nil {
		if webhookErr != nil {
			return fmt.Errorf("send %s notification: %v; webhook delivery: %w", event, err, webhookErr)
		}
		return fmt.Errorf("send %s notification: %w", event, err)
	}
	return webhookErr
}

func (m *Manager) DryRun() ([]string, error) {
	if m == nil || m.Templates == nil {
		return nil, fmt.Errorf("built-in email templates are unavailable")
	}
	names := []string{"security_alert", "backup_complete", "backup_failed", "ssl_expiring", "ssl_renewed", "ssl_renewal_failed", "scheduler_error", "startup", "shutdown", "disk_low", "database_error", "update_available", "update_installed", "update_failed", "update_staged", "test"}
	values := m.globalVariables()
	values["ip"] = "192.168.1.100"
	values["event"] = "Rate limit exceeded"
	values["details"] = "Sample event details"
	values["filename"] = "backup.tar.gz"
	values["size"] = "1.2 GB"
	values["error"] = "Sample error"
	values["expires_in"] = "7"
	values["expiry_date"] = formatNotificationTime(time.Now().Add(7 * 24 * time.Hour).Format(time.DateOnly))
	values["valid_until"] = formatNotificationTime(time.Now().Add(90 * 24 * time.Hour).Format(time.DateOnly))
	values["next_retry"] = "in one hour"
	values["reason"] = "signal"
	values["path"] = "/var/lib/countries"
	values["used_percent"] = "90"
	values["free"] = "2 GB"
	values["threshold_percent"] = "85"
	values["since"] = formatNotificationTime(time.Now().Add(-time.Minute).Format(time.RFC3339))
	values["task_name"] = "healthcheck_self"
	values["next_run"] = "in five minutes"
	values["current_version"] = m.Version
	values["new_version"] = "2.0.0"
	values["command"] = "countries --service restart"
	values["channel"] = "stable"
	values["previous_version"] = "1.9.0"
	var rendered []string
	for _, name := range names {
		text, err := m.loadTemplateForLanguage(name, m.notificationLanguage())
		if err != nil {
			return nil, err
		}
		subject, body, err := RenderTemplate(text, values)
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", name, err)
		}
		if err := validateRenderedTemplate(subject, body); err != nil {
			return nil, fmt.Errorf("template %s: %w", name, err)
		}
		rendered = append(rendered, fmt.Sprintf("Template: %s\nSubject: %s\n%s", name, subject, body))
	}
	return rendered, nil
}

func (m *Manager) SendTest(recipient string) (string, error) {
	if !m.CanSend() {
		return "", fmt.Errorf("email features require SMTP configuration")
	}
	if strings.TrimSpace(recipient) == "" {
		recipient = strings.TrimSpace(m.Config.Server.Notifications.Email.ReplyTo)
	}
	if parsed, err := mail.ParseAddress(recipient); err != nil || parsed.Address != recipient {
		return "", fmt.Errorf("a valid test recipient is required")
	}
	values := m.globalVariables()
	values["ip"] = "192.168.1.100"
	values["timestamp"] = formatNotificationTime(time.Now().Format(time.RFC3339))
	text, err := m.loadTemplateForLanguage("test", m.notificationLanguage())
	if err != nil {
		return "", err
	}
	subject, body, err := RenderTemplate(text, values)
	if err != nil {
		return "", err
	}
	subject = "[TEST] " + subject
	from := m.Config.Server.Notifications.Email.From
	if from.Name == "" {
		from.Name = values["app_name"]
	}
	if from.Email == "" {
		from.Email = "no-reply@" + m.FQDN
	}
	message := Message{FromName: from.Name, FromEmail: from.Email, To: recipient, ReplyTo: m.Config.Server.Notifications.Email.ReplyTo, Subject: subject, Text: body, HTML: "<!doctype html><html><body><pre>" + html.EscapeString(body) + "</pre></body></html>", FQDN: m.FQDN}
	if err := m.Sender.Send(message); err != nil {
		return "", err
	}
	m.Sent.Add(1)
	log.Printf("email.test sent recipient=%q template=test", recipient)
	return subject, nil
}

func (m *Manager) eventEnabled(name string) bool {
	events := m.Config.Server.Notifications.Email.Events
	switch name {
	case "startup":
		return events.Startup
	case "shutdown":
		return events.Shutdown
	case "backup_complete":
		return events.BackupComplete
	case "backup_failed":
		return events.BackupFailed
	case "ssl_expiring":
		return events.SSLExpiring
	case "ssl_renewed":
		return events.SSLRenewed
	case "ssl_renewal_failed":
		return events.SSLRenewalFailed
	case "security_alert":
		return events.SecurityAlert
	case "scheduler_error":
		return events.SchedulerError
	case "update_available":
		return events.UpdateAvailable
	case "update_installed":
		return events.UpdateInstalled
	case "update_failed":
		return events.UpdateFailed
	case "update_staged":
		return events.UpdateStaged
	case "disk_low":
		return true
	case "database_error":
		return m.Config.Server.Notifications.DatabaseError.Enabled
	case "test":
		return true
	default:
		return false
	}
}

func sanitizeDatabaseNotificationError(message string) string {
	message = notificationCredentialURL.ReplaceAllString(message, `${1}[redacted]@`)
	return notificationCredentialQuery.ReplaceAllString(message, `${1}[redacted]`)
}

func (m *Manager) globalVariables() map[string]string {
	appName := m.Config.Server.Branding.Title
	if appName == "" {
		appName = m.ProjectName
	}
	fqdn := strings.TrimSpace(m.FQDN)
	urlHost := fqdn
	if strings.Contains(urlHost, ":") && !strings.HasPrefix(urlHost, "[") {
		urlHost = "[" + urlHost + "]"
	}
	appURL := "https://" + urlHost
	if strings.HasSuffix(fqdn, ".onion") || strings.HasSuffix(fqdn, ".i2p") {
		appURL = "http://" + urlHost
	}
	return map[string]string{
		"app_name": appName, "app_url": appURL, "fqdn": fqdn,
		"onion_url": "", "onion_address": "", "i2p_url": "", "i2p_address": "",
		"notification_reply_to": m.Config.Server.Notifications.Email.ReplyTo,
		"timestamp":             formatNotificationTime(time.Now().Format(time.RFC3339)), "year": fmt.Sprint(time.Now().Year()),
		"hostname": m.Hostname, "version": m.Version, "mode": m.Mode,
	}
}

func formatNotificationTime(value string) string {
	location, err := time.LoadLocation("America/New_York")
	if configured := strings.TrimSpace(os.Getenv("TZ")); configured != "" {
		if configuredLocation, loadErr := time.LoadLocation(configured); loadErr == nil {
			location, err = configuredLocation, nil
		}
	}
	if err != nil || location == nil {
		location = time.UTC
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Now().In(location).Format("January 02, 2006 at 15:04:05 MST")
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, time.DateTime, time.DateOnly} {
		parsed, parseErr := time.ParseInLocation(layout, value, location)
		if parseErr == nil {
			return parsed.In(location).Format("January 02, 2006 at 15:04:05 MST")
		}
	}
	return value
}

func (m *Manager) loadTemplate(name string) (string, error) {
	return m.loadTemplateForLanguage(name, "en")
}

func (m *Manager) loadTemplateForLanguage(name, lang string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("notification manager is unavailable")
	}
	filename := name + ".tmpl"
	if m.OverrideDir != "" {
		path := filepath.Join(m.OverrideDir, filename)
		if data, err := os.ReadFile(path); err == nil {
			return string(data), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if translated := i18n.Translate(lang, "email.template."+name); translated != "email.template."+name {
		return translated, nil
	}
	if m.Templates == nil {
		return "", fmt.Errorf("built-in email templates are unavailable")
	}
	data, err := fs.ReadFile(m.Templates, filename)
	if err != nil {
		return "", fmt.Errorf("load built-in %s email template: %w", name, err)
	}
	return string(data), nil
}

func (m *Manager) notificationLanguage() string {
	if m == nil || m.Config == nil {
		return "en"
	}
	lang := strings.ToLower(strings.TrimSpace(m.Language))
	if lang == "" {
		lang = strings.ToLower(strings.TrimSpace(m.Config.Server.I18N.DefaultLanguage))
	}
	if lang == "" || !i18n.IsSupported(lang) {
		return "en"
	}
	available := m.Config.Server.I18N.AvailableLanguages
	if len(available) != 0 {
		for _, supported := range available {
			if strings.EqualFold(strings.TrimSpace(supported), lang) {
				return lang
			}
		}
		return "en"
	}
	return lang
}

func validateRenderedTemplate(subject, body string) error {
	if strings.TrimSpace(subject) == "" {
		return fmt.Errorf("subject cannot be empty")
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("body cannot be empty")
	}
	if strings.ContainsAny(subject, "\r\n") {
		return fmt.Errorf("template subject cannot contain CR or LF")
	}
	for _, value := range append(templateVariable.FindAllStringSubmatch(subject, -1), templateVariable.FindAllStringSubmatch(body, -1)...) {
		if !templateVariables[value[1]] {
			return fmt.Errorf("unknown variable: {%s}", value[1])
		}
	}
	return nil
}

func (m *Manager) CheckOverrides() error {
	if m.OverrideDir == "" {
		return nil
	}
	entries, err := os.ReadDir(m.OverrideDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".tmpl" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.OverrideDir, entry.Name()))
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(entry.Name(), ".tmpl")
		subject, body, err := RenderTemplate(string(data), m.globalVariables())
		if err != nil {
			return fmt.Errorf("template %s: %w", name, err)
		}
		if err := validateRenderedTemplate(subject, body); err != nil {
			return fmt.Errorf("template %s: %w", name, err)
		}
	}
	return nil
}

func (m *Manager) Logf(format string, args ...any) {
	log.Printf(format, args...)
}
