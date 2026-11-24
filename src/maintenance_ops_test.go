package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/testutil"
)

func maintenanceTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	root := testutil.TempDir(t)
	db, err := database.Open(filepath.Join(root, "server.db"))
	if err != nil {
		t.Fatalf("database.Open(): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, root
}

func createMaintenanceTestOwner(t *testing.T, db *sql.DB, name string) (database.TokenRecord, string) {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	primary, raw, err := database.CreateOwnerToken(context.Background(), db, key)
	if err != nil {
		t.Fatalf("CreateOwnerToken(): %v", err)
	}
	if name != "" {
		if _, _, err := database.CreateLinkedOwnerToken(context.Background(), db, primary.OwnerID, name, nil, 25, key, time.Now().UTC()); err != nil {
			t.Fatalf("CreateLinkedOwnerToken(): %v", err)
		}
	}
	return primary, raw
}

func TestMaintenanceTokenListAndRevoke(t *testing.T) {
	db, root := maintenanceTestDB(t)
	primary, primaryRaw := createMaintenanceTestOwner(t, db, "")
	linked, linkedRaw, err := database.CreateLinkedOwnerToken(context.Background(), db, primary.OwnerID, "worker", nil, 25, base64.StdEncoding.EncodeToString(make([]byte, 32)), time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateLinkedOwnerToken(): %v", err)
	}
	logsDir := filepath.Join(root, "logs")
	if err := maintenanceTokenCommand(context.Background(), db, []string{"list"}, logsDir); err != nil {
		t.Fatalf("maintenanceTokenCommand(list): %v", err)
	}
	if err := maintenanceTokenCommand(context.Background(), db, []string{"revoke", linked.Prefix, "--yes"}, logsDir); err != nil {
		t.Fatalf("maintenanceTokenCommand(revoke linked): %v", err)
	}
	if _, err := database.LookupOwnerToken(context.Background(), db, linkedRaw, time.Now()); err == nil {
		t.Fatal("linked token remained usable after revocation")
	}
	if _, err := database.LookupOwnerToken(context.Background(), db, primaryRaw, time.Now()); err != nil {
		t.Fatalf("linked-token revocation affected primary token: %v", err)
	}
	if err := maintenanceTokenCommand(context.Background(), db, []string{"revoke", primary.Prefix, "--yes"}, logsDir); err != nil {
		t.Fatalf("maintenanceTokenCommand(revoke primary): %v", err)
	}
	var owners, tokens int
	if err := db.QueryRow(`SELECT COUNT(*) FROM owners WHERE id=?`, primary.OwnerID).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_tokens WHERE owner_id=?`, primary.OwnerID).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if owners != 0 || tokens != 0 {
		t.Fatalf("primary revocation left owners=%d tokens=%d", owners, tokens)
	}
	logData, err := os.ReadFile(filepath.Join(logsDir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), primaryRaw) || strings.Contains(string(logData), linkedRaw) {
		t.Fatal("maintenance audit log contains a raw token")
	}
}

func TestMaintenanceDataExportAndDelete(t *testing.T) {
	db, root := maintenanceTestDB(t)
	primary, raw := createMaintenanceTestOwner(t, db, "automation")
	logsDir := filepath.Join(root, "logs")
	output := filepath.Join(root, "owner-export.json")
	if err := maintenanceDataCommand(context.Background(), db, config.DefaultConfig(), []string{"export", "--owner", primary.Prefix, "--output", output}, logsDir); err != nil {
		t.Fatalf("maintenanceDataCommand(export): %v", err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("export mode = %04o, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var exported maintenanceExport
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if exported.Format != 1 || exported.Owner.ID != primary.OwnerID || len(exported.Tokens) != 2 || len(exported.Resources) != 0 {
		t.Fatalf("export metadata = %#v", exported)
	}
	if strings.Contains(string(data), raw) || strings.Contains(string(data), "token_hash") {
		t.Fatal("export contains a raw token or token hash")
	}
	if err := maintenanceDataCommand(context.Background(), db, config.DefaultConfig(), []string{"delete", "--owner", primary.Prefix, "--yes"}, logsDir); err != nil {
		t.Fatalf("maintenanceDataCommand(delete): %v", err)
	}
	var owners, tokens int
	if err := db.QueryRow(`SELECT COUNT(*) FROM owners WHERE id=?`, primary.OwnerID).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_tokens WHERE owner_id=?`, primary.OwnerID).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if owners != 0 || tokens != 0 {
		t.Fatalf("data deletion left owners=%d tokens=%d", owners, tokens)
	}
}

