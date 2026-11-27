package app

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/apimgr/countries/src/auditlog"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/notifications"
	"github.com/apimgr/countries/src/paths"
	"github.com/apimgr/countries/src/privilege"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

type maintenanceToken struct {
	OwnerID  string     `json:"owner_id"`
	Kind     string     `json:"kind"`
	Name     string     `json:"name,omitempty"`
	Prefix   string     `json:"prefix"`
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"last_used,omitempty"`
	Expires  *time.Time `json:"expires,omitempty"`
	Status   string     `json:"status"`
}

type maintenanceOwnerExport struct {
	ID       string    `json:"id"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"last_seen"`
}

type maintenanceExport struct {
	Format    int                    `json:"format"`
	Owner     maintenanceOwnerExport `json:"owner"`
	Tokens    []maintenanceToken     `json:"tokens"`
	Resources []json.RawMessage      `json:"resources"`
}

var complianceStandardNames = []string{
	"gdpr", "ccpa", "hipaa", "soc2", "pci_dss", "iso27001", "fedramp", "lgpd", "pipeda", "appi", "pdpa",
}

func runMaintenanceOps(command string, args []string, configDir, dataDir, logsDir, configPath string) (bool, error) {
	if command != "token" && command != "data" && command != "security" && command != "compliance" {
		return false, nil
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return true, fmt.Errorf("load configuration: %w", err)
	}
	if err := authorizeMaintenance(cfg, privilege.IsElevated()); err != nil {
		return true, err
	}
	db, err := openMaintenanceDatabase(context.Background(), cfg, dataDir)
	if err != nil {
		return true, err
	}
	defer func() { _ = database.Close(db) }()
	ctx := context.Background()
	switch command {
	case "token":
		return true, maintenanceTokenCommand(ctx, db, args, logsDir)
	case "data":
		return true, maintenanceDataCommand(ctx, db, cfg, args, logsDir)
	case "security":
		return true, maintenanceSecurityCommand(ctx, db, cfg, args, logsDir)
	case "compliance":
		return true, maintenanceComplianceCommand(ctx, db, cfg, args, configPath, logsDir)
	}
	return false, nil
}

func openMaintenanceDatabase(ctx context.Context, cfg *config.Config, dataDir string) (*sql.DB, error) {
	databasePath := strings.ReplaceAll(cfg.Server.Database.URL, "{data_dir}", dataDir)
	if cfg.Server.Database.URL == "" || cfg.Server.Database.URL == "{data_dir}/db/server.db" {
		databasePath = filepath.Join(paths.GetDatabaseDir(dataDir), "server.db")
	}
	db, err := database.Open(databasePath)
	if err != nil {
		return nil, err
	}
	auditMirrorDB = db
	return db, nil
}

func authorizeMaintenance(cfg *config.Config, root bool) error {
	if root {
		return nil
	}
	if strings.TrimSpace(cfg.Server.Token) == "" {
		return errors.New("maintenance requires root or the operator token")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("maintenance requires an interactive terminal to verify the operator token")
	}
	fmt.Fprint(os.Stderr, "Enter operator token: ")
	provided, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return err
	}
	expectedHash := sha256.Sum256([]byte(cfg.Server.Token))
	providedHash := sha256.Sum256(provided)
	if subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) != 1 {
		return errors.New("operator token is invalid")
	}
	return nil
}

func maintenanceTokenCommand(ctx context.Context, db *sql.DB, args []string, logsDir string) error {
	if len(args) == 1 && args[0] == "list" {
		rows, err := db.QueryContext(ctx, `SELECT t.owner_id, t.kind, COALESCE(t.name,''), t.token_prefix, t.created_at, t.last_used_at, t.expires_at, t.revoked_at FROM api_tokens AS t ORDER BY t.owner_id, CASE t.kind WHEN 'primary' THEN 0 ELSE 1 END, t.created_at, t.id`)
		if err != nil {
			return fmt.Errorf("list owner tokens: %w", err)
		}
		defer func() { _ = rows.Close() }()
		fmt.Println("PREFIX\tKIND\tOWNER\tNAME\tCREATED\tLAST USED\tSTATUS")
		for rows.Next() {
			var ownerID, kind, name, prefix string
			var created int64
			var lastUsed, expires, revoked sql.NullInt64
			if err := rows.Scan(&ownerID, &kind, &name, &prefix, &created, &lastUsed, &expires, &revoked); err != nil {
				return fmt.Errorf("read owner token metadata: %w", err)
			}
			status := tokenStatus(expires, revoked, time.Now())
			last := "-"
			if lastUsed.Valid {
				last = time.Unix(lastUsed.Int64, 0).UTC().Format(time.RFC3339)
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n", prefix, kind, ownerID, safeTableValue(name), time.Unix(created, 0).UTC().Format(time.RFC3339), last, status)
		}
		return rows.Err()
	}
	if len(args) < 2 || args[0] != "revoke" {
		return errors.New("usage: countries --maintenance token list | revoke <prefix> [--yes]")
	}
	prefix := strings.TrimSpace(args[1])
	if prefix == "" || strings.ContainsAny(prefix, "\r\n\x00") {
		return errors.New("a token prefix is required")
	}
	yes, err := hasOnlyYes(args[2:])
	if err != nil {
		return err
	}
	publicIDColumn, err := database.PublicTokenIDColumn(ctx, db)
	if err != nil {
		return fmt.Errorf("inspect public API token identifier: %w", err)
	}
	var ownerID, kind, tokenID string
	query := fmt.Sprintf(`SELECT %s, owner_id, kind FROM api_tokens WHERE token_prefix = ? AND revoked_at IS NULL`, publicIDColumn)
	if err := db.QueryRowContext(ctx, query, prefix).Scan(&tokenID, &ownerID, &kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("token prefix not found")
		}
		return fmt.Errorf("find token prefix: %w", err)
	}
	resourceTables, err := ownerResourceTables(ctx, db)
	if err != nil {
		return err
	}
	counts, err := ownerCounts(ctx, db, ownerID, resourceTables)
	if err != nil {
		return err
	}
	if kind == "primary" {
		fmt.Fprintf(os.Stderr, "Revoking primary token deletes owner %s, %d tokens and %d resources.\n", ownerID, counts.tokens, counts.resources)
		if err := confirmMaintenance("Type owner id to confirm: ", ownerID, yes); err != nil {
			return err
		}
		deleted, err := deleteOwnerTransaction(ctx, db, ownerID, resourceTables)
		if err != nil {
			return err
		}
		if err := writeMaintenanceAudit(logsDir, "token.revoked", "security", "high", map[string]any{"scope": "owner", "revoked_count": deleted.tokens, "resources": deleted.resources, "source": "cli"}, ownerID); err != nil {
			return err
		}
		fmt.Printf("Owner revoked; deleted %d tokens and %d resources.\n", deleted.tokens, deleted.resources)
		return nil
	}
	fmt.Fprintf(os.Stderr, "Revoking linked token %s.\n", prefix)
	if err := confirmMaintenance("Type token prefix to confirm: ", prefix, yes); err != nil {
		return err
	}
	var result sql.Result
	err = database.Write(ctx, db, func(tx *sql.Tx) error {
		query := fmt.Sprintf(`UPDATE api_tokens SET revoked_at = ?, revoked_reason = 'operator' WHERE %s = ? AND revoked_at IS NULL`, publicIDColumn)
		var err error
		result, err = tx.ExecContext(ctx, query, time.Now().UTC().Unix(), tokenID)
		return err
	})
	if err != nil {
		return fmt.Errorf("revoke linked token: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return errors.New("token prefix not found")
	}
	if err := writeMaintenanceAudit(logsDir, "token.revoked", "security", "medium", map[string]any{"scope": "token", "prefix": prefix, "source": "cli"}, ownerID); err != nil {
		return err
	}
	fmt.Println("Linked token revoked.")
	return nil
}

func maintenanceDataCommand(ctx context.Context, db *sql.DB, cfg *config.Config, args []string, logsDir string) error {
	if len(args) < 2 || (args[0] != "export" && args[0] != "delete") {
		return errors.New("usage: countries --maintenance data <export|delete> --owner <prefix> [--output FILE] [--yes]")
	}
	action := args[0]
	prefix, output, yes, err := parseDataArgs(args[1:])
	if err != nil {
		return err
	}
	ownerID, err := ownerIDForPrefix(ctx, db, prefix)
	if err != nil {
		return err
	}
	if action == "export" {
		return exportOwnerData(ctx, db, ownerID, prefix, output, logsDir)
	}
	resourceTables, err := ownerResourceTables(ctx, db)
	if err != nil {
		return err
	}
	counts, err := ownerCounts(ctx, db, ownerID, resourceTables)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Deleting owner %s, %d tokens and %d resources.\n", ownerID, counts.tokens, counts.resources)
	if err := confirmMaintenance("Type owner id to confirm: ", ownerID, yes); err != nil {
		return err
	}
	anonymousID, err := randomMaintenanceID()
	if err != nil {
		return err
	}
	requestID, err := randomMaintenanceID()
	if err != nil {
		return err
	}
	if err := writeMaintenanceAudit(logsDir, "compliance.data_deletion_requested", "compliance", "medium", map[string]any{"request_id": requestID}, anonymousID); err != nil {
		return err
	}
	deleted, err := deleteOwnerTransaction(ctx, db, ownerID, resourceTables)
	if err != nil {
		return err
	}
	if err := writeMaintenanceAudit(logsDir, "compliance.data_deletion_completed", "compliance", "medium", map[string]any{"request_id": requestID, "deleted_count": deleted.tokens + deleted.resources, "anonymized_count": 0}, anonymousID); err != nil {
		return err
	}
	fmt.Printf("Owner data deleted: %d items deleted, 0 anonymized.\n", deleted.tokens+deleted.resources)
	return nil
}

func exportOwnerData(ctx context.Context, db *sql.DB, ownerID, prefix, output, logsDir string) error {
	var created, lastSeen int64
	if err := db.QueryRowContext(ctx, `SELECT created_at,last_seen_at FROM owners WHERE id = ?`, ownerID).Scan(&created, &lastSeen); err != nil {
		return fmt.Errorf("read owner metadata: %w", err)
	}
	tokens, err := listOwnerTokenMetadata(ctx, db, ownerID)
	if err != nil {
		return err
	}
	requestID, err := randomMaintenanceID()
	if err != nil {
		return err
	}
	if err := writeMaintenanceAudit(logsDir, "compliance.data_export_requested", "compliance", "low", map[string]any{"request_id": requestID}, ownerID); err != nil {
		return err
	}
	export := maintenanceExport{
		Format: 1,
		Owner:  maintenanceOwnerExport{ID: ownerID, Created: time.Unix(created, 0).UTC(), LastSeen: time.Unix(lastSeen, 0).UTC()},
		Tokens: tokens, Resources: []json.RawMessage{},
	}
	data, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return fmt.Errorf("encode owner export: %w", err)
	}
	data = append(data, '\n')
	if output == "-" {
		if _, err := os.Stdout.Write(data); err != nil {
			return fmt.Errorf("write owner export: %w", err)
		}
	} else {
		if output == "" {
			output = fmt.Sprintf("countries-export-%s-%s.json", ownerID, time.Now().UTC().Format("20060102"))
		}
		if err := writePrivateExport(output, data); err != nil {
			return err
		}
	}
	if err := writeMaintenanceAudit(logsDir, "compliance.data_export_completed", "compliance", "low", map[string]any{"request_id": requestID, "file_size": len(data)}, ownerID); err != nil {
		return err
	}
	if output != "-" {
		fmt.Printf("Owner data export written: %s\n", output)
	}
	_ = prefix
	return nil
}

func maintenanceSecurityCommand(ctx context.Context, db *sql.DB, cfg *config.Config, args []string, logsDir string) error {
	if len(args) == 0 {
		return errors.New("usage: countries --maintenance security <list|show|update>")
	}
	switch args[0] {
	case "list":
		status := ""
		if len(args) == 3 && args[1] == "--status" {
			status = strings.ToLower(strings.TrimSpace(args[2]))
		} else if len(args) != 1 {
			return errors.New("usage: countries --maintenance security list [--status STATUS]")
		}
		query := `SELECT tracking_id,status,severity,component,created_at,updated_at FROM security_reports`
		values := []any{}
		if status != "" {
			if !validSecurityStatus(status) {
				return errors.New("invalid security report status")
			}
			query += ` WHERE status = ?`
			values = append(values, status)
		}
		query += ` ORDER BY created_at DESC,tracking_id`
		rows, err := db.QueryContext(ctx, query, values...)
		if err != nil {
			return fmt.Errorf("list security reports: %w", err)
		}
		defer func() { _ = rows.Close() }()
		fmt.Println("TRACKING ID\tSTATUS\tSEVERITY\tCOMPONENT\tCREATED\tUPDATED")
		for rows.Next() {
			var id, state, severity, component, created, updated string
			if err := rows.Scan(&id, &state, &severity, &component, &created, &updated); err != nil {
				return fmt.Errorf("read security report metadata: %w", err)
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n", id, state, severity, safeTableValue(component), created, updated)
		}
		return rows.Err()
	case "show":
		if len(args) != 2 {
			return errors.New("usage: countries --maintenance security show TRACKING_ID")
		}
		var id, status, severity, component, comment, disclosure, created, updated string
		if err := db.QueryRowContext(ctx, `SELECT tracking_id,status,severity,component,researcher_comment,COALESCE(disclosure_date,''),created_at,updated_at FROM security_reports WHERE tracking_id = ?`, args[1]).Scan(&id, &status, &severity, &component, &comment, &disclosure, &created, &updated); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("security report not found")
			}
			return fmt.Errorf("read security report metadata: %w", err)
		}
		fmt.Printf("Tracking ID: %s\nStatus: %s\nSeverity: %s\nComponent: %s\nCreated: %s\nUpdated: %s\nDisclosure date: %s\nResearcher comment: %s\n", id, status, severity, safeTableValue(component), created, updated, disclosure, safeTableValue(comment))
		return nil
	case "update":
		return updateSecurityReport(ctx, db, cfg, args[1:], logsDir)
	default:
		return errors.New("usage: countries --maintenance security <list|show|update>")
	}
}

func updateSecurityReport(ctx context.Context, db *sql.DB, cfg *config.Config, args []string, logsDir string) error {
	if len(args) < 1 {
		return errors.New("usage: countries --maintenance security update TRACKING_ID [--status STATUS] [--comment TEXT] [--disclosure-date YYYY-MM-DD] [--severity LEVEL]")
	}
	id := strings.TrimSpace(args[0])
	var nextStatus, comment, disclosure, severity string
	for index := 1; index < len(args); {
		if index+1 >= len(args) {
			return fmt.Errorf("option %s requires a value", args[index])
		}
		value := args[index+1]
		switch args[index] {
		case "--status":
			nextStatus = strings.ToLower(value)
		case "--comment":
			comment = value
		case "--disclosure-date":
			disclosure = value
		case "--severity":
			severity = strings.ToLower(value)
		default:
			return fmt.Errorf("unknown option %q", args[index])
		}
		index += 2
	}
	if nextStatus == "" && comment == "" && disclosure == "" && severity == "" {
		return errors.New("at least one report field must be updated")
	}
	if len([]rune(comment)) > 2000 {
		return errors.New("researcher comment exceeds 2000 characters")
	}
	if disclosure != "" {
		if _, err := time.Parse(time.DateOnly, disclosure); err != nil {
			return errors.New("disclosure date must use YYYY-MM-DD")
		}
	}
	if severity != "" && !validSecuritySeverity(severity) {
		return errors.New("invalid security report severity")
	}
	now := time.Now().UTC()
	var oldStatus, oldSeverity, creditPref, creditName, component string
	err := database.Write(ctx, db, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT status,severity,credit_pref,COALESCE(credit_name,''),component FROM security_reports WHERE tracking_id = ?`, id).Scan(&oldStatus, &oldSeverity, &creditPref, &creditName, &component); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("security report not found")
			}
			return fmt.Errorf("read security report for update: %w", err)
		}
		if nextStatus != "" && (!validSecurityStatus(nextStatus) || !statusMayAdvance(oldStatus, nextStatus)) {
			return errors.New("security report status may only move forward or to wont_fix")
		}
		if nextStatus == "" {
			nextStatus = oldStatus
		}
		if severity == "" {
			severity = oldSeverity
		}
		if comment == "" {
			comment = ""
		}
		var disclosureValue any
		if disclosure != "" {
			disclosureValue = disclosure
		}
		var closedValue any
		if nextStatus == "disclosed" || nextStatus == "wont_fix" {
			closedValue = now.Format(time.RFC3339Nano)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE security_reports SET status=?,severity=?,researcher_comment=CASE WHEN ? = '' THEN researcher_comment ELSE ? END,disclosure_date=CASE WHEN ? IS NULL THEN disclosure_date ELSE ? END,closed_at=CASE WHEN closed_at IS NULL AND ? IS NOT NULL THEN ? ELSE closed_at END,updated_at=? WHERE tracking_id=?`, nextStatus, severity, comment, comment, disclosureValue, disclosureValue, closedValue, closedValue, now.Format(time.RFC3339Nano), id); err != nil {
			return fmt.Errorf("update security report: %w", err)
		}
		if nextStatus == "disclosed" && oldStatus != "disclosed" && creditPref != "no" {
			credit := creditName
			if creditPref == "anon" {
				var count int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_thanks WHERE credit LIKE 'Anonymous Researcher #%'`).Scan(&count); err != nil {
					return err
				}
				credit = fmt.Sprintf("Anonymous Researcher #%d", count+1)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO security_thanks(tracking_id,credit,severity,disclosed_on) VALUES(?,?,?,?) ON CONFLICT(tracking_id) DO NOTHING`, id, credit, severity, now.Format(time.DateOnly)); err != nil {
				return fmt.Errorf("record security researcher acknowledgment: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := appendSecurityLog(logsDir, map[string]any{"event": "security.report_updated", "tracking_id": id, "old_status": oldStatus, "new_status": nextStatus, "time": now.Format(time.RFC3339)}); err != nil {
		return err
	}
	if err := writeMaintenanceAudit(logsDir, "security.report_updated", "security", "medium", map[string]any{"tracking_id": id, "old_status": oldStatus, "new_status": nextStatus}, id); err != nil {
		return err
	}
	if err := sendSecurityStatusNotice(cfg, db, id, nextStatus, comment); err != nil {
		fmt.Fprintf(os.Stderr, "Security report updated; researcher notification could not be sent: %v\n", err)
	}
	fmt.Println("Security report updated.")
	_ = component
	return nil
}

func sendSecurityStatusNotice(cfg *config.Config, db *sql.DB, id, status, comment string) error {
	if cfg == nil || !cfg.Server.Notifications.Email.Enabled || strings.TrimSpace(cfg.Server.Notifications.Email.SMTP.Host) == "" {
		return nil
	}
	var encrypted []byte
	if err := db.QueryRow(`SELECT contact_enc FROM security_reports WHERE tracking_id = ?`, id).Scan(&encrypted); err != nil {
		return err
	}
	if len(encrypted) == 0 {
		return nil
	}
	key, err := base64.StdEncoding.DecodeString(cfg.Server.Security.EncryptionKey)
	if err != nil || len(key) != 32 {
		return errors.New("security encryption key is unavailable")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(encrypted) < gcm.NonceSize() {
		return errors.New("researcher contact data is unavailable")
	}
	nonce, ciphertext := encrypted[:gcm.NonceSize()], encrypted[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return errors.New("researcher contact data could not be decrypted")
	}
	var contact struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(plain, &contact); err != nil {
		return errors.New("researcher contact data is invalid")
	}
	address, err := mail.ParseAddress(contact.Email)
	if err != nil || address.Address != contact.Email {
		return errors.New("researcher email is invalid")
	}
	from := cfg.Server.Notifications.Email.From
	if from.Email == "" {
		from.Email = "no-reply@" + notificationFQDN(cfg)
	}
	if from.Name == "" {
		from.Name = "Countries API"
	}
	body := fmt.Sprintf("Your security report %s status changed to %s.\n", id, status)
	if strings.TrimSpace(comment) != "" {
		body += "Maintainer comment: " + comment + "\n"
	}
	sender := notifications.Sender{Config: smtpSenderConfig(cfg.Server.Notifications.Email.SMTP)}
	return sender.Send(notifications.Message{FromName: from.Name, FromEmail: from.Email, To: contact.Email, ReplyTo: cfg.Server.Notifications.Email.ReplyTo, Subject: "Security report status updated", Text: body, HTML: "<!doctype html><html><body><pre>" + htmlEscape(body) + "</pre></body></html>", FQDN: notificationFQDN(cfg)})
}

func maintenanceComplianceCommand(ctx context.Context, db *sql.DB, cfg *config.Config, args []string, configPath, logsDir string) error {
	if len(args) != 1 || args[0] != "report" {
		return errors.New("usage: countries --maintenance compliance report")
	}
	standards, err := readComplianceStandards(configPath)
	if err != nil {
		return err
	}
	auditKeep, err := readAuditLogRetention(configPath)
	if err != nil {
		return err
	}
	resourceTables, err := ownerResourceTables(ctx, db)
	if err != nil {
		return err
	}
	var owners, tokens, reports int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM owners`).Scan(&owners); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, time.Now().UTC().Unix()).Scan(&tokens); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_reports WHERE closed_at IS NULL`).Scan(&reports); err != nil {
		return err
	}
	resourceCount := int64(0)
	for _, table := range resourceTables {
		quoted := quoteSQLiteIdentifier(table)
		var count int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoted).Scan(&count); err != nil {
			return err
		}
		resourceCount += count
	}
	backupTime := "never"
	var lastBackup sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(created_at) FROM backups`).Scan(&lastBackup); err != nil {
		return err
	}
	if lastBackup.Valid {
		backupTime = time.Unix(lastBackup.Int64, 0).UTC().Format(time.RFC3339)
	}
	auditPath := filepath.Join(logsDir, "audit.log")
	entries, complianceEvents, auditErr := auditLogCounts(auditPath, time.Now().UTC().Add(-30*24*time.Hour))
	auditStatus := fmt.Sprintf("intact (%d entries)", entries)
	if auditErr != nil {
		auditStatus = fmt.Sprintf("broken at %s", auditErr.Error())
	}
	fmt.Println("Compliance report")
	fmt.Println("Enabled standards and minimum settings:")
	first := true
	minimumAuditYears := 0
	minimumAudit := map[string]int{"gdpr": 1, "ccpa": 1, "hipaa": 6, "soc2": 1, "pci_dss": 1, "iso27001": 3}
	for _, name := range complianceStandardNames {
		if !standards[name] {
			continue
		}
		first = false
		if years := minimumAudit[name]; years > 0 {
			fmt.Printf("- %s: enabled; audit log retention minimum=%dy\n", name, years)
			if years > minimumAuditYears {
				minimumAuditYears = years
			}
		} else {
			fmt.Printf("- %s: enabled; audit log retention minimum=not specified in the matrix\n", name)
		}
	}
	if first {
		fmt.Println("- none")
	}
	fmt.Printf("Standard settings: server.compliance.enabled=%t; owner token idle purge=%d days\n", cfg.Server.Compliance.Enabled, cfg.Server.Tokens.IdlePurgeDays)
	fmt.Printf("Retention: audit log configured keep=%s; minimum required=%s; security reports=%d days; backups=max %d, weekly %d, monthly %d, yearly %d, size %s\n", auditKeep, auditRetentionYears(minimumAuditYears), cfg.Server.Security.Reports.RetentionDays, cfg.Server.Backup.Retention.MaxBackups, cfg.Server.Backup.Retention.KeepWeekly, cfg.Server.Backup.Retention.KeepMonthly, cfg.Server.Backup.Retention.KeepYearly, cfg.Server.Backup.Retention.MaxTotalSize)
	fmt.Printf("Counts: owners=%d resources=%d active_tokens=%d open_security_reports=%d\n", owners, resourceCount, tokens, reports)
	fmt.Printf("Compliance events (last 30 days): %d\n", complianceEvents)
	fmt.Printf("Last backup: %s\n", backupTime)
	fmt.Printf("Audit log integrity: %s\n", auditStatus)
	if err := writeMaintenanceAudit(logsDir, "compliance.report_generated", "compliance", "low", map[string]any{"report_type": "summary", "generated_by": "cli"}, ""); err != nil {
		return err
	}
	return nil
}

