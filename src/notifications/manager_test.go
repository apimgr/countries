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
	manager := &Manager{Config: cfg, Sender: Sender{Config: SMTPConfig{Host: "relay.example"}}}
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
	if got := manager.globalVariables()["fqdn"]; got != "localhost" {
		t.Fatalf("empty FQDN fallback = %q", got)
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
	if err := os.WriteFile(filepath.Join(directory, "event.tmpl"), []byte("Subject: Override\n---\nCustom body"), 0600); err != nil {
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
	if err != nil || len(rendered) != 14 {
		t.Fatalf("DryRun() rendered %d templates, error = %v", len(rendered), err)
	}
	if err := manager.CheckOverrides(); err != nil {
		t.Fatalf("CheckOverrides(empty directory) = %v", err)
	}
	directory := testutil.TempDir(t)
	manager.OverrideDir = directory
	if err := os.WriteFile(filepath.Join(directory, "bad.tmpl"), []byte("Subject: Bad {unknown}\n---\nBody"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckOverrides(); err == nil || !strings.Contains(err.Error(), "unknown variable") {
		t.Fatalf("CheckOverrides(unknown variable) = %v", err)
	}
	if err := os.Mkdir(filepath.Join(directory, "folder.tmpl"), 0700); err != nil {
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
	names := []string{"security_alert", "backup_complete", "backup_failed", "ssl_expiring", "ssl_renewed", "ssl_renewal_failed", "scheduler_error", "startup", "shutdown", "disk_low", "database_error", "update_available", "update_installed", "test"}
	files := make(fstest.MapFS, len(names))
	for _, name := range names {
		files[name+".tmpl"] = &fstest.MapFile{Data: []byte("Subject: {app_name} " + name + "\n---\nVersion {version}")}
	}
	return files
}