func TestMaintenanceComplianceReport(t *testing.T) {
	db, root := maintenanceTestDB(t)
	configPath := filepath.Join(root, "server.yml")
	configData := []byte("server:\n  compliance:\n    gdpr: true\n    hipaa: true\n  logs:\n    defaults:\n      keep: 6y\n    audit:\n      keep: 6y\n")
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO backups (filename, filepath, size_bytes, type, created_at) VALUES ('backup.tar', '/backup/backup.tar', 10, 'manual', ?)`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	logsDir := filepath.Join(root, "logs")
	if err := writeMaintenanceAudit(logsDir, "compliance.test", "compliance", "low", nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := maintenanceComplianceCommand(context.Background(), db, config.DefaultConfig(), []string{"report"}, configPath, logsDir); err != nil {
		t.Fatalf("maintenanceComplianceCommand(report): %v", err)
	}
	entries, events, err := auditLogCounts(filepath.Join(logsDir, "audit.log"), time.Now().Add(-24*time.Hour))
	if err != nil || entries != 2 || events != 2 {
		t.Fatalf("auditLogCounts() = (%d, %d, %v), want (2, 2, nil)", entries, events, err)
	}
}

func TestMaintenanceSecurityReportLifecycle(t *testing.T) {
	db, root := maintenanceTestDB(t)
	created := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO security_reports (tracking_id, security_id, severity, component, status, body_enc, body_alg, credit_pref, token_hash, created_at, updated_at) VALUES (?, '', 'high', 'api\ncomponent', 'received', ?, 'pgp', 'anon', 'hash', ?, ?)`, "SR-TEST-1", []byte("encrypted"), created, created); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	logsDir := filepath.Join(root, "logs")
	if err := maintenanceSecurityCommand(context.Background(), db, cfg, []string{"list"}, logsDir); err != nil {
		t.Fatalf("security list: %v", err)
	}
	if err := maintenanceSecurityCommand(context.Background(), db, cfg, []string{"list", "--status", "triaged"}, logsDir); err != nil {
		t.Fatalf("filtered security list: %v", err)
	}
	if err := maintenanceSecurityCommand(context.Background(), db, cfg, []string{"list", "--status", "invalid"}, logsDir); err == nil {
		t.Fatal("security list accepted an invalid status filter")
	}
	if err := maintenanceSecurityCommand(context.Background(), db, cfg, []string{"show", "SR-TEST-1"}, logsDir); err != nil {
		t.Fatalf("security show: %v", err)
	}
	if err := maintenanceSecurityCommand(context.Background(), db, cfg, []string{"show", "missing"}, logsDir); err == nil {
		t.Fatal("security show accepted a missing report")
	}
	if err := maintenanceSecurityCommand(context.Background(), db, cfg, []string{"update", "SR-TEST-1", "--status", "triaged", "--comment", "Investigating"}, logsDir); err != nil {
		t.Fatalf("security update triaged: %v", err)
	}
	if err := maintenanceSecurityCommand(context.Background(), db, cfg, []string{"update", "SR-TEST-1", "--status", "disclosed", "--severity", "critical", "--disclosure-date", "2026-11-23"}, logsDir); err != nil {
		t.Fatalf("security update disclosed: %v", err)
	}
	var status, severity, comment, disclosure, credit string
	if err := db.QueryRow(`SELECT status, severity, researcher_comment, disclosure_date FROM security_reports WHERE tracking_id=?`, "SR-TEST-1").Scan(&status, &severity, &comment, &disclosure); err != nil {
		t.Fatal(err)
	}
	if status != "disclosed" || severity != "critical" || comment != "Investigating" || !strings.HasPrefix(disclosure, "2026-11-23") {
		t.Fatalf("updated report = status %q severity %q comment %q disclosure %q", status, severity, comment, disclosure)
	}
	if err := db.QueryRow(`SELECT credit FROM security_thanks WHERE tracking_id=?`, "SR-TEST-1").Scan(&credit); err != nil || credit != "Anonymous Researcher #1" {
		t.Fatalf("anonymous acknowledgment = (%q, %v)", credit, err)
	}
	for _, args := range [][]string{
		{"update", "SR-TEST-1"},
		{"update", "SR-TEST-1", "--unknown", "x"},
		{"update", "SR-TEST-1", "--status"},
		{"update", "SR-TEST-1", "--status", "received"},
		{"update", "SR-TEST-1", "--severity", "urgent"},
		{"update", "SR-TEST-1", "--disclosure-date", "not-a-date"},
		{"update", "SR-TEST-1", "--comment", strings.Repeat("x", 2001)},
		{"update", "missing", "--comment", "not found"},
	} {
		if err := maintenanceSecurityCommand(context.Background(), db, cfg, args, logsDir); err == nil {
			t.Errorf("maintenanceSecurityCommand(%q) unexpectedly succeeded", args)
		}
	}
}