func readAuditLogRetention(configPath string) (string, error) {
	retention := "none"
	paths := []string{configPath}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(configPath), "yaml.d"))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".yml") || strings.HasSuffix(entry.Name(), ".yaml")) {
			paths = append(paths, filepath.Join(filepath.Dir(configPath), "yaml.d", entry.Name()))
		}
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		var document map[string]any
		if err := yaml.Unmarshal(data, &document); err != nil {
			return "", fmt.Errorf("read audit retention from %s: %w", filepath.Base(path), err)
		}
		server, _ := document["server"].(map[string]any)
		logs, _ := server["logs"].(map[string]any)
		defaults, _ := logs["defaults"].(map[string]any)
		audit, _ := logs["audit"].(map[string]any)
		if value, ok := defaults["keep"].(string); ok && strings.TrimSpace(value) != "" {
			retention = strings.TrimSpace(value)
		}
		if value, ok := audit["keep"].(string); ok && strings.TrimSpace(value) != "" {
			retention = strings.TrimSpace(value)
		}
	}
	return retention, nil
}

func auditRetentionYears(years int) string {
	if years == 0 {
		return "none specified"
	}
	return fmt.Sprintf("%dy", years)
}

func readComplianceStandards(configPath string) (map[string]bool, error) {
	result := make(map[string]bool, len(complianceStandardNames))
	for _, name := range complianceStandardNames {
		result[name] = false
	}
	paths := []string{configPath}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(configPath), "yaml.d"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".yml") && !strings.HasSuffix(entry.Name(), ".yaml")) {
			continue
		}
		paths = append(paths, filepath.Join(filepath.Dir(configPath), "yaml.d", entry.Name()))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var document map[string]any
		if err := yaml.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("read compliance settings from %s: %w", filepath.Base(path), err)
		}
		server, _ := document["server"].(map[string]any)
		section, _ := server["compliance"].(map[string]any)
		for _, name := range complianceStandardNames {
			if value, ok := section[name].(bool); ok {
				result[name] = value
			}
		}
	}
	return result, nil
}

