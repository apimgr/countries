// Package securitydata downloads and manages external security databases and blocklists.
package securitydata

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

const (
	FireHOLID           = "firehol_level1"
	FireHOLURL          = "https://raw.githubusercontent.com/firehol/blocklist-ipsets/master/firehol_level1.netset"
	NVDURL              = "https://services.nvd.nist.gov/rest/json/cves/2.0"
	TrivyLayerMediaType = "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"
	BlocklistMaxAge     = 72 * time.Hour
)

type BlocklistSource struct {
	ID      string `json:"id" yaml:"id"`
	Type    string `json:"type" yaml:"type"`
	URL     string `json:"url" yaml:"url"`
	Enabled *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

type Options struct {
	SecurityDir       string
	BlocklistsEnabled bool
	ExtraBlocklists   []BlocklistSource
	NVDAPIKey         string
	NVDSource         string
	TrivyEnabled      bool
	TrivySource       string
	HTTPClient        *http.Client
	Logger            *log.Logger
	TrustedRanges     []netip.Prefix
	Now               func() time.Time
}

type Health struct {
	Status      string    `json:"status"`
	LastUpdated time.Time `json:"last_updated,omitempty"`
	Skipped     int       `json:"skipped,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type Manager struct {
	opts        Options
	client      *http.Client
	now         func() time.Time
	mu          sync.RWMutex
	ipLists     map[string][]netip.Prefix
	domainLists map[string][]string
	health      map[string]Health
	nvdMu       sync.Mutex
	limiter     *rateLimiter
}

type rateLimiter struct {
	mu     sync.Mutex
	starts []time.Time
	max    int
	now    func() time.Time
}

func (l *rateLimiter) wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := l.now()
		cut := now.Add(-30 * time.Second)
		kept := l.starts[:0]
		for _, t := range l.starts {
			if t.After(cut) {
				kept = append(kept, t)
			}
		}
		l.starts = kept
		if len(l.starts) < l.max {
			l.starts = append(l.starts, now)
			l.mu.Unlock()
			return nil
		}
		wait := l.starts[0].Add(30 * time.Second).Sub(now)
		l.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func New(opts Options) (*Manager, error) {
	if strings.TrimSpace(opts.SecurityDir) == "" {
		return nil, errors.New("security data directory is required")
	}
	if opts.NVDSource == "" {
		opts.NVDSource = NVDURL
	}
	if opts.TrivySource == "" {
		opts.TrivySource = "ghcr.io/aquasecurity/trivy-db:2"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	seen := map[string]bool{FireHOLID: true}
	for i := range opts.ExtraBlocklists {
		s := &opts.ExtraBlocklists[i]
		if !regexp.MustCompile(`^[a-z0-9_]+$`).MatchString(s.ID) || seen[s.ID] {
			return nil, fmt.Errorf("invalid or duplicate blocklist id %q", s.ID)
		}
		seen[s.ID] = true
		if s.Type != "ip" && s.Type != "domain" {
			return nil, fmt.Errorf("blocklist %s has invalid type %q", s.ID, s.Type)
		}
		u, e := url.Parse(s.URL)
		if e != nil || u.Scheme != "https" || u.Host == "" {
			return nil, fmt.Errorf("blocklist %s URL must use HTTPS", s.ID)
		}
	}
	max := 5
	if opts.NVDAPIKey != "" {
		max = 50
	}
	m := &Manager{opts: opts, client: opts.HTTPClient, now: opts.Now, ipLists: map[string][]netip.Prefix{}, domainLists: map[string][]string{}, health: map[string]Health{}, limiter: &rateLimiter{max: max, now: opts.Now}}
	if err := m.loadBlocklists(); err != nil {
		opts.Logger.Printf("load persisted security blocklists: %v", err)
	}
	m.loadDatabaseHealth("cve_nvd", filepath.Join(opts.SecurityDir, "cve"), func() error {
		data, err := os.ReadFile(filepath.Join(opts.SecurityDir, "cve", "nvd.json"))
		if err != nil {
			return err
		}
		var records []json.RawMessage
		if err := json.Unmarshal(data, &records); err != nil || len(records) == 0 {
			return errors.New("NVD database is empty or invalid")
		}
		for _, record := range records {
			var item struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(record, &item); err != nil || item.ID == "" {
				return errors.New("NVD database contains an invalid record")
			}
		}
		return nil
	})
	if opts.TrivyEnabled {
		m.loadDatabaseHealth("trivy_db", filepath.Join(opts.SecurityDir, "trivy"), func() error {
			if info, err := os.Stat(filepath.Join(opts.SecurityDir, "trivy", "trivy.db")); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
				return errors.New("trivy database is missing or empty")
			}
			data, err := os.ReadFile(filepath.Join(opts.SecurityDir, "trivy", "metadata.json"))
			if err != nil {
				return err
			}
			var metadata map[string]any
			if err := json.Unmarshal(data, &metadata); err != nil || len(metadata) == 0 {
				return errors.New("trivy metadata is empty or invalid")
			}
			return nil
		})
	}
	return m, nil
}

func (m *Manager) loadDatabaseHealth(key, dir string, validate func() error) {
	health := Health{Status: "error", Error: "database has never loaded"}
	if err := validate(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			health.Error = err.Error()
		}
		m.health[key] = health
		return
	}
	stamp, err := os.ReadFile(filepath.Join(dir, ".last_updated"))
	if err != nil {
		health.Status = "degraded"
		health.Error = "database has no successful update timestamp"
		m.health[key] = health
		return
	}
	updated, err := time.Parse(time.RFC3339, strings.TrimSpace(string(stamp)))
	if err != nil {
		health.Status = "degraded"
		health.Error = "database update timestamp is invalid"
		health.LastUpdated = time.Time{}
		m.health[key] = health
		return
	}
	health.LastUpdated = updated
	health.Error = ""
	health.Status = "healthy"
	if m.now().Sub(updated) > BlocklistMaxAge {
		health.Status = "degraded"
	}
	m.health[key] = health
}

func (m *Manager) UpdateBlocklists(ctx context.Context) error {
	sources := []BlocklistSource{{ID: FireHOLID, Type: "ip", URL: FireHOLURL}}
	if m.opts.BlocklistsEnabled {
		for _, s := range m.opts.ExtraBlocklists {
			if s.Enabled == nil || *s.Enabled {
				sources = append(sources, s)
			}
		}
	}
	parsed := make(map[string]struct {
		source  BlocklistSource
		ips     []netip.Prefix
		domains []string
		skipped int
	})
	for _, s := range sources {
		raw, err := m.fetch(ctx, s.URL)
		var ips []netip.Prefix
		var domains []string
		skipped := 0
		if err == nil {
			if s.Type == "ip" {
				if s.ID == FireHOLID {
					ips, skipped, err = parseFireHOL(raw, m.opts.TrustedRanges)
				} else {
					ips, skipped, err = parseIPs(raw, m.opts.TrustedRanges)
				}
			} else {
				domains, skipped, err = parseDomains(raw)
			}
		}
		if err != nil {
			m.recordFailure("blocklist_"+s.ID, err)
			return fmt.Errorf("%s: %w", s.ID, err)
		}
		parsed[s.ID] = struct {
			source  BlocklistSource
			ips     []netip.Prefix
			domains []string
			skipped int
		}{s, ips, domains, skipped}
	}
	dir := filepath.Join(m.opts.SecurityDir, "blocklists")
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".blocklists-update-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(tmp); err != nil {
			m.opts.Logger.Printf("remove temporary blocklist directory: %v", err)
		}
	}()
	if entries, e := os.ReadDir(dir); e == nil {
		for _, entry := range entries {
			if !entry.Type().IsRegular() {
				continue
			}
			if _, replace := parsed[strings.TrimSuffix(entry.Name(), ".txt")]; replace && strings.HasSuffix(entry.Name(), ".txt") {
				continue
			}
			data, e := os.ReadFile(filepath.Join(dir, entry.Name()))
			if e != nil {
				return e
			}
			if e = os.WriteFile(filepath.Join(tmp, entry.Name()), data, 0o600); e != nil {
				return e
			}
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	for id, p := range parsed {
		if err = os.WriteFile(filepath.Join(tmp, id+".txt"), serializeEntries(p.ips, p.domains), 0o600); err != nil {
			return err
		}
	}
	if err = writeUpdatedAt(tmp, m.now()); err != nil {
		return err
	}
	if err = swapDir(tmp, dir); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ipLists = map[string][]netip.Prefix{}
	m.domainLists = map[string][]string{}
	for id, p := range parsed {
		if p.source.Type == "ip" {
			m.ipLists[id] = p.ips
		} else {
			m.domainLists[id] = p.domains
		}
		m.health["blocklist_"+id] = Health{Status: "healthy", LastUpdated: m.now().UTC(), Skipped: p.skipped}
	}
	return nil
}

func (m *Manager) loadBlocklists() error {
	dir := filepath.Join(m.opts.SecurityDir, "blocklists")
	if err := recoverDir(dir); err != nil {
		return err
	}
	sources := []BlocklistSource{{ID: FireHOLID, Type: "ip"}}
	if m.opts.BlocklistsEnabled {
		for _, s := range m.opts.ExtraBlocklists {
			if s.Enabled == nil || *s.Enabled {
				sources = append(sources, s)
			}
		}
	}
	var first error
	for _, s := range sources {
		m.health["blocklist_"+s.ID] = Health{Status: "error", Error: "source has never loaded"}
		b, e := os.ReadFile(filepath.Join(dir, s.ID+".txt"))
		if e != nil {
			if !errors.Is(e, os.ErrNotExist) && first == nil {
				first = e
			}
			continue
		}
		var ips []netip.Prefix
		var domains []string
		var skipped int
		if s.Type == "ip" {
			ips, skipped, e = parseIPs(b, m.opts.TrustedRanges)
		} else {
			domains, skipped, e = parseDomains(b)
		}
		if e != nil {
			m.health["blocklist_"+s.ID] = Health{Status: "error", Error: "persisted source failed validation: " + e.Error()}
			if first == nil {
				first = e
			}
			continue
		}
		stamp, e := os.ReadFile(filepath.Join(dir, ".last_updated"))
		var updated time.Time
		if e == nil {
			updated, e = time.Parse(time.RFC3339, strings.TrimSpace(string(stamp)))
		}
		status := "healthy"
		if e != nil || m.now().Sub(updated) > BlocklistMaxAge {
			status = "degraded"
		}
		m.health["blocklist_"+s.ID] = Health{Status: status, LastUpdated: updated, Skipped: skipped}
		if s.Type == "ip" {
			m.ipLists[s.ID] = ips
		} else {
			m.domainLists[s.ID] = domains
		}
	}
	return first
}

func parseIPs(raw []byte, trusted []netip.Prefix) ([]netip.Prefix, int, error) {
	var out []netip.Prefix
	skipped := 0
	reserved := []netip.Prefix{netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("fe80::/10"), netip.MustParsePrefix("100.64.0.0/10")}
	reserved = append(reserved, trusted...)
	for _, line := range strings.Split(string(raw), "\n") {
		v := cleanLine(line)
		if v == "" {
			continue
		}
		p, e := parsePrefix(v)
		if e != nil {
			skipped++
			continue
		}
		excluded := false
		for _, t := range reserved {
			if p.Overlaps(t) {
				excluded = true
				break
			}
		}
		if excluded {
			skipped++
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, skipped, errors.New("blocklist contains no valid entries")
	}
	return out, skipped, nil
}

func parseFireHOL(raw []byte, trusted []netip.Prefix) ([]netip.Prefix, int, error) {
	valid := make([]string, 0)
	skipped := 0
	for _, line := range strings.Split(string(raw), "\n") {
		v := strings.TrimSpace(line)
		if v == "" || strings.HasPrefix(v, "#") {
			continue
		}
		if strings.ContainsAny(v, " \t;") {
			skipped++
			continue
		}
		valid = append(valid, v)
	}
	ips, filtered, err := parseIPs([]byte(strings.Join(valid, "\n")), trusted)
	return ips, skipped + filtered, err
}

func parsePrefix(s string) (netip.Prefix, error) {
	if a, e := netip.ParseAddr(s); e == nil {
		return netip.PrefixFrom(a, a.BitLen()), nil
	}
	return netip.ParsePrefix(s)
}

func cleanLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, ";") {
		return ""
	}
	if i := strings.IndexAny(s, " \t;"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func parseDomains(raw []byte) ([]string, int, error) {
	out := []string{}
	skipped := 0
	for _, line := range strings.Split(string(raw), "\n") {
		v := cleanLine(line)
		if v == "" {
			continue
		}
		wildcard := strings.HasPrefix(v, "*.")
		if wildcard {
			v = strings.TrimPrefix(v, "*.")
		}
		host, e := normalizeHost(v)
		if e != nil {
			skipped++
			continue
		}
		if wildcard {
			suffix, icann := publicsuffix.PublicSuffix(host)
			if icann && suffix == host {
				skipped++
				continue
			}
			host = "*." + host
		}
		out = append(out, host)
	}
	if len(out) == 0 {
		return nil, skipped, errors.New("blocklist contains no valid entries")
	}
	return out, skipped, nil
}

func normalizeHost(v string) (string, error) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return "", errors.New("empty host")
	}
	if u, e := url.Parse(v); e == nil && u.Host != "" {
		v = u.Hostname()
	} else {
		if at := strings.LastIndex(v, "@"); at >= 0 {
			v = v[at+1:]
		}
		if h, _, e := net.SplitHostPort(v); e == nil {
			v = h
		} else {
			v = strings.TrimSuffix(v, ".")
		}
	}
	v = strings.TrimSuffix(v, ".")
	if net.ParseIP(v) != nil {
		return "", errors.New("IP literal")
	}
	ascii, e := idna.Lookup.ToASCII(v)
	if e != nil || len(ascii) > 253 {
		return "", errors.New("invalid domain")
	}
	for _, label := range strings.Split(ascii, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("invalid domain")
		}
	}
	return ascii, nil
}

func (m *Manager) MatchIP(ip netip.Addr) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, ps := range m.ipLists {
		for _, p := range ps {
			if p.Contains(ip) {
				return id, true
			}
		}
	}
	return "", false
}

func (m *Manager) MatchDomain(value string) (string, bool) {
	host, e := normalizeHost(value)
	if e != nil {
		return "", false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, ds := range m.domainLists {
		for _, d := range ds {
			if d == host {
				return id, true
			}
			if strings.HasPrefix(d, "*.") && strings.HasSuffix(host, d[1:]) && host != d[2:] {
				return id, true
			}
		}
	}
	return "", false
}

func (m *Manager) Health() map[string]Health {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := map[string]Health{}
	for k, v := range m.health {
		if (strings.HasPrefix(k, "blocklist_") || k == "cve_nvd" || k == "trivy_db") && v.Status == "healthy" && m.now().Sub(v.LastUpdated) > BlocklistMaxAge {
			v.Status = "degraded"
		}
		v.Error = ""
		r[k] = v
	}
	return r
}

func (m *Manager) UpdateCVE(ctx context.Context) error {
	var errs []error
	if e := m.UpdateNVD(ctx); e != nil {
		errs = append(errs, e)
	}
	if m.opts.TrivyEnabled {
		if e := m.UpdateTrivy(ctx); e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}

type nvdResponse struct {
	ResultsPerPage  int `json:"resultsPerPage"`
	StartIndex      int `json:"startIndex"`
	TotalResults    int `json:"totalResults"`
	Vulnerabilities []struct {
		CVE json.RawMessage `json:"cve"`
	} `json:"vulnerabilities"`
}

func (m *Manager) UpdateNVD(ctx context.Context) error {
	err := m.updateNVD(ctx)
	if err != nil {
		m.recordFailure("cve_nvd", err)
	}
	return err
}

func (m *Manager) updateNVD(ctx context.Context) error {
	m.nvdMu.Lock()
	defer m.nvdMu.Unlock()
	dir := filepath.Join(m.opts.SecurityDir, "cve")
	path := filepath.Join(dir, "nvd.json")
	if err := recoverDir(dir); err != nil {
		return err
	}
	old := map[string]json.RawMessage{}
	b, err := os.ReadFile(path)
	if err == nil {
		var arr []json.RawMessage
		if json.Unmarshal(b, &arr) == nil {
			for _, v := range arr {
				var x struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(v, &x) == nil && x.ID != "" {
					old[x.ID] = v
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	start := m.now().UTC().Add(-24 * time.Hour)
	if fi, e := os.Stat(filepath.Join(dir, ".last_updated")); e == nil {
		if x, e := os.ReadFile(filepath.Join(dir, ".last_updated")); e == nil {
			if t, e := time.Parse(time.RFC3339, strings.TrimSpace(string(x))); e == nil && t.Before(m.now()) {
				start = t
			}
		}
		_ = fi
	}
	end := m.now().UTC()
	full := len(old) == 0
	windows := [][2]time.Time{}
	if full {
		windows = append(windows, [2]time.Time{})
	} else {
		for w := start; w.Before(end); {
			n := w.Add(120 * 24 * time.Hour)
			if n.After(end) {
				n = end
			}
			windows = append(windows, [2]time.Time{w, n})
			w = n
		}
	}
	for _, window := range windows {
		idx := 0
		total := 1
		for idx < total {
			if err := m.limiter.wait(ctx); err != nil {
				return err
			}
			u, e := url.Parse(m.opts.NVDSource)
			if e != nil {
				return e
			}
			q := u.Query()
			q.Set("resultsPerPage", "2000")
			q.Set("startIndex", fmt.Sprint(idx))
			if !full {
				q.Set("lastModStartDate", window[0].Format("2006-01-02T15:04:05.000Z"))
				q.Set("lastModEndDate", window[1].Format("2006-01-02T15:04:05.000Z"))
			}
			u.RawQuery = q.Encode()
			req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
			if e != nil {
				return e
			}
			if m.opts.NVDAPIKey != "" {
				req.Header.Set("apiKey", m.opts.NVDAPIKey)
			}
			resp, e := m.client.Do(req)
			if e != nil {
				return e
			}
			if resp.StatusCode == 403 || resp.StatusCode == 429 {
				if closeErr := resp.Body.Close(); closeErr != nil {
					m.opts.Logger.Printf("close NVD rate-limit response: %v", closeErr)
				}
				err = fmt.Errorf("NVD rate limit/access response %d", resp.StatusCode)
				m.recordFailure("cve_nvd", err)
				return err
			}
			if resp.StatusCode/100 != 2 {
				if closeErr := resp.Body.Close(); closeErr != nil {
					m.opts.Logger.Printf("close NVD error response: %v", closeErr)
				}
				return fmt.Errorf("NVD returned HTTP %d", resp.StatusCode)
			}
			var page nvdResponse
			e = json.NewDecoder(resp.Body).Decode(&page)
			if closeErr := resp.Body.Close(); closeErr != nil {
				m.opts.Logger.Printf("close NVD response: %v", closeErr)
			}
			if e != nil {
				return e
			}
			total = page.TotalResults
			if total == 0 {
				break
			}
			for _, v := range page.Vulnerabilities {
				var x struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(v.CVE, &x) == nil && x.ID != "" {
					old[x.ID] = append(json.RawMessage(nil), v.CVE...)
				}
			}
			if page.ResultsPerPage > 0 {
				idx += page.ResultsPerPage
			} else {
				idx += 2000
			}
			if idx == 0 {
				break
			}
		}
	}
	if len(old) == 0 {
		return errors.New("NVD response contains no CVE records")
	}
	out := make([]json.RawMessage, 0, len(old))
	for _, v := range old {
		out = append(out, v)
	}
	encoded, e := json.Marshal(out)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(dir), 0o755); e != nil {
		return e
	}
	tmp, e := os.MkdirTemp(filepath.Dir(dir), ".cve-update-")
	if e != nil {
		return e
	}
	defer func() {
		if err := os.RemoveAll(tmp); err != nil {
			m.opts.Logger.Printf("remove temporary NVD directory: %v", err)
		}
	}()
	if e = os.WriteFile(filepath.Join(tmp, "nvd.json"), encoded, 0o600); e != nil {
		return e
	}
	if e = writeUpdatedAt(tmp, m.now()); e != nil {
		return e
	}
	if e = swapDir(tmp, dir); e != nil {
		return e
	}
	m.setHealth("cve_nvd", Health{Status: "healthy", LastUpdated: m.now().UTC()})
	return nil
}

func (m *Manager) UpdateTrivy(ctx context.Context) error {
	err := m.updateTrivy(ctx)
	if err != nil {
		m.recordFailure("trivy_db", err)
	}
	return err
}

func (m *Manager) updateTrivy(ctx context.Context) error {
	dir := filepath.Join(m.opts.SecurityDir, "trivy")
	if err := recoverDir(dir); err != nil {
		return err
	}
	if b, e := os.ReadFile(filepath.Join(dir, "metadata.json")); e == nil {
		var md struct {
			NextUpdate time.Time `json:"NextUpdate"`
		}
		if json.Unmarshal(b, &md) == nil && md.NextUpdate.After(m.now()) {
			m.setHealth("trivy_db", Health{Status: "healthy", LastUpdated: md.NextUpdate.Add(-24 * time.Hour)})
			return nil
		}
	}
	registry, repo, tag, e := parseOCIRef(m.opts.TrivySource)
	if e != nil {
		return e
	}
	manifestURL := "https://" + registry + "/v2/" + repo + "/manifests/" + url.PathEscape(tag)
	manifestBytes, e := ociGet(ctx, m.client, repo, manifestURL, "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	if e != nil {
		return e
	}
	var manifest struct {
		Layers []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
		} `json:"layers"`
	}
	if e = json.Unmarshal(manifestBytes, &manifest); e != nil {
		return e
	}
	digest := ""
	for _, l := range manifest.Layers {
		if l.MediaType == TrivyLayerMediaType {
			digest = l.Digest
			break
		}
	}
	if digest == "" {
		return errors.New("trivy manifest has no database layer")
	}
	blobURL := "https://" + registry + "/v2/" + repo + "/blobs/" + digest
	data, e := ociGet(ctx, m.client, repo, blobURL, "application/octet-stream")
	if e != nil {
		return e
	}
	if len(data) == 0 {
		return errors.New("empty Trivy layer")
	}
	if e = os.MkdirAll(filepath.Dir(dir), 0o755); e != nil {
		return e
	}
	tmp, e := os.MkdirTemp(filepath.Dir(dir), ".trivy-update-")
	if e != nil {
		return e
	}
	defer func() {
		if err := os.RemoveAll(tmp); err != nil {
			m.opts.Logger.Printf("remove temporary Trivy directory: %v", err)
		}
	}()
	if e = extractTrivy(data, tmp); e != nil {
		return e
	}
	if e = writeUpdatedAt(tmp, m.now()); e != nil {
		return e
	}
	if e = swapDir(tmp, dir); e != nil {
		return e
	}
	m.setHealth("trivy_db", Health{Status: "healthy", LastUpdated: m.now().UTC()})
	return nil
}

func extractTrivy(data []byte, dir string) (resultErr error) {
	g, e := gzip.NewReader(bytes.NewReader(data))
	if e != nil {
		return e
	}
	defer func() {
		resultErr = errors.Join(resultErr, g.Close())
	}()
	tr := tar.NewReader(g)
	found := map[string]bool{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		name := filepath.Base(h.Name)
		if name != "trivy.db" && name != "metadata.json" {
			continue
		}
		if h.Typeflag != tar.TypeReg || h.Size <= 0 || h.Size > 2<<30 {
			return fmt.Errorf("invalid Trivy archive member %q", name)
		}
		f, e := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if e != nil {
			return e
		}
		_, e = io.CopyN(f, tr, h.Size)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		found[name] = true
	}
	if !found["trivy.db"] || !found["metadata.json"] {
		return errors.New("trivy archive is missing required files")
	}
	b, e := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if e != nil {
		return e
	}
	var md map[string]any
	if json.Unmarshal(b, &md) != nil {
		return errors.New("invalid Trivy metadata")
	}
	return nil
}

func parseOCIRef(ref string) (string, string, string, error) {
	if !strings.Contains(ref, "/") {
		return "", "", "", errors.New("invalid OCI reference")
	}
	parts := strings.SplitN(ref, "/", 2)
	registry, rest := parts[0], parts[1]
	if !strings.Contains(registry, ".") && !strings.Contains(registry, ":") {
		registry = "registry-1.docker.io"
	}
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return "", "", "", errors.New("OCI reference requires tag")
	}
	return registry, rest[:i], rest[i+1:], nil
}

func ociGet(ctx context.Context, c *http.Client, repo, target, accept string) ([]byte, error) {
	request := func(token string) (*http.Response, error) {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if e != nil {
			return nil, e
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return c.Do(req)
	}
	resp, e := request("")
	if e != nil {
		return nil, e
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		if err := resp.Body.Close(); err != nil {
			log.Printf("close OCI unauthorized response: %v", err)
		}
		token, e := ociBearerToken(ctx, c, repo, challenge)
		if e != nil {
			return nil, e
		}
		if token == "" {
			return nil, errors.New("OCI registry returned an unsupported authentication challenge")
		}
		resp, e = request(token)
		if e != nil {
			return nil, e
		}
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("close OCI response: %v", err)
		}
	}()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("OCI request returned HTTP %d", resp.StatusCode)
	}
	limit := int64(16 << 20)
	if accept == "application/octet-stream" {
		limit = 2 << 30
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(body)) > limit {
		return nil, errors.New("OCI response exceeds size limit")
	}
	return body, nil
}

func ociBearerToken(ctx context.Context, c *http.Client, repo, challenge string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", errors.New("OCI registry did not provide a bearer challenge")
	}
	params := map[string]string{}
	for _, match := range regexp.MustCompile(`([A-Za-z_]+)="((?:\\.|[^"])*)"`).FindAllStringSubmatch(challenge, -1) {
		params[strings.ToLower(match[1])] = strings.ReplaceAll(match[2], `\"`, `"`)
	}
	realm, e := url.Parse(params["realm"])
	if e != nil || realm.Scheme != "https" || realm.Host == "" {
		return "", errors.New("OCI bearer challenge has an invalid HTTPS realm")
	}
	q := realm.Query()
	if params["service"] != "" {
		q.Set("service", params["service"])
	}
	if params["scope"] != "" {
		q.Set("scope", params["scope"])
	} else {
		q.Set("scope", "repository:"+repo+":pull")
	}
	realm.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if e != nil {
		return "", e
	}
	resp, e := c.Do(req)
	if e != nil {
		return "", e
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("close OCI token response: %v", err)
		}
	}()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("OCI token endpoint returned HTTP %d", resp.StatusCode)
	}
	var token struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token); e != nil {
		return "", e
	}
	if token.Token != "" {
		return token.Token, nil
	}
	if token.AccessToken != "" {
		return token.AccessToken, nil
	}
	return "", errors.New("OCI token response did not contain a token")
}

