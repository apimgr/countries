package notifications

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/testutil"
)

func TestManagerAvailabilityAndEvents(t *testing.T) {
	if (*Manager)(nil).CanSend() {
		t.Fatal("nil manager can send")
	}
	if (&Manager{}).CanSend() {
		t.Fatal("manager without config can send")
	}
	cfg := config.DefaultConfig()
	manager := &Manager{Config: cfg, Sender: Sender{Config: SMTPConfig{Host: "relay.example"}}, FQDN: "countries.example"}
	if manager.CanSend() {
		t.Fatal("manager with email disabled can send")
	}
	cfg.Server.Notifications.Email.Enabled = true
	if !manager.CanSend() {
		t.Fatal("configured manager cannot send")
	}
	manager.Sender.Config.Host = ""
	if manager.CanSend() {
		t.Fatal("manager without SMTP host can send")
	}
	cfg.Server.Notifications.Email.Enabled = false
	manager.Sender.Config.Host = "relay.example"

	for _, test := range []struct {
		name  string
		set   func(*config.NotificationEmailEventsConfig)
		event string
		want  bool
	}{
		{"startup", func(e *config.NotificationEmailEventsConfig) { e.Startup = true }, "startup", true},
		{"shutdown", func(e *config.NotificationEmailEventsConfig) { e.Shutdown = true }, "shutdown", true},
		{"backup complete", func(e *config.NotificationEmailEventsConfig) { e.BackupComplete = true }, "backup_complete", true},
		{"backup failed", func(e *config.NotificationEmailEventsConfig) { e.BackupFailed = true }, "backup_failed", true},
		{"SSL expiring", func(e *config.NotificationEmailEventsConfig) { e.SSLExpiring = true }, "ssl_expiring", true},
		{"SSL renewed", func(e *config.NotificationEmailEventsConfig) { e.SSLRenewed = true }, "ssl_renewed", true},
		{"SSL renewal failed", func(e *config.NotificationEmailEventsConfig) { e.SSLRenewalFailed = true }, "ssl_renewal_failed", true},
		{"security alert", func(e *config.NotificationEmailEventsConfig) { e.SecurityAlert = true }, "security_alert", true},
		{"scheduler error", func(e *config.NotificationEmailEventsConfig) { e.SchedulerError = true }, "scheduler_error", true},
		{"update available", func(e *config.NotificationEmailEventsConfig) { e.UpdateAvailable = true }, "update_available", true},
		{"update installed", func(e *config.NotificationEmailEventsConfig) { e.UpdateInstalled = true }, "update_installed", true},
		{"update failed", func(e *config.NotificationEmailEventsConfig) { e.UpdateFailed = true }, "update_failed", true},
		{"update staged", func(e *config.NotificationEmailEventsConfig) { e.UpdateStaged = true }, "update_staged", true},
		{"disk low", func(*config.NotificationEmailEventsConfig) {}, "disk_low", true},
		{"database error", func(*config.NotificationEmailEventsConfig) {}, "database_error", true},
		{"test", func(*config.NotificationEmailEventsConfig) {}, "test", true},
		{"unknown", func(*config.NotificationEmailEventsConfig) {}, "unknown", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg.Server.Notifications.Email.Events = config.NotificationEmailEventsConfig{}
			test.set(&cfg.Server.Notifications.Email.Events)
			if got := manager.eventEnabled(test.event); got != test.want {
				t.Errorf("eventEnabled(%q) = %t, want %t", test.event, got, test.want)
			}
		})
	}
	cfg.Server.Notifications.DatabaseError.Enabled = false
	if manager.eventEnabled("database_error") {
		t.Fatal("database_error event enabled while its notification is disabled")
	}
	cfg.Server.Notifications.DatabaseError.Enabled = true
	if !manager.eventEnabled("database_error") {
		t.Fatal("database_error event disabled with its notification enabled")
	}
	message := sanitizeDatabaseNotificationError("connect postgres://operator:" + "secret@db.example/x?token=abc")
	if strings.Contains(message, "operator") || strings.Contains(message, "secret") || strings.Contains(message, "abc") {
		t.Fatalf("database notification error contains credentials: %q", message)
	}
}

