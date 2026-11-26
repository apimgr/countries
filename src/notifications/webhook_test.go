package notifications

import (
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
)

func TestWebhookUUIDv7(t *testing.T) {
	id, err := webhookUUIDv7(time.Date(2026, 11, 24, 12, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("webhookUUIDv7() error = %v", err)
	}
	parts := strings.Split(id, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		t.Fatalf("UUIDv7 shape = %q", id)
	}
	if parts[2][0] != '7' || !strings.Contains("89ab", strings.ToLower(parts[3][:1])) {
		t.Fatalf("UUID version/variant bits are invalid: %q", id)
	}
}

func TestNotifyAdminWebhooksWithoutConfiguredEndpoints(t *testing.T) {
	manager := &Manager{Config: config.DefaultConfig()}
	if err := manager.notifyAdminWebhooks("scheduler_error", map[string]string{"error": "example"}); err != nil {
		t.Fatalf("notifyAdminWebhooks() error = %v", err)
	}
}

func TestResolveWebhookTargetsUsesPerTransportRoleFallback(t *testing.T) {
	contact := config.ContactConfig{
		Admin: config.PublicContactConfig{
			Webhooks:       map[string]string{"generic": "https://admin.example/hook", "slack": "https://admin.example/slack"},
			WebhookSecrets: map[string]string{"generic": "admin-generic", "slack": "admin-slack"},
		},
		General: config.PublicContactConfig{
			Webhooks:       map[string]string{"slack": "https://general.example/slack"},
			WebhookSecrets: map[string]string{"slack": "general-slack"},
		},
		Abuse: config.PublicContactConfig{
			Webhooks:       map[string]string{"slack": "https://abuse.example/slack"},
			WebhookSecrets: map[string]string{"slack": "abuse-slack"},
		},
	}
	targets := resolveWebhookTargets(contact, "abuse")
	if got := targets["slack"].endpoint; got != "https://abuse.example/slack" {
		t.Fatalf("abuse slack endpoint = %q", got)
	}
	if got := targets["generic"].endpoint; got != "https://admin.example/hook" {
		t.Fatalf("admin fallback endpoint = %q", got)
	}
	if got := targets["generic"].secret; got != "admin-generic" {
		t.Fatalf("fallback secret = %q", got)
	}
}

func TestWebhookSummaryOmitsReportBodyAndContactDetails(t *testing.T) {
	summary := webhookSummary(map[string]string{
		"component": "api", "tracking_id": "report-123", "severity": "critical",
		"email": "researcher@example.test", "name": "Researcher", "message": "private report body",
	})
	for _, privateValue := range []string{"researcher@example.test", "Researcher", "private report body"} {
		if strings.Contains(summary, privateValue) {
			t.Fatalf("webhook summary exposed %q: %q", privateValue, summary)
		}
	}
	for _, publicValue := range []string{"component: api", "tracking_id: report-123", "severity: critical"} {
		if !strings.Contains(summary, publicValue) {
			t.Fatalf("webhook summary omitted %q: %q", publicValue, summary)
		}
	}
}