func (m *Manager) fetch(ctx context.Context, u string) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", u, nil)
	if e != nil {
		return nil, e
	}
	resp, e := m.client.Do(req)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode/100 != 2 {
		return nil, errors.Join(fmt.Errorf("download returned HTTP %d", resp.StatusCode), resp.Body.Close())
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	e = errors.Join(e, resp.Body.Close())
	if e == nil && len(b) == 0 {
		e = errors.New("empty download")
	}
	return b, e
}

func serializeEntries(ips []netip.Prefix, ds []string) []byte {
	var b strings.Builder
	for _, p := range ips {
		b.WriteString(p.String())
		b.WriteByte('\n')
	}
	for _, d := range ds {
		b.WriteString(d)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func atomicFile(path string, data []byte) (resultErr error) {
	if len(data) == 0 {
		return errors.New("refusing empty data")
	}
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0o755); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".security-update-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer func() {
		if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	if _, e = f.Write(data); e != nil {
		return errors.Join(e, f.Close())
	}
	if e = f.Sync(); e != nil {
		return errors.Join(e, f.Close())
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(tmp, path)
}

func writeUpdatedAt(dir string, now time.Time) error {
	return atomicFile(filepath.Join(dir, ".last_updated"), []byte(now.UTC().Format(time.RFC3339)+"\n"))
}

func recoverDir(dest string) error {
	old := dest + ".old"
	_, destErr := os.Stat(dest)
	_, oldErr := os.Stat(old)
	if errors.Is(destErr, os.ErrNotExist) && oldErr == nil {
		return os.Rename(old, dest)
	}
	if destErr == nil && oldErr == nil {
		return os.RemoveAll(old)
	}
	if destErr != nil && !errors.Is(destErr, os.ErrNotExist) {
		return destErr
	}
	if oldErr != nil && !errors.Is(oldErr, os.ErrNotExist) {
		return oldErr
	}
	return nil
}

func swapDir(tmp, dest string) error {
	parent := filepath.Dir(dest)
	if e := os.MkdirAll(parent, 0o755); e != nil {
		return e
	}
	if e := recoverDir(dest); e != nil {
		return e
	}
	old := dest + ".old"
	if _, e := os.Stat(dest); e == nil {
		if e = os.Rename(dest, old); e != nil {
			return e
		}
	}
	if e := os.Rename(tmp, dest); e != nil {
		_ = os.Rename(old, dest)
		return e
	}
	_ = os.RemoveAll(old)
	return nil
}
func (m *Manager) setHealth(k string, h Health) { m.mu.Lock(); m.health[k] = h; m.mu.Unlock() }
func (m *Manager) recordFailure(k string, e error) {
	m.mu.Lock()
	prior, ok := m.health[k]
	prior.Error = e.Error()
	if !ok || prior.LastUpdated.IsZero() {
		prior.Status = "error"
	} else if m.now().Sub(prior.LastUpdated) > BlocklistMaxAge {
		prior.Status = "degraded"
	}
	m.health[k] = prior
	m.mu.Unlock()
	m.opts.Logger.Printf("security data update %s failed: %v", k, e)
}