func parseDataArgs(args []string) (owner, output string, yes bool, err error) {
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--owner":
			if index+1 >= len(args) || owner != "" {
				return "", "", false, errors.New("--owner requires one token prefix")
			}
			owner = args[index+1]
			index++
		case "--output":
			if index+1 >= len(args) || output != "" {
				return "", "", false, errors.New("--output requires one filename")
			}
			output = args[index+1]
			index++
		case "--yes":
			if yes {
				return "", "", false, errors.New("--yes may be specified only once")
			}
			yes = true
		default:
			return "", "", false, fmt.Errorf("unknown data option %q", args[index])
		}
	}
	if strings.TrimSpace(owner) == "" {
		return "", "", false, errors.New("--owner requires a token prefix")
	}
	return owner, output, yes, nil
}

func ownerIDForPrefix(ctx context.Context, db *sql.DB, prefix string) (string, error) {
	var ownerID string
	err := db.QueryRowContext(ctx, `SELECT owner_id FROM api_tokens WHERE token_prefix = ? ORDER BY id LIMIT 1`, strings.TrimSpace(prefix)).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("token prefix not found")
	}
	if err != nil {
		return "", fmt.Errorf("resolve token prefix: %w", err)
	}
	return ownerID, nil
}

func listOwnerTokenMetadata(ctx context.Context, db *sql.DB, ownerID string) ([]maintenanceToken, error) {
	rows, err := db.QueryContext(ctx, `SELECT owner_id,kind,COALESCE(name,''),token_prefix,created_at,last_used_at,expires_at,revoked_at FROM api_tokens WHERE owner_id=? ORDER BY CASE kind WHEN 'primary' THEN 0 ELSE 1 END,created_at,id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	tokens := make([]maintenanceToken, 0)
	now := time.Now().UTC()
	for rows.Next() {
		var token maintenanceToken
		var created int64
		var lastUsed, expires, revoked sql.NullInt64
		if err := rows.Scan(&token.OwnerID, &token.Kind, &token.Name, &token.Prefix, &created, &lastUsed, &expires, &revoked); err != nil {
			return nil, err
		}
		token.Created = time.Unix(created, 0).UTC()
		if lastUsed.Valid {
			value := time.Unix(lastUsed.Int64, 0).UTC()
			token.LastUsed = &value
		}
		if expires.Valid {
			value := time.Unix(expires.Int64, 0).UTC()
			token.Expires = &value
		}
		token.Status = tokenStatus(expires, revoked, now)
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func tokenStatus(expires, revoked sql.NullInt64, now time.Time) string {
	if revoked.Valid {
		return "revoked"
	}
	if expires.Valid && expires.Int64 <= now.Unix() {
		return "expired"
	}
	return "active"
}

func hasOnlyYes(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	if len(args) == 1 && args[0] == "--yes" {
		return true, nil
	}
	return false, errors.New("the only supported option is --yes")
}

func confirmMaintenance(prompt, expected string, yes bool) error {
	if yes {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("a terminal is required for confirmation; use --yes for scripted maintenance")
	}
	fmt.Fprint(os.Stderr, prompt)
	reader := bufio.NewReader(os.Stdin)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if strings.TrimSpace(value) != expected {
		return errors.New("confirmation did not match")
	}
	return nil
}

type ownerDeleteCounts struct {
	tokens    int64
	resources int64
}

func ownerCounts(ctx context.Context, db *sql.DB, ownerID string, resourceTables []string) (ownerDeleteCounts, error) {
	var counts ownerDeleteCounts
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id=?`, ownerID).Scan(&counts.tokens); err != nil {
		return counts, err
	}
	for _, table := range resourceTables {
		var count int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteSQLiteIdentifier(table)+` WHERE owner_id=?`, ownerID).Scan(&count); err != nil {
			return counts, err
		}
		counts.resources += count
	}
	return counts, nil
}

func ownerResourceTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var tables []string
	for _, name := range names {
		if name == "owners" || name == "api_tokens" {
			continue
		}
		columns, err := db.QueryContext(ctx, `PRAGMA table_info(`+quoteSQLiteIdentifier(name)+`)`)
		if err != nil {
			return nil, err
		}
		hasOwner := false
		for columns.Next() {
			var cid, notNull, primary int
			var column, kind string
			var defaultValue any
			if err := columns.Scan(&cid, &column, &kind, &notNull, &defaultValue, &primary); err != nil {
				_ = columns.Close()
				return nil, err
			}
			if column == "owner_id" {
				hasOwner = true
			}
		}
		closeErr := columns.Close()
		if closeErr != nil {
			return nil, closeErr
		}
		if hasOwner {
			tables = append(tables, name)
		}
	}
	sort.Strings(tables)
	return tables, nil
}

func deleteOwnerTransaction(ctx context.Context, db *sql.DB, ownerID string, resourceTables []string) (ownerDeleteCounts, error) {
	var counts ownerDeleteCounts
	err := database.Write(ctx, db, func(tx *sql.Tx) error {
		var err error
		counts, err = ownerCountsTx(ctx, tx, ownerID, resourceTables)
		if err != nil {
			return err
		}
		for _, table := range resourceTables {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteSQLiteIdentifier(table)+` WHERE owner_id=?`, ownerID); err != nil {
				return fmt.Errorf("delete owner resources: %w", err)
			}
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM owners WHERE id=?`, ownerID)
		if err != nil {
			return fmt.Errorf("delete owner: %w", err)
		}
		return nil
	})
	if err != nil {
		return ownerDeleteCounts{}, err
	}
	return counts, nil
}

func ownerCountsTx(ctx context.Context, tx *sql.Tx, ownerID string, resourceTables []string) (ownerDeleteCounts, error) {
	var counts ownerDeleteCounts
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id=?`, ownerID).Scan(&counts.tokens); err != nil {
		return counts, err
	}
	for _, table := range resourceTables {
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteSQLiteIdentifier(table)+` WHERE owner_id=?`, ownerID).Scan(&count); err != nil {
			return counts, err
		}
		counts.resources += count
	}
	return counts, nil
}

