package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/apimgr/countries/src/config"
	"github.com/go-chi/chi/v5"
)

func TestStartupMaintenanceAllowsOnlyHealthAndDelegatesAfterRecovery(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Maintenance.SelfHealing.RetryInterval = "45s"
	startup := &Server{config: cfg}
	startup.startupMaintenance.Store(true)
	startup.SetMaintenanceMode(true)
	next := startup.startupMaintenanceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	health := httptest.NewRecorder()
	next.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/v1/server/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.Code)
	}
	countryRead := httptest.NewRecorder()
	next.ServeHTTP(countryRead, httptest.NewRequest(http.MethodGet, "/api/v1/countries", nil))
	if countryRead.Code != http.StatusOK {
		t.Fatalf("database-independent read status = %d, want 200", countryRead.Code)
	}
	databaseRead := httptest.NewRecorder()
	next.ServeHTTP(databaseRead, httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/tokens", nil))
	if databaseRead.Code != http.StatusServiceUnavailable {
		t.Fatalf("database-dependent read status = %d, want 503", databaseRead.Code)
	}
	write := httptest.NewRecorder()
	next.ServeHTTP(write, httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens", nil))
	var body struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		Message string `json:"message"`
		Details struct {
			Reason      string `json:"reason"`
			SelfHealing bool   `json:"self_healing"`
		} `json:"details"`
	}
	if write.Code != http.StatusServiceUnavailable || write.Header().Get("Retry-After") != "45" ||
		json.Unmarshal(write.Body.Bytes(), &body) != nil || body.OK || body.Error != "MAINTENANCE" ||
		body.Details.Reason != "database_connection" || !body.Details.SelfHealing {
		t.Fatalf("maintenance write = status %d headers=%v body=%s", write.Code, write.Header(), write.Body.String())
	}

	delegated := chi.NewRouter()
	delegated.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	startup.SetStartupDelegate(&Server{router: delegated})
	ready := httptest.NewRecorder()
	next.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ready.Code != http.StatusNoContent {
		t.Fatalf("recovered request status = %d, want 204", ready.Code)
	}
}

func TestStartupMaintenanceReadOnlyDelegateKeepsSecretsAndWritesUnavailable(t *testing.T) {
	startup := &Server{config: config.DefaultConfig()}
	startup.startupMaintenance.Store(true)
	startup.SetMaintenanceMode(true)
	delegated := chi.NewRouter()
	delegated.Get(apiCountryPath, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	delegated.Get("/api/v1/server/preferences/tokens", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	startup.SetStartupReadOnlyDelegate(&Server{router: delegated})
	handler := startup.startupMaintenanceMiddleware(http.NotFoundHandler())

	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest(http.MethodGet, apiCountryPath, nil))
	if public.Code != http.StatusOK {
		t.Fatalf("public cached read status = %d, want 200", public.Code)
	}
	secret := httptest.NewRecorder()
	handler.ServeHTTP(secret, httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/tokens", nil))
	if secret.Code != http.StatusServiceUnavailable {
		t.Fatalf("secret-dependent route status = %d, want 503", secret.Code)
	}
	write := httptest.NewRecorder()
	handler.ServeHTTP(write, httptest.NewRequest(http.MethodPost, apiCountryPath, nil))
	if write.Code != http.StatusServiceUnavailable {
		t.Fatalf("write status = %d, want 503", write.Code)
	}
}

const apiCountryPath = "/api/v1/countries"