func TestMaintenanceOwnerResourcesAndSafeMetadata(t *testing.T) {
	db, _ := maintenanceTestDB(t)
	primary, _ := createMaintenanceTestOwner(t, db, "")
	if _, err := db.Exec(`CREATE TABLE owned_resources (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO owned_resources (id, owner_id) VALUES ('resource-1', ?), ('resource-2', ?)`, primary.OwnerID, primary.OwnerID); err != nil {
		t.Fatal(err)
	}
	tables, err := ownerResourceTables(context.Background(), db)
	if err != nil || len(tables) != 1 || tables[0] != "owned_resources" {
		t.Fatalf("ownerResourceTables() = (%v, %v)", tables, err)
	}
	counts, err := ownerCounts(context.Background(), db, primary.OwnerID, tables)
	if err != nil || counts.tokens != 1 || counts.resources != 2 {
		t.Fatalf("ownerCounts() = (%+v, %v)", counts, err)
	}
	deleted, err := deleteOwnerTransaction(context.Background(), db, primary.OwnerID, tables)
	if err != nil || deleted.tokens != 1 || deleted.resources != 2 {
		t.Fatalf("deleteOwnerTransaction() = (%+v, %v)", deleted, err)
	}
	if validSecurityStatus("triaged") != true || validSecurityStatus("unknown") || validSecuritySeverity("critical") != true || validSecuritySeverity("urgent") {
		t.Fatal("security status or severity validation accepted an invalid value")
	}
	if got := tokenStatus(sql.NullInt64{Int64: time.Now().Add(-time.Hour).Unix(), Valid: true}, sql.NullInt64{}, time.Now()); got != "expired" {
		t.Fatalf("expired token status = %q", got)
	}
	if got := tokenStatus(sql.NullInt64{}, sql.NullInt64{Int64: 1, Valid: true}, time.Now()); got != "revoked" {
		t.Fatalf("revoked token status = %q", got)
	}
	if quoteSQLiteIdentifier(`a"b`) != `"a""b"` || safeTableValue("one\ntwo") != "one two" || htmlEscape(`<a&"'>`) != "&lt;a&amp;&#34;&#39;&gt;" {
		t.Fatal("maintenance metadata escaping did not match the safe representation")
	}
}

func TestWritePrivateExportRejectsSymlinksAndInvalidPaths(t *testing.T) {
	root := testutil.TempDir(t)
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "export")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := writePrivateExport(link, []byte("replace")); err == nil {
		t.Fatal("writePrivateExport() followed a symlink")
	}
	if err := writePrivateExport("", []byte("invalid")); err == nil {
		t.Fatal("writePrivateExport() accepted an empty path")
	}
}

