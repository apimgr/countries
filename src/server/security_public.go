package server

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/apimgr/countries/src/paths"
)

type securityThanksEntry struct {
	Credit   string
	Date     string
	Severity string
}

func (s *Server) securityPolicy(r *http.Request) string {
	policy := strings.TrimSpace(s.config.Web.Security.Policy)
	if policy != "" {
		return policy
	}
	appName := strings.TrimSpace(s.config.Server.Branding.Title)
	if appName == "" {
		appName = "Countries API"
	}
	publicURL := strings.TrimPrefix(strings.TrimPrefix(s.BuildURL(r, ""), "https://"), "http://")
	fqdn, _, _ := strings.Cut(publicURL, "/")
	fqdn = strings.TrimSpace(fqdn)
	if fqdn == "" {
		fqdn = "the configured host"
	}
	return fmt.Sprintf("## Scope\nThe %s application served at %s, including its API and other configured public hosts, Tor address, and I2P address when published.\n\n## Out of scope\nDenial of service and volumetric testing, social engineering of operators or users, physical attacks, findings in third-party services or dependencies without an exploitable path here, automated scanner output without a demonstrated impact, and missing best-practice headers on non-sensitive pages.\n\n## Coordinated disclosure\nPlease report privately through [/server/security](/server/security) and allow 90 days from the report to publish; the window ends early once a fix is released and may be extended by agreement.\n\n## Safe harbor\nGood-faith research that follows this policy, avoids privacy violations and service disruption, accesses only what is needed to show the issue and stops when it has done so is authorized, and no legal action will be taken for it.\n\n## Reporting\nPlease report through [/server/security](/server/security).", appName, fqdn)
}

func (s *Server) securityPublicKeyPath() string {
	configDir := strings.TrimSpace(s.config.ConfigDir)
	if configDir == "" {
		configDir = paths.GetConfigDir("countries")
	}
	return filepath.Join(configDir, "security", "pgp.pub.asc")
}

func (s *Server) readSecurityPublicKey() ([]byte, string, error) {
	data, err := os.ReadFile(s.securityPublicKeyPath())
	if err != nil {
		return nil, "", err
	}
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(string(data)))
	if err != nil || len(entities) == 0 || entities[0] == nil || entities[0].PrimaryKey == nil {
		return nil, "", fmt.Errorf("invalid armored public key")
	}
	return data, strings.ToUpper(hex.EncodeToString(entities[0].PrimaryKey.Fingerprint[:])), nil
}

func (s *Server) handlePGPPublicKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, _, err := s.readSecurityPublicKey()
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Public key unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/pgp-keys")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}

func (s *Server) securityThanks() ([]securityThanksEntry, error) {
	entries := make([]securityThanksEntry, 0)
	if s.db == nil {
		return entries, nil
	}
	rows, err := s.db.Query(`SELECT credit, strftime('%Y-%m-%d', disclosed_on), severity FROM security_thanks ORDER BY disclosed_on DESC, tracking_id DESC`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var entry securityThanksEntry
		if err := rows.Scan(&entry.Credit, &entry.Date, &entry.Severity); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *Server) handleSecurityPolicyAPI(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, map[string]interface{}{"ok": true, "data": map[string]string{"policy": s.securityPolicy(r)}})
}

func (s *Server) handleSecurityThanksAPI(w http.ResponseWriter, r *http.Request) {
	entries, err := s.securityThanks()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load acknowledgments")
		return
	}
	items := make([]map[string]string, 0, len(entries))
	for _, entry := range entries {
		items = append(items, map[string]string{"credit": entry.Credit, "date": entry.Date, "severity": entry.Severity})
	}
	s.jsonResponse(w, map[string]interface{}{"ok": true, "data": map[string]interface{}{"entries": items}})
}
