// Package geoip downloads and queries the project's ip-location-db datasets.
package geoip

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

const (
	defaultBaseURL  = "https://github.com/sapics/ip-location-db/releases/download"
	maxDatabaseSize = 512 << 20
	maxChecksumSize = 4096
)

var checksumPattern = regexp.MustCompile(`(?i)^[0-9a-f]{64}$`)

// ErrDatabaseUnavailable indicates that an asset has not been downloaded or opened.
var ErrDatabaseUnavailable = errors.New("GeoIP database is unavailable")

// ASNRecord contains the upstream AS number and organization.
type ASNRecord struct {
	ASN uint32 `maxminddb:"autonomous_system_number"`
	Org string `maxminddb:"autonomous_system_organization"`
}

// CountryRecord contains the upstream ISO 3166-1 alpha-2 country code.
type CountryRecord struct {
	CountryCode string `maxminddb:"country_code"`
}

// CityRecord contains the DB-IP city fields defined by the project specification.
type CityRecord struct {
	City        string  `maxminddb:"city"`
	CountryCode string  `maxminddb:"country_code"`
	State1      string  `maxminddb:"state1"`
	State2      string  `maxminddb:"state2"`
	Postcode    string  `maxminddb:"postcode"`
	Latitude    float64 `maxminddb:"latitude"`
	Longitude   float64 `maxminddb:"longitude"`
	Timezone    string  `maxminddb:"timezone"`
}

// DatabaseAsset identifies one canonical ip-location-db release asset.
type DatabaseAsset struct {
	Name string
	File string
}

var databaseAssets = [...]DatabaseAsset{
	{Name: "asn", File: "iptoasn-asn.mmdb"},
	{Name: "country", File: "iptoasn-country.mmdb"},
	{Name: "city_ipv4", File: "dbip-city-ipv4.mmdb"},
	{Name: "city_ipv6", File: "dbip-city-ipv6.mmdb"},
}

type databaseReader interface {
	Lookup(net.IP, any) error
	Close() error
}

// GeoIPManager owns downloaded database files and their open MMDB readers.
type GeoIPManager struct {
	directory string
	client    *http.Client
	baseURL   string

	updateMu sync.Mutex
	mu       sync.RWMutex
	readers  map[string]databaseReader
	open     func(string) (databaseReader, error)
}

// NewManager creates a manager under dataDir/security/geoip.
func NewManager(dataDir string) (*GeoIPManager, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("GeoIP data directory is empty")
	}
	return NewManagerWithHTTP(filepath.Join(dataDir, "security", "geoip"), http.DefaultClient)
}

// NewManagerWithHTTP creates a manager with a caller-supplied HTTP client.
func NewManagerWithHTTP(directory string, client *http.Client) (*GeoIPManager, error) {
	if client == nil {
		client = &http.Client{}
	}
	if client.Timeout == 0 {
		copyClient := *client
		copyClient.Timeout = 30 * time.Second
		client = &copyClient
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create GeoIP directory: %w", err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, fmt.Errorf("secure GeoIP directory: %w", err)
	}
	return newManager(directory, client, defaultBaseURL, func(path string) (databaseReader, error) {
		return maxminddb.Open(path)
	}), nil
}

func newManager(directory string, client *http.Client, baseURL string, open func(string) (databaseReader, error)) *GeoIPManager {
	return &GeoIPManager{
		directory: directory,
		client:    client,
		baseURL:   strings.TrimRight(baseURL, "/"),
		readers:   make(map[string]databaseReader),
		open:      open,
	}
}

// Directory returns the runtime directory containing the MMDB files.
func (m *GeoIPManager) Directory() string {
	return m.directory
}

// MissingDatabases returns canonical asset files that are not present on disk.
func (m *GeoIPManager) MissingDatabases() []string {
	missing := make([]string, 0)
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, asset := range databaseAssets {
		if m.readers[asset.Name] == nil {
			missing = append(missing, asset.File)
		}
	}
	return missing
}

// Open loads all available databases. Missing files are tolerated so lookup
// failures can remain fail-open while an update is pending.
func (m *GeoIPManager) Open() error {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()

	var failures []error
	for _, asset := range databaseAssets {
		path := filepath.Join(m.directory, asset.File)
		reader, err := m.open(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, fmt.Errorf("open %s: %w", asset.File, err))
			}
			continue
		}
		m.replaceReader(asset.Name, reader)
	}
	return errors.Join(failures...)
}