func TestAuditLogCountsReportsMissingAndCorruptFiles(t *testing.T) {
	missing := filepath.Join(testutil.TempDir(t), "missing-audit.log")
	if entries, events, err := auditLogCounts(missing, time.Now()); err != nil || entries != 0 || events != 0 {
		t.Fatalf("missing audit log = (%d, %d, %v)", entries, events, err)
	}
	corrupt := filepath.Join(filepath.Dir(missing), "corrupt-audit.log")
	if err := os.WriteFile(corrupt, []byte("not-json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := auditLogCounts(corrupt, time.Now()); err == nil || !strings.Contains(err.Error(), "corrupt-audit.log:1") {
		t.Fatalf("corrupt audit log error = %v, want line 1", err)
	}
}

func TestReadAuditLogRetentionDropInsAndMalformedYAML(t *testing.T) {
	root := testutil.TempDir(t)
	configPath := filepath.Join(root, "server.yml")
	if err := os.WriteFile(configPath, []byte("server:\n  logs:\n    defaults:\n      keep: 2y\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dropins := filepath.Join(root, "yaml.d")
	if err := os.Mkdir(dropins, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropins, "audit.yml"), []byte("server:\n  logs:\n    audit:\n      keep: 6y\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readAuditLogRetention(configPath); err != nil || got != "6y" {
		t.Fatalf("readAuditLogRetention() = (%q, %v), want 6y", got, err)
	}
	if auditRetentionYears(6) != "6y" || auditRetentionYears(0) != "none specified" {
		t.Fatal("audit retention display is incorrect")
	}
	if err := os.WriteFile(filepath.Join(dropins, "bad.yaml"), []byte("server: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuditLogRetention(configPath); err == nil {
		t.Fatal("readAuditLogRetention() accepted malformed drop-in YAML")
	}
}

func TestReadComplianceStandardsRejectsMalformedYAML(t *testing.T) {
	configPath := filepath.Join(testutil.TempDir(t), "server.yml")
	if err := os.WriteFile(configPath, []byte("server: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readComplianceStandards(configPath); err == nil {
		t.Fatal("readComplianceStandards() accepted malformed YAML")
	}
}

func TestHasOnlyYes(t *testing.T) {
	if yes, err := hasOnlyYes([]string{"--yes"}); err != nil || !yes {
		t.Fatalf("hasOnlyYes(--yes) = (%t, %v)", yes, err)
	}
	if yes, err := hasOnlyYes(nil); err != nil || yes {
		t.Fatalf("hasOnlyYes(nil) = (%t, %v)", yes, err)
	}
	if _, err := hasOnlyYes([]string{"--no"}); err == nil {
		t.Fatal("hasOnlyYes() accepted an unsupported option")
	}
}

func TestMaintenanceDispatchAuthorizationAndDatabase(t *testing.T) {
	if handled, err := runMaintenanceOps("unknown", nil, "", "", "", ""); handled || err != nil {
		t.Fatalf("runMaintenanceOps(unknown) = (%t, %v), want (false, nil)", handled, err)
	}
	if handled, err := runMaintenanceOps("token", nil, "", "", "", filepath.Join(testutil.TempDir(t), "missing.yml")); !handled || err == nil {
		t.Fatalf("runMaintenanceOps(missing config) = (%t, %v), want handled error", handled, err)
	}
	cfg := config.DefaultConfig()
	if err := authorizeMaintenance(cfg, true); err != nil {
		t.Fatalf("authorizeMaintenance(root) error = %v", err)
	}
	if err := authorizeMaintenance(cfg, false); err == nil || !strings.Contains(err.Error(), "operator token") {
		t.Fatalf("authorizeMaintenance(no token) error = %v", err)
	}
	cfg.Server.Token = "configured-token"
	if err := authorizeMaintenance(cfg, false); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("authorizeMaintenance(non-interactive) error = %v", err)
	}
	root := testutil.TempDir(t)
	t.Setenv("DATABASE_DIR", filepath.Join(root, "database"))
	db, err := openMaintenanceDatabase(context.Background(), cfg, filepath.Join(root, "data"))
	if err != nil {
		t.Fatalf("openMaintenanceDatabase() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close maintenance database: %v", err)
	}
}
