package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/apimgr/countries/src/auditlog"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/paths"
	"github.com/apimgr/countries/src/privilege"
	"github.com/apimgr/countries/src/securitykey"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

type pgpServerState struct {
	Fingerprint string                       `json:"fingerprint"`
	Keyservers  map[string]pgpPublishAttempt `json:"keyservers"`
	LastExports map[string]string            `json:"last_exports"`
}

type pgpPublishAttempt struct {
	Attempts    int       `json:"attempts"`
	NextAt      time.Time `json:"next_at,omitempty"`
	PublishedAt string    `json:"published_at,omitempty"`
	Done        bool      `json:"done"`
}

func handlePGPMaintenance(args []string, configDir, dataDir, logsDir, configPath string) (resultErr error) {
	if len(args) == 0 {
		return errors.New("usage: countries --maintenance pgp <generate|rotate|publish|export|import|delete>")
	}
	cfg, err := config.LoadConfigFromFile(configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	databasePath := strings.ReplaceAll(cfg.Server.Database.URL, "{data_dir}", dataDir)
	if cfg.Server.Database.URL == "" || cfg.Server.Database.URL == "{data_dir}/db/server.db" {
		databasePath = filepath.Join(paths.GetDatabaseDir(dataDir), "server.db")
	}
	db, err := database.Open(databasePath)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, database.Close(db)) }()
	ctx := context.Background()
	if err := database.EnsureGeneratedSecrets(ctx, db); err != nil {
		return err
	}
	keyDir := filepath.Join(configDir, "security")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return fmt.Errorf("create security key directory: %w", err)
	}
	if err := RecoverPGPRotation(ctx, db, keyDir); err != nil {
		return fmt.Errorf("recover PGP key rotation: %w", err)
	}
	authorizationAction := args[0]
	if len(args) > 1 && args[0] == "export" && args[1] == "private" {
		authorizationAction = "export-private"
	}
	if err := authorizePGP(cfg, authorizationAction, privilege.IsElevated()); err != nil {
		return err
	}
	statePath := filepath.Join(keyDir, "keyservers.state")
	state, err := readPGPState(statePath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "generate":
		if len(args) != 1 {
			return errors.New("usage: countries --maintenance pgp generate")
		}
		for _, name := range []string{"pgp.pub.asc", "pgp.priv.asc.enc"} {
			if _, err := os.Stat(filepath.Join(keyDir, name)); err == nil {
				return errors.New("a PGP key already exists; use rotate")
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		secret, err := installationSecret(ctx, db)
		if err != nil {
			return err
		}
		pair, err := securitykey.Generate(pgpIdentityName(), pgpSecurityEmail(cfg), time.Now())
		if err != nil {
			return err
		}
		sealed, err := securitykey.SealPrivate(secret, pair.Private)
		if err != nil {
			return err
		}
		if err := writePGPFile(filepath.Join(keyDir, "pgp.priv.asc.enc"), sealed); err != nil {
			return err
		}
		if err := writePGPFile(filepath.Join(keyDir, "pgp.pub.asc"), pair.Public); err != nil {
			_ = os.Remove(filepath.Join(keyDir, "pgp.priv.asc.enc"))
			return err
		}
		if err := database.SavePGPKeyMetadata(ctx, db, database.PGPKeyMetadata{Fingerprint: pair.Fingerprint, CreatedAt: pair.CreatedAt, ExpiresAt: pair.ExpiresAt, KeyserversPublished: map[string]string{}}); err != nil {
			return err
		}
		state.Fingerprint = pair.Fingerprint
		state.Keyservers = map[string]pgpPublishAttempt{}
		if err := writePGPState(statePath, state); err != nil {
			return err
		}
		fmt.Printf("Generated PGP key %s\n", pair.Fingerprint)
		return publishPGP(ctx, cfg, db, pair.Public, pair.Fingerprint, statePath, &state, time.Now())
	case "rotate":
		if len(args) != 1 {
			return errors.New("usage: countries --maintenance pgp rotate")
		}
		return rotatePGP(cfg, db, keyDir, statePath, &state)
	case "publish":
		pub, err := os.ReadFile(filepath.Join(keyDir, "pgp.pub.asc"))
		if err != nil {
			return errors.New("no public PGP key is installed")
		}
		fingerprint, err := securitykey.Fingerprint(pub)
		if err != nil {
			return err
		}
		return publishPGP(ctx, cfg, db, pub, fingerprint, statePath, &state, time.Now())
	case "export":
		return exportPGP(args[1:], db, keyDir, logsDir)
	case "import":
		if len(args) != 2 {
			return errors.New("usage: countries --maintenance pgp import <file>")
		}
		return importPGP(args[1], cfg, db, keyDir)
	case "delete":
		if len(args) != 1 {
			return errors.New("usage: countries --maintenance pgp delete")
		}
		return deletePGP(configPath, db, keyDir)
	default:
		return fmt.Errorf("unknown PGP action %q", args[0])
	}
}

func authorizePGP(cfg *config.Config, action string, root bool) error {
	if !root {
		if strings.TrimSpace(cfg.Server.Token) == "" {
			return errors.New("PGP maintenance requires root or the operator token")
		}
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("PGP maintenance requires an interactive terminal to verify the operator token")
		}
		fmt.Fprint(os.Stderr, "Enter operator token: ")
		provided, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		if len(provided) != len(cfg.Server.Token) || subtle.ConstantTimeCompare(provided, []byte(cfg.Server.Token)) != 1 {
			return errors.New("operator token is invalid")
		}
	}
	if action == "export-private" || action == "import" || action == "delete" {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("this PGP action requires an interactive terminal")
		}
	}
	if action == "export-private" || action == "import" || action == "delete" {
		confirmationText := "PGP " + strings.ToUpper(action)
		switch action {
		case "export-private":
			confirmationText = "EXPORT PRIVATE KEY"
		case "import":
			confirmationText = "IMPORT PRIVATE KEY"
		case "delete":
			confirmationText = "DELETE PGP"
		}
		if action == "delete" {
			fmt.Fprint(os.Stderr, "Deleting the key makes PGP-encrypted reports unreadable. Type DELETE PGP to confirm: ")
		} else {
			fmt.Fprintf(os.Stderr, "Type %s to confirm: ", confirmationText)
		}
		var confirmation string
		if _, err := fmt.Fscanln(os.Stdin, &confirmation); err != nil {
			return err
		}
		if confirmation != confirmationText {
			return errors.New("confirmation did not match")
		}
		if action == "export-private" && !root {
			fmt.Fprint(os.Stderr, "Re-enter operator token to confirm private key export: ")
			provided, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			if len(provided) != len(cfg.Server.Token) || subtle.ConstantTimeCompare(provided, []byte(cfg.Server.Token)) != 1 {
				return errors.New("operator token is invalid")
			}
		}
		return nil
	}
	return nil
}