// Update downloads and atomically replaces every canonical database asset.
func (m *GeoIPManager) Update(ctx context.Context) error {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()

	var failures []error
	for _, asset := range databaseAssets {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := m.updateAsset(ctx, asset); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (m *GeoIPManager) updateAsset(ctx context.Context, asset DatabaseAsset) error {
	dataURL := m.baseURL + "/latest/" + asset.File
	checksumURL := m.baseURL + "/checksum/" + asset.File + ".sha256"
	checksumBody, err := m.fetch(ctx, checksumURL, maxChecksumSize)
	if err != nil {
		return fmt.Errorf("download checksum for %s: %w", asset.File, err)
	}
	want, err := parseChecksum(checksumBody)
	if err != nil {
		return fmt.Errorf("parse checksum for %s: %w", asset.File, err)
	}
	data, err := m.fetch(ctx, dataURL, maxDatabaseSize)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset.File, err)
	}
	have := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(have[:], want) != 1 {
		return fmt.Errorf("checksum mismatch for %s", asset.File)
	}
	return m.installAsset(asset, data)
}

func (m *GeoIPManager) fetch(ctx context.Context, source string, maxBytes int64) (data []byte, resultErr error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	response, err := m.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	return data, nil
}

func parseChecksum(data []byte) ([]byte, error) {
	fields := strings.Fields(string(data))
	if len(fields) == 0 || !checksumPattern.MatchString(fields[0]) {
		return nil, fmt.Errorf("checksum response must begin with a 64-character SHA-256 digest")
	}
	return hex.DecodeString(fields[0])
}

func (m *GeoIPManager) installAsset(asset DatabaseAsset, data []byte) (resultErr error) {
	temporary, err := os.CreateTemp(m.directory, ".geoip-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary database: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove temporary database: %w", removeErr))
		}
	}()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary database: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary database: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary database: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary database: %w", err)
	}
	newReader, err := m.open(temporaryPath)
	if err != nil {
		return fmt.Errorf("validate temporary database %s: %w", asset.File, err)
	}
	finalPath := filepath.Join(m.directory, asset.File)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		_ = newReader.Close()
		return fmt.Errorf("install database %s: %w", asset.File, err)
	}
	m.replaceReader(asset.Name, newReader)
	return nil
}

func (m *GeoIPManager) replaceReader(name string, reader databaseReader) {
	m.mu.Lock()
	previous := m.readers[name]
	m.readers[name] = reader
	m.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
}

// LookupASN looks up an address without querying private or non-routable IPs.
func (m *GeoIPManager) LookupASN(ip net.IP) (ASNRecord, bool, error) {
	var record ASNRecord
	if isPrivateOrNonRoutable(ip) {
		return record, false, nil
	}
	if err := m.lookup("asn", ip, &record); err != nil {
		return record, false, err
	}
	return record, record.ASN != 0 || record.Org != "", nil
}

// LookupCountry looks up an address without querying private or non-routable IPs.
func (m *GeoIPManager) LookupCountry(ip net.IP) (CountryRecord, bool, error) {
	var record CountryRecord
	if isPrivateOrNonRoutable(ip) {
		return record, false, nil
	}
	if err := m.lookup("country", ip, &record); err != nil {
		return record, false, err
	}
	record.CountryCode = strings.ToUpper(strings.TrimSpace(record.CountryCode))
	return record, len(record.CountryCode) == 2, nil
}

// LookupCity looks up an address in the database matching its IP family.
func (m *GeoIPManager) LookupCity(ip net.IP) (CityRecord, bool, error) {
	var record CityRecord
	if isPrivateOrNonRoutable(ip) {
		return record, false, nil
	}
	assetName := "city_ipv6"
	if ip.To4() != nil {
		assetName = "city_ipv4"
	}
	if err := m.lookup(assetName, ip, &record); err != nil {
		return record, false, err
	}
	return record, record.CountryCode != "" || record.City != "", nil
}

func (m *GeoIPManager) lookup(name string, ip net.IP, target any) error {
	if ip == nil {
		return errors.New("invalid IP address")
	}
	m.mu.RLock()
	reader := m.readers[name]
	if reader == nil {
		m.mu.RUnlock()
		return ErrDatabaseUnavailable
	}
	err := reader.Lookup(ip, target)
	m.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("lookup %s: %w", name, err)
	}
	return nil
}

// Close releases the currently open MMDB readers.
func (m *GeoIPManager) Close() error {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	m.mu.Lock()
	readers := m.readers
	m.readers = make(map[string]databaseReader)
	m.mu.Unlock()
	var failures []error
	for _, reader := range readers {
		if err := reader.Close(); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func isPrivateOrNonRoutable(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		ip = ipv4
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}