func TestManagerGlobalVariablesAndTemplateResolution(t *testing.T) {
	cfg := config.DefaultConfig()
	manager := &Manager{Config: cfg, ProjectName: "countries", FQDN: "countries.example", Hostname: "node-1", Version: "1.2.3", Mode: "production"}
	values := manager.globalVariables()
	if values["app_name"] != "Countries API" || values["app_url"] != "https://countries.example" || values["fqdn"] != "countries.example" || values["version"] != "1.2.3" {
		t.Fatalf("globalVariables() = %#v", values)
	}
	cfg.Server.Branding.Title = ""
	if got := manager.globalVariables()["app_name"]; got != "countries" {
		t.Fatalf("project name fallback = %q", got)
	}
	cfg.Server.Branding.Title = "Country API"
	manager.FQDN = "abc.onion"
	values = manager.globalVariables()
	if values["app_name"] != "Country API" || values["app_url"] != "http://abc.onion" {
		t.Fatalf("branded overlay variables = %#v", values)
	}
	manager.FQDN = ""
	if got := manager.globalVariables()["fqdn"]; got != "" || manager.globalVariables()["app_url"] != "https://" {
		t.Fatalf("empty FQDN must remain unresolved, got %#v", manager.globalVariables())
	}
	if manager.CanSend() {
		t.Fatal("manager without a resolved FQDN can send email")
	}

	manager.Templates = fstest.MapFS{"event.tmpl": &fstest.MapFile{Data: []byte("Subject: Built in\n---\nBuilt in body")}}
	if got, err := manager.loadTemplate("event"); err != nil || !strings.Contains(got, "Built in body") {
		t.Fatalf("loadTemplate(built-in) = %q, %v", got, err)
	}
	if _, err := manager.loadTemplate("missing"); err == nil {
		t.Fatal("loadTemplate accepted a missing built-in template")
	}
	manager.Templates = nil
	if _, err := manager.loadTemplate("event"); err == nil {
		t.Fatal("loadTemplate accepted an absent template filesystem")
	}
	if _, err := (*Manager)(nil).loadTemplate("event"); err == nil {
		t.Fatal("nil manager loadTemplate succeeded")
	}

	directory := testutil.TempDir(t)
	manager.OverrideDir = directory
	if err := os.WriteFile(filepath.Join(directory, "event.tmpl"), []byte("Subject: Override\n---\nCustom body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := manager.loadTemplate("event"); err != nil || !strings.Contains(got, "Custom body") {
		t.Fatalf("loadTemplate(override) = %q, %v", got, err)
	}
	manager.OverrideDir = filepath.Join(directory, "missing")
	if err := manager.CheckOverrides(); err != nil {
		t.Fatalf("CheckOverrides(missing directory) = %v", err)
	}
}

func TestManagerEmailTemplateLanguageAndPlaceholderResolution(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.I18N.DefaultLanguage = "en"
	manager := &Manager{Config: cfg, Templates: managerTemplates(), ProjectName: "countries", FQDN: "countries.example"}
	manager.Language = "fr"
	templateText, err := manager.loadTemplateForLanguage("backup_complete", manager.notificationLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(templateText, "Sauvegarde terminée") || !strings.Contains(templateText, "{filename}") || !strings.Contains(templateText, "{app_name}") {
		t.Fatalf("localized template lost translation or placeholders: %q", templateText)
	}
	subject, body, err := RenderTemplate(templateText, map[string]string{
		"app_name": "Countries", "fqdn": "countries.example", "timestamp": "now",
		"filename": "daily.tar", "size": "1 MB", "app_url": "https://countries.example",
	})
	if err != nil || !strings.Contains(subject, "Countries") || !strings.Contains(body, "daily.tar") {
		t.Fatalf("localized template render = (%q, %q, %v)", subject, body, err)
	}
	manager.Language = "unsupported"
	if got := manager.notificationLanguage(); got != "en" {
		t.Fatalf("unsupported language resolved to %q, want en", got)
	}
	cfg.Server.I18N.AvailableLanguages = []string{"en"}
	manager.Language = "fr"
	if got := manager.notificationLanguage(); got != "en" {
		t.Fatalf("unavailable language resolved to %q, want en", got)
	}
}

func TestEmailTemplatesExistInEverySupportedLanguage(t *testing.T) {
	cfg := config.DefaultConfig()
	manager := &Manager{Config: cfg, Templates: managerTemplates()}
	names := []string{"backup_complete", "backup_failed", "database_error", "disk_low", "scheduler_error", "security_alert", "shutdown", "ssl_expiring", "ssl_renewal_failed", "ssl_renewed", "startup", "test", "update_available", "update_failed", "update_installed", "update_staged"}
	for _, lang := range []string{"ar", "de", "en", "es", "fr", "it", "ja", "zh"} {
		for _, name := range names {
			translated, err := manager.loadTemplateForLanguage(name, lang)
			if err != nil {
				t.Fatalf("loadTemplateForLanguage(%q, %q): %v", name, lang, err)
			}
			base, err := manager.loadTemplate(name)
			if err != nil {
				t.Fatalf("loadTemplate(%q): %v", name, err)
			}
			want := make(map[string]bool)
			for _, match := range templateVariable.FindAllStringSubmatch(base, -1) {
				want[match[1]] = true
			}
			got := make(map[string]bool)
			for _, match := range templateVariable.FindAllStringSubmatch(translated, -1) {
				got[match[1]] = true
			}
			if len(want) != len(got) {
				t.Fatalf("template %q in %q has a different placeholder set: got %v, want %v", name, lang, got, want)
			}
			for key := range want {
				if !got[key] {
					t.Fatalf("template %q in %q is missing {%s}", name, lang, key)
				}
			}
		}
	}
}

func TestFormatNotificationTimeUsesHumanReadableTimezone(t *testing.T) {
	t.Setenv("TZ", "UTC")
	if got := formatNotificationTime("2026-01-05T19:03:07Z"); got != "January 05, 2026 at 19:03:07 UTC" {
		t.Fatalf("formatNotificationTime(timestamp) = %q", got)
	}
	if got := formatNotificationTime("2026-01-05"); got != "January 05, 2026 at 00:00:00 UTC" {
		t.Fatalf("formatNotificationTime(date) = %q", got)
	}
}

func TestManagerDryRunOverrideValidationAndRenderedNotifications(t *testing.T) {
	cfg := config.DefaultConfig()
	manager := &Manager{Config: cfg, Templates: managerTemplates(), ProjectName: "countries", FQDN: "countries.example", Version: "1.2.3"}
	if _, err := (*Manager)(nil).DryRun(); err == nil {
		t.Fatal("nil manager DryRun succeeded")
	}
	if _, err := (&Manager{}).DryRun(); err == nil {
		t.Fatal("DryRun without built-in templates succeeded")
	}
	rendered, err := manager.DryRun()
	if err != nil || len(rendered) != 16 {
		t.Fatalf("DryRun() rendered %d templates, error = %v", len(rendered), err)
	}
	if err := manager.CheckOverrides(); err != nil {
		t.Fatalf("CheckOverrides(empty directory) = %v", err)
	}
	directory := testutil.TempDir(t)
	manager.OverrideDir = directory
	if err := os.WriteFile(filepath.Join(directory, "bad.tmpl"), []byte("Subject: Bad {unknown}\n---\nBody"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckOverrides(); err == nil || !strings.Contains(err.Error(), "unknown variable") {
		t.Fatalf("CheckOverrides(unknown variable) = %v", err)
	}
	if err := os.Mkdir(filepath.Join(directory, "folder.tmpl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, "bad.tmpl")); err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckOverrides(); err != nil {
		t.Fatalf("CheckOverrides(non-template directories) = %v", err)
	}

	cfg.Server.Notifications.Email.Enabled = true
	cfg.Server.Contact.Admin.Email = "operator@example.test"
	manager.Sender.Config.Host = "127.0.0.1"
	manager.Templates = fstest.MapFS{"disk_low.tmpl": &fstest.MapFile{Data: []byte("Subject: Disk {used_percent}\n---\n{path}")}}
	if err := manager.Notify("startup", nil); err != nil {
		t.Fatalf("disabled event Notify() = %v", err)
	}
	if manager.Sent.Load() != 1 {
		t.Fatalf("Sent after disabled event = %d", manager.Sent.Load())
	}
	err = manager.Notify("disk_low", map[string]string{"used_percent": "92", "ignored\nkey": "bad"})
	if err == nil || !strings.Contains(err.Error(), "SMTP is not configured") {
		t.Fatalf("Notify() should render, then reject the invalid SMTP port: %v", err)
	}
	cfg.Server.Contact.Admin.Email = "not an address"
	if err := manager.Notify("disk_low", nil); err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Fatalf("Notify(invalid recipient) = %v", err)
	}
	if err := (*Manager)(nil).Notify("disk_low", nil); err == nil {
		t.Fatal("nil manager Notify succeeded")
	}
	if err := (&Manager{}).Notify("disk_low", nil); err == nil {
		t.Fatal("manager without config Notify succeeded")
	}
	if _, err := (&Manager{}).SendTest("bad"); err == nil || !strings.Contains(err.Error(), "SMTP") {
		t.Fatalf("SendTest without SMTP = %v", err)
	}
	if err := validateRenderedTemplate("", "body"); err == nil {
		t.Fatal("empty template subject passed validation")
	}
	if err := validateRenderedTemplate("Subject", " "); err == nil {
		t.Fatal("empty template body passed validation")
	}
	if err := validateRenderedTemplate("Subject\nInjected: header", "body"); err == nil {
		t.Fatal("newline template subject passed validation")
	}
	if err := validateRenderedTemplate("Subject {unknown}", "body"); err == nil {
		t.Fatal("unknown subject variable passed validation")
	}
	if err := validateRenderedTemplate("Subject", "{unknown}"); err == nil {
		t.Fatal("unknown body variable passed validation")
	}
	if err := validateRenderedTemplate("Subject {app_name}", "Body {version}"); err != nil {
		t.Fatalf("valid template variables rejected: %v", err)
	}
}

func managerTemplates() fs.FS {
	names := []string{"security_alert", "backup_complete", "backup_failed", "ssl_expiring", "ssl_renewed", "ssl_renewal_failed", "scheduler_error", "startup", "shutdown", "disk_low", "database_error", "update_available", "update_installed", "update_failed", "update_staged", "test"}
	files := make(fstest.MapFS, len(names))
	for _, name := range names {
		files[name+".tmpl"] = &fstest.MapFile{Data: []byte("Subject: {app_name} " + name + "\n---\nVersion {version}")}
	}
	return files
}