func pgpIdentityName() string {
	return "Countries API Security"
}

func pgpSecurityEmail(cfg *config.Config) string {
	if email := strings.TrimSpace(cfg.Server.Contact.Security.Email); email != "" {
		return email
	}
	if email := strings.TrimSpace(cfg.Server.Contact.Admin.Email); email != "" {
		return email
	}
	return "security@" + strings.TrimSpace(cfg.Server.FQDN)
}

func installationSecret(ctx context.Context, db *sql.DB) ([]byte, error) {
	encoded, ok, err := database.GetAppSecret(ctx, db, "installation_secret")
	if err != nil || !ok {
		return nil, errors.New("installation secret is unavailable")
	}
	secret, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(secret) < 32 {
		return nil, errors.New("installation secret is invalid")
	}
	return secret, nil
}

func writePGPFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil {
		return errors.Join(err, f.Close())
	}
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if directory, err := os.Open(filepath.Dir(path)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func readPGPState(path string) (pgpServerState, error) {
	state := pgpServerState{Keyservers: map[string]pgpPublishAttempt{}, LastExports: map[string]string{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("read PGP keyserver state: %w", err)
	}
	if state.Keyservers == nil {
		state.Keyservers = map[string]pgpPublishAttempt{}
	}
	if state.LastExports == nil {
		state.LastExports = map[string]string{}
	}
	return state, nil
}

func writePGPState(path string, state pgpServerState) error {
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writePGPFile(path, append(encoded, '\n'))
}

func publishPGP(ctx context.Context, cfg *config.Config, db *sql.DB, pub []byte, fingerprint, statePath string, state *pgpServerState, now time.Time) error {
	if state.Fingerprint != fingerprint {
		state.Fingerprint = fingerprint
		state.Keyservers = map[string]pgpPublishAttempt{}
	}
	client := &http.Client{Timeout: 20 * time.Second}
	var failures []string
	for _, host := range cfg.Web.Security.Keyservers {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		attempt := state.Keyservers[host]
		if attempt.Done {
			if db != nil && attempt.PublishedAt != "" {
				publishedAt, parseErr := time.Parse(time.RFC3339Nano, attempt.PublishedAt)
				if parseErr != nil {
					failures = append(failures, host+": invalid persisted publication timestamp")
				} else if recordErr := database.RecordPGPKeyserverPublication(ctx, db, fingerprint, host, publishedAt); recordErr != nil {
					failures = append(failures, host+": record publication metadata: "+recordErr.Error())
				}
			}
			continue
		}
		if attempt.NextAt.After(now) {
			continue
		}
		endpoint, method, body, contentType, err := keyserverRequest(host, pub)
		if err == nil {
			request, requestErr := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
			if requestErr != nil {
				err = requestErr
			} else {
				request.Header.Set("Content-Type", contentType)
				response, requestErr := client.Do(request)
				if requestErr != nil {
					err = requestErr
				} else {
					responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
					readErr = errors.Join(readErr, response.Body.Close())
					if response.StatusCode >= 200 && response.StatusCode < 300 {
						if readErr != nil {
							err = fmt.Errorf("read keyserver success response: %w", readErr)
						} else if strings.EqualFold(host, "keys.openpgp.org") && !validVKSResponse(fingerprint, responseBody) {
							err = errors.New("keyserver returned an invalid success response")
						} else {
							attempt.Done = true
							attempt.PublishedAt = now.UTC().Format(time.RFC3339Nano)
							attempt.NextAt = time.Time{}
							state.Keyservers[host] = attempt
							if db != nil {
								if recordErr := database.RecordPGPKeyserverPublication(ctx, db, fingerprint, host, now); recordErr != nil {
									failures = append(failures, host+": record publication metadata: "+recordErr.Error())
								}
							}
							continue
						}
					}
					if response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests && (response.StatusCode < 200 || response.StatusCode >= 300) {
						attempt.Done = true
						state.Keyservers[host] = attempt
						failures = append(failures, host+": rejected submission")
						continue
					}
					if err == nil {
						err = fmt.Errorf("temporary HTTP status %d", response.StatusCode)
					}
				}
			}
		}
		attempt.Attempts++
		if attempt.Attempts >= 8 {
			attempt.Done = true
		} else {
			delay := time.Minute << (attempt.Attempts - 1)
			if delay > 6*time.Hour {
				delay = 6 * time.Hour
			}
			attempt.NextAt = now.Add(delay)
		}
		state.Keyservers[host] = attempt
		failures = append(failures, host+": "+err.Error())
	}
	if err := writePGPState(statePath, *state); err != nil {
		return err
	}
	if len(failures) > 0 {
		return fmt.Errorf("key publication incomplete: %s", strings.Join(failures, "; "))
	}
	return nil
}

func retryPGPPublication(ctx context.Context, cfg *config.Config, db *sql.DB, configDir string, now time.Time) error {
	keyDir := filepath.Join(configDir, "security")
	statePath := filepath.Join(keyDir, "keyservers.state")
	public, err := os.ReadFile(filepath.Join(keyDir, "pgp.pub.asc"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	fingerprint, err := securitykey.Fingerprint(public)
	if err != nil {
		return err
	}
	state, err := readPGPState(statePath)
	if err != nil {
		return err
	}
	due := state.Fingerprint != fingerprint
	for _, host := range cfg.Web.Security.Keyservers {
		attempt := state.Keyservers[strings.TrimSpace(host)]
		if !attempt.Done && !attempt.NextAt.After(now) {
			due = true
			break
		}
	}
	if !due {
		return nil
	}
	return publishPGP(ctx, cfg, db, public, fingerprint, statePath, &state, now)
}

func rotatePGP(cfg *config.Config, db *sql.DB, keyDir, statePath string, state *pgpServerState) error {
	ctx := context.Background()
	publicPath := filepath.Join(keyDir, "pgp.pub.asc")
	privatePath := filepath.Join(keyDir, "pgp.priv.asc.enc")
	oldPrivatePath := filepath.Join(keyDir, "pgp.priv.old.asc.enc")
	stagedPublicPath := publicPath + ".new"
	stagedPrivatePath := privatePath + ".new"
	stagedSignaturePath := filepath.Join(keyDir, "pgp.rotation.asc.sig.new")
	oldPublic, err := os.ReadFile(publicPath)
	if err != nil {
		return errors.New("no active PGP key is installed")
	}
	oldFingerprint, err := securitykey.Fingerprint(oldPublic)
	if err != nil {
		return err
	}
	metadata, exists, err := database.GetPGPKeyMetadata(ctx, db)
	if err != nil || !exists || metadata.Fingerprint != oldFingerprint {
		return errors.New("active PGP key metadata does not match the installed public key")
	}
	if _, err := os.Stat(oldPrivatePath); err == nil {
		return errors.New("a retired PGP private key is still within its 30-day retention period")
	} else if !os.IsNotExist(err) {
		return err
	}
	secret, err := installationSecret(ctx, db)
	if err != nil {
		return err
	}
	oldPrivateEncrypted, err := os.ReadFile(privatePath)
	if err != nil {
		return errors.New("encrypted private key is unavailable")
	}
	oldPrivate, err := securitykey.OpenPrivate(secret, oldPrivateEncrypted)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	newPair, err := securitykey.Generate(pgpIdentityName(), pgpSecurityEmail(cfg), now)
	if err != nil {
		return err
	}
	signature, err := securitykey.SignPublicKey(oldPrivate, newPair.Public)
	if err != nil {
		return err
	}
	newPrivateEncrypted, err := securitykey.SealPrivate(secret, newPair.Private)
	if err != nil {
		return err
	}
	if err := writePGPFile(stagedPrivatePath, newPrivateEncrypted); err != nil {
		return fmt.Errorf("stage rotated PGP private key: %w", err)
	}
	if err := writePGPFile(stagedPublicPath, newPair.Public); err != nil {
		_ = os.Remove(stagedPrivatePath)
		return fmt.Errorf("stage rotated PGP public key: %w", err)
	}
	if err := writePGPFile(stagedSignaturePath, signature); err != nil {
		_ = os.Remove(stagedPrivatePath)
		_ = os.Remove(stagedPublicPath)
		return fmt.Errorf("stage rotated PGP signature: %w", err)
	}
	nextState := pgpServerState{Fingerprint: newPair.Fingerprint, Keyservers: map[string]pgpPublishAttempt{}, LastExports: state.LastExports}

	var publishedJSON []byte
	err = database.Write(ctx, db, func(tx *sql.Tx) error {
		if err := reencryptPGPReports(ctx, tx, oldPrivate, newPair.Public); err != nil {
			return err
		}
		publishedJSON, err = json.Marshal(map[string]string{})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pgp_keys (fingerprint, created_at, expires_at, last_rotated_at, keyservers_published, revoked) VALUES (?, ?, ?, ?, ?, FALSE)`, newPair.Fingerprint, newPair.CreatedAt.Format(time.RFC3339Nano), newPair.ExpiresAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), string(publishedJSON)); err != nil {
			return fmt.Errorf("save rotated PGP key metadata: %w", err)
		}
		return nil
	})
	if err != nil {
		_ = os.Remove(stagedPrivatePath)
		_ = os.Remove(stagedPublicPath)
		_ = os.Remove(stagedSignaturePath)
		return fmt.Errorf("commit PGP report re-encryption: %w", err)
	}
	signaturePath := filepath.Join(keyDir, "pgp.rotation.asc.sig")
	if err := os.Rename(privatePath, oldPrivatePath); err != nil {
		return fmt.Errorf("preserve retired PGP private key: %w", err)
	}
	if err := os.Rename(stagedPrivatePath, privatePath); err != nil {
		return fmt.Errorf("install rotated PGP private key: %w", err)
	}
	if err := os.Rename(stagedSignaturePath, signaturePath); err != nil {
		return fmt.Errorf("install rotated PGP signature: %w", err)
	}
	if err := os.Rename(stagedPublicPath, publicPath); err != nil {
		return fmt.Errorf("install rotated PGP public key: %w", err)
	}
	if err := publishPGP(ctx, cfg, nil, newPair.Public, newPair.Fingerprint, statePath, &nextState, now); err != nil && !strings.HasPrefix(err.Error(), "key publication incomplete:") {
		return fmt.Errorf("persist PGP publication state: %w", err)
	}
	for host, attempt := range nextState.Keyservers {
		if !attempt.Done || attempt.PublishedAt == "" {
			continue
		}
		publishedAt, err := time.Parse(time.RFC3339Nano, attempt.PublishedAt)
		if err != nil {
			return fmt.Errorf("parse PGP publication time for %s: %w", host, err)
		}
		if err := database.RecordPGPKeyserverPublication(ctx, db, newPair.Fingerprint, host, publishedAt); err != nil {
			return fmt.Errorf("record PGP publication for %s: %w", host, err)
		}
	}
	*state = nextState
	if err := writePGPState(statePath, *state); err != nil {
		return fmt.Errorf("save PGP rotation publication state: %w", err)
	}
	fmt.Printf("Rotated PGP key %s to %s; the previous private key is retained for 30 days\n", oldFingerprint, newPair.Fingerprint)
	return nil
}

// RecoverPGPRotation resolves staged key files against the database commit marker.
func RecoverPGPRotation(ctx context.Context, db *sql.DB, keyDir string) error {
	publicPath := filepath.Join(keyDir, "pgp.pub.asc")
	privatePath := filepath.Join(keyDir, "pgp.priv.asc.enc")
	oldPrivatePath := filepath.Join(keyDir, "pgp.priv.old.asc.enc")
	stagedPublicPath := publicPath + ".new"
	stagedPrivatePath := privatePath + ".new"
	stagedSignaturePath := filepath.Join(keyDir, "pgp.rotation.asc.sig.new")
	signaturePath := filepath.Join(keyDir, "pgp.rotation.asc.sig")
	staged, err := anyFileExists(stagedPublicPath, stagedPrivatePath, stagedSignaturePath)
	if err != nil || !staged {
		return err
	}
	metadata, found, err := database.GetPGPKeyMetadata(ctx, db)
	if err != nil {
		return err
	}
	stagedPublic, stagedErr := os.ReadFile(stagedPublicPath)
	if stagedErr == nil {
		fingerprint, fingerprintErr := securitykey.Fingerprint(stagedPublic)
		if fingerprintErr != nil {
			return fmt.Errorf("read staged PGP fingerprint: %w", fingerprintErr)
		}
		if !found || fingerprint != metadata.Fingerprint {
			return removeStagedPGPFiles(stagedPrivatePath, stagedPublicPath, stagedSignaturePath)
		}
	} else if !os.IsNotExist(stagedErr) {
		return stagedErr
	} else {
		activePublic, readErr := os.ReadFile(publicPath)
		if readErr != nil {
			return fmt.Errorf("read active PGP key during recovery: %w", readErr)
		}
		fingerprint, fingerprintErr := securitykey.Fingerprint(activePublic)
		if fingerprintErr != nil || !found || fingerprint != metadata.Fingerprint {
			return errors.New("PGP rotation staged files are incomplete and the active key does not match the database commit marker")
		}
		// The public key is renamed last. Without its staged file, any remaining
		// staged artifacts came from a crash before the database commit.
		return removeStagedPGPFiles(stagedPrivatePath, stagedPublicPath, stagedSignaturePath)
	}
	if _, err := os.Stat(stagedPrivatePath); err == nil {
		if _, err := os.Stat(oldPrivatePath); os.IsNotExist(err) {
			if err := os.Rename(privatePath, oldPrivatePath); err != nil {
				return fmt.Errorf("preserve previous PGP private key during recovery: %w", err)
			}
		} else if err != nil {
			return err
		}
		if err := os.Rename(stagedPrivatePath, privatePath); err != nil {
			return fmt.Errorf("install staged PGP private key during recovery: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(stagedSignaturePath); err == nil {
		if err := os.Rename(stagedSignaturePath, signaturePath); err != nil {
			return fmt.Errorf("install staged PGP signature during recovery: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(stagedPublicPath); err == nil {
		if err := os.Rename(stagedPublicPath, publicPath); err != nil {
			return fmt.Errorf("install staged PGP public key during recovery: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func anyFileExists(paths ...string) (bool, error) {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, nil
}

func removeStagedPGPFiles(paths ...string) error {
	var result error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func reencryptPGPReports(ctx context.Context, conn *sql.Tx, oldPrivate, newPublic []byte) error {
	lastID := ""
	for {
		rows, err := conn.QueryContext(ctx, `SELECT tracking_id, body_enc FROM security_reports WHERE body_alg = 'pgp' AND tracking_id > ? ORDER BY tracking_id LIMIT 100`, lastID)
		if err != nil {
			return fmt.Errorf("read reports for PGP rotation: %w", err)
		}
		type report struct {
			id   string
			body []byte
		}
		batch := make([]report, 0, 100)
		for rows.Next() {
			var item report
			if err := rows.Scan(&item.id, &item.body); err != nil {
				return errors.Join(fmt.Errorf("read report for PGP rotation: %w", err), rows.Close())
			}
			batch = append(batch, item)
		}
		if err := rows.Err(); err != nil {
			return errors.Join(fmt.Errorf("read reports for PGP rotation: %w", err), rows.Close())
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close report query for PGP rotation: %w", err)
		}
		if len(batch) == 0 {
			return nil
		}
		for _, item := range batch {
			plain, err := securitykey.DecryptMessage(oldPrivate, item.body)
			if err != nil {
				return fmt.Errorf("decrypt retained security report %s: %w", item.id, err)
			}
			body, err := securitykey.EncryptMessage(newPublic, plain)
			if err != nil {
				return fmt.Errorf("encrypt retained security report %s: %w", item.id, err)
			}
			if _, err := conn.ExecContext(ctx, `UPDATE security_reports SET body_enc = ? WHERE tracking_id = ? AND body_alg = 'pgp'`, body, item.id); err != nil {
				return fmt.Errorf("update retained security report %s: %w", item.id, err)
			}
			lastID = item.id
		}
	}
}

func validVKSResponse(fingerprint string, response []byte) bool {
	var result struct {
		Fingerprint string `json:"key_fpr"`
		Status      string `json:"status"`
	}
	if json.Unmarshal(response, &result) != nil {
		return false
	}
	return strings.EqualFold(result.Fingerprint, fingerprint) && strings.TrimSpace(result.Status) != ""
}

func keyserverRequest(host string, pub []byte) (string, string, []byte, string, error) {
	if strings.ContainsAny(host, "/:@?#") {
		return "", "", nil, "", errors.New("keyserver must be a host name")
	}
	if strings.EqualFold(host, "keys.openpgp.org") {
		body, err := json.Marshal(map[string]string{"keytext": string(pub)})
		return "https://keys.openpgp.org/vks/v1/upload", http.MethodPost, body, "application/json", err
	}
	return "https://" + host + "/pks/add", http.MethodPost, []byte("keytext=" + url.QueryEscape(string(pub))), "application/x-www-form-urlencoded", nil
}

func exportPGP(args []string, db *sql.DB, keyDir, logsDir string) error {
	if len(args) < 1 || len(args) > 2 || (args[0] != "public" && args[0] != "private") {
		return errors.New("usage: countries --maintenance pgp export <public [path]|private <path>>")
	}
	if args[0] == "public" {
		pub, err := os.ReadFile(filepath.Join(keyDir, "pgp.pub.asc"))
		if err != nil {
			return errors.New("no public PGP key is installed")
		}
		if len(args) == 1 {
			_, err = os.Stdout.Write(pub)
			return err
		}
		if err := writePGPFile(args[1], pub); err != nil {
			return err
		}
		return os.Chmod(args[1], 0o644)
	}
	if len(args) != 2 {
		return errors.New("private key export requires an output path")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("private key export requires an interactive terminal")
	}
	fmt.Fprint(os.Stderr, "Reason for private key export: ")
	var reason string
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	reason = strings.TrimSpace(line)
	if reason == "" {
		return errors.New("a reason is required for private key export")
	}
	operator := "root"
	if !privilege.IsElevated() {
		operator = "operator-token"
	}
	allowed, retryAfter, err := database.ClaimPGPPrivateExport(context.Background(), db, operator, time.Now().UTC())
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("private key export is limited to one per hour per operator; try again in %s", retryAfter.Round(time.Second))
	}
	secret, err := installationSecret(context.Background(), db)
	if err != nil {
		return err
	}
	encrypted, err := os.ReadFile(filepath.Join(keyDir, "pgp.priv.asc.enc"))
	if err != nil {
		return errors.New("encrypted private key is unavailable")
	}
	private, err := securitykey.OpenPrivate(secret, encrypted)
	if err != nil {
		return err
	}
	if err := writePGPFile(args[1], private); err != nil {
		return err
	}
	ip := "local"
	if ssh := strings.Fields(os.Getenv("SSH_CONNECTION")); len(ssh) > 0 {
		if parsed := net.ParseIP(ssh[0]); parsed != nil {
			ip = parsed.String()
		}
	}
	if err := auditlog.WriteAndMirror(logsDir, db, auditlog.Event{Event: "security.private_key_exported", Category: "security", Severity: "high", Actor: auditlog.Actor{Type: "operator", ID: operator, IP: &ip}, Details: map[string]any{"reason": reason, "path": filepath.Base(args[1])}, Result: "success"}, time.Now()); err != nil {
		return errors.Join(fmt.Errorf("write private key export audit event: %w", err), os.Remove(args[1]))
	}
	return nil
}

func importPGP(source string, cfg *config.Config, db *sql.DB, keyDir string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(data))
	if err != nil || len(entities) == 0 || entities[0].PrimaryKey == nil || entities[0].PrivateKey == nil {
		return errors.New("input is not a valid armored private key")
	}
	fingerprint, err := securitykey.Fingerprint(data)
	if err != nil {
		return err
	}
	emailMatch := false
	for _, identity := range entities[0].Identities {
		if identity.UserId != nil && strings.EqualFold(identity.UserId.Email, pgpSecurityEmail(cfg)) && strings.EqualFold(identity.UserId.Name, pgpIdentityName()) {
			emailMatch = true
		}
	}
	if !emailMatch {
		fmt.Fprintf(os.Stderr, "Warning: imported key identity does not match %s <%s>. Type OVERRIDE to continue: ", pgpIdentityName(), pgpSecurityEmail(cfg))
		var confirmation string
		if _, err := fmt.Fscanln(os.Stdin, &confirmation); err != nil || confirmation != "OVERRIDE" {
			return errors.New("identity mismatch override was not confirmed")
		}
	}
	if installedPublic, readErr := os.ReadFile(filepath.Join(keyDir, "pgp.pub.asc")); readErr == nil {
		installedFingerprint, fingerprintErr := securitykey.Fingerprint(installedPublic)
		if fingerprintErr != nil {
			return fingerprintErr
		}
		if installedFingerprint != fingerprint {
			var reportCount int
			if err := db.QueryRow(`SELECT COUNT(*) FROM security_reports WHERE body_alg = 'pgp'`).Scan(&reportCount); err != nil {
				return fmt.Errorf("check retained PGP reports before import: %w", err)
			}
			if reportCount > 0 {
				return errors.New("import would make reports encrypted to the installed key unreadable; rotate the installed key instead")
			}
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	secret, err := installationSecret(context.Background(), db)
	if err != nil {
		return err
	}
	sealed, err := securitykey.SealPrivate(secret, data)
	if err != nil {
		return err
	}
	created := entities[0].PrimaryKey.CreationTime
	expires := created.Add(2 * 365 * 24 * time.Hour)
	if err := writePGPFile(filepath.Join(keyDir, "pgp.priv.asc.enc"), sealed); err != nil {
		return err
	}
	public := publicArmor(entities[0])
	if len(public) == 0 {
		return errors.New("serialize imported public key")
	}
	if err := writePGPFile(filepath.Join(keyDir, "pgp.pub.asc"), public); err != nil {
		return err
	}
	return database.SavePGPKeyMetadata(context.Background(), db, database.PGPKeyMetadata{Fingerprint: fingerprint, CreatedAt: created, ExpiresAt: expires, KeyserversPublished: map[string]string{}})
}

func publicArmor(entity *openpgp.Entity) []byte {
	var output bytes.Buffer
	writer, err := armor.Encode(&output, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil
	}
	if err := entity.Serialize(writer); err != nil {
		if closeErr := writer.Close(); closeErr != nil {
			return nil
		}
		return nil
	}
	if writer.Close() != nil {
		return nil
	}
	return output.Bytes()
}

func deletePGP(configPath string, db *sql.DB, keyDir string) error {
	metadata, exists, err := database.GetPGPKeyMetadata(context.Background(), db)
	if err != nil {
		return err
	}
	for _, name := range []string{"pgp.priv.asc.enc", "pgp.priv.old.asc.enc", "pgp.pub.asc"} {
		if err := os.Remove(filepath.Join(keyDir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if exists {
		if err := database.RevokePGPKeyMetadata(context.Background(), db, metadata.Fingerprint); err != nil {
			return err
		}
	}
	if err := setPGPPublishFalse(configPath); err != nil {
		return err
	}
	return nil
}

func setPGPPublishFalse(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("configuration document root must be a YAML mapping")
	}
	root := document.Content[0]
	web := yamlMappingValue(root, "web", true)
	security := yamlMappingValue(web, "security", true)
	yamlSetMappingValue(security, "publish_pgp_key", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"})
	encoded, err := yaml.Marshal(&document)
	if err != nil {
		return err
	}
	return writePGPFile(configPath, encoded)
}