func writePrivateExport(path string, data []byte) error {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, '\x00') {
		return errors.New("export output path is invalid")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(absolute); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return errors.New("export output must be a regular file, not a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(absolute), ".countries-export-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, absolute); err != nil {
		return err
	}
	return nil
}

func randomMaintenanceID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func writeMaintenanceAudit(logsDir, event, category, severity string, details map[string]any, targetID string) error {
	var target *auditlog.Target
	if targetID != "" {
		target = &auditlog.Target{Type: "owner", ID: targetID}
	}
	return auditlog.WriteAndMirror(logsDir, auditMirrorDB, auditlog.Event{
		Event: event, Category: category, Severity: severity,
		Actor: auditlog.Actor{Type: "system", ID: "cli"}, Target: target,
		Details: details, Result: "success", Source: "cli",
	}, time.Now().UTC())
}

func appendSecurityLog(logsDir string, event map[string]any) error {
	path := filepath.Join(logsDir, "security.log")
	if err := os.MkdirAll(logsDir, 0750); err != nil {
		return err
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	if err := file.Chmod(0640); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func auditLogCounts(path string, since time.Time) (int, int, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("%s:1", filepath.Base(path))
	}
	defer func() { _ = file.Close() }()
	var total, compliance int
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		var event auditlog.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return total, compliance, fmt.Errorf("%s:%d", filepath.Base(path), lineNumber)
		}
		total++
		if strings.HasPrefix(event.Event, "compliance.") && !event.Time.Before(since) {
			compliance++
		}
	}
	if err := scanner.Err(); err != nil {
		return total, compliance, fmt.Errorf("%s:%d", filepath.Base(path), lineNumber+1)
	}
	if err := auditlog.Verify(path); err != nil {
		brokenLine := lineNumber
		if marker := strings.Index(err.Error(), "audit log line "); marker >= 0 {
			lineText := strings.TrimPrefix(err.Error()[marker+len("audit log line "):], "")
			end := strings.IndexByte(lineText, ' ')
			if end < 0 {
				end = len(lineText)
			}
			if parsed, parseErr := strconv.Atoi(lineText[:end]); parseErr == nil {
				brokenLine = parsed
			}
		}
		return total, compliance, fmt.Errorf("%s:%d", filepath.Base(path), brokenLine)
	}
	return total, compliance, nil
}

func validSecurityStatus(value string) bool {
	switch value {
	case "received", "triaged", "confirmed", "patching", "disclosed", "wont_fix":
		return true
	default:
		return false
	}
}

func statusMayAdvance(previous, next string) bool {
	if next == "wont_fix" || next == previous {
		return true
	}
	order := map[string]int{"received": 0, "triaged": 1, "confirmed": 2, "patching": 3, "disclosed": 4}
	oldOrder, oldOK := order[previous]
	newOrder, newOK := order[next]
	return oldOK && newOK && newOrder > oldOrder
}

func validSecuritySeverity(value string) bool {
	switch value {
	case "critical", "high", "medium", "low", "informational":
		return true
	default:
		return false
	}
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func safeTableValue(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
}

func htmlEscape(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&#34;", "'", "&#39;").Replace(value)
}
