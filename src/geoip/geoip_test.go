package geoip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/apimgr/countries/src/testutil"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeDatabaseReader struct {
	lookupErr   error
	lookupCalls int
	closed      bool
}

func (r *fakeDatabaseReader) Lookup(_ net.IP, target any) error {
	r.lookupCalls++
	if r.lookupErr != nil {
		return r.lookupErr
	}
	switch record := target.(type) {
	case *ASNRecord:
		record.ASN = 64512
		record.Org = "Example Network"
	case *CountryRecord:
		record.CountryCode = " us "
	case *CityRecord:
		record.City = "Test City"
		record.CountryCode = "US"
	}
	return nil
}

func (r *fakeDatabaseReader) Close() error {
	r.closed = true
	return nil
}

func testManager(directory, baseURL string, client *http.Client) *GeoIPManager {
	if client == nil {
		client = http.DefaultClient
	}
	return newManager(directory, client, baseURL, func(string) (databaseReader, error) {
		return &fakeDatabaseReader{}, nil
	})
}

func TestDatabaseAssetsMatchCanonicalUpstreamFiles(t *testing.T) {
	want := []string{"iptoasn-asn.mmdb", "iptoasn-country.mmdb", "dbip-city-ipv4.mmdb", "dbip-city-ipv6.mmdb"}
	if len(databaseAssets) != len(want) {
		t.Fatalf("got %d assets, want %d", len(databaseAssets), len(want))
	}
	for index, asset := range databaseAssets {
		if asset.File != want[index] {
			t.Errorf("asset %d file = %q, want %q", index, asset.File, want[index])
		}
	}
}

func TestParseChecksum(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, input := range []string{digest, digest + "  file.mmdb\n", digest + " *file.mmdb\n"} {
		got, err := parseChecksum([]byte(input))
		if err != nil {
			t.Errorf("parseChecksum(%q) error = %v", input, err)
			continue
		}
		if hex.EncodeToString(got) != digest {
			t.Errorf("parseChecksum(%q) = %x, want %s", input, got, digest)
		}
	}
	for _, input := range []string{"", strings.Repeat("a", 63), strings.Repeat("g", 64)} {
		if _, err := parseChecksum([]byte(input)); err == nil {
			t.Errorf("parseChecksum(%q) accepted invalid digest", input)
		}
	}
}

func TestUpdateChecksumsBeforeReplacingFiles(t *testing.T) {
	directory := testutil.TempDir(t)
	data := []byte("replacement")
	digest := sha256.Sum256(data)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/checksum/") {
			_, _ = fmt.Fprintf(w, "%x  %s\n", digest, filepath.Base(strings.TrimSuffix(r.URL.Path, ".sha256")))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/latest/") {
			_, _ = w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	manager := testManager(directory, server.URL, server.Client())
	if err := manager.Update(context.Background()); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	for _, asset := range databaseAssets {
		path := filepath.Join(directory, asset.File)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", asset.File, err)
		}
		if string(got) != string(data) {
			t.Errorf("%s content = %q, want %q", asset.File, got, data)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", asset.File, err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("%s permissions = %04o, want 0600", asset.File, info.Mode().Perm())
		}
	}
}

func TestUpdateChecksumMismatchPreservesExistingDatabase(t *testing.T) {
	directory := testutil.TempDir(t)
	path := filepath.Join(directory, databaseAssets[0].File)
	if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/checksum/") {
			_, _ = io.WriteString(w, strings.Repeat("0", 64)+"\n")
			return
		}
		_, _ = io.WriteString(w, "unverified replacement")
	}))
	defer server.Close()
	manager := testManager(directory, server.URL, server.Client())
	if err := manager.Update(context.Background()); err == nil {
		t.Fatal("Update() error = nil, want checksum failures")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing" {
		t.Fatalf("existing database was replaced with %q", got)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("temporary download %q was not removed", entry.Name())
		}
	}
}

func TestFetchRejectsNonSuccessAndOversizedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/not-found" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "four bytes")
	}))
	defer server.Close()
	manager := testManager(testutil.TempDir(t), server.URL, server.Client())
	if _, err := manager.fetch(context.Background(), server.URL+"/not-found", 100); err == nil {
		t.Fatal("fetch() accepted an HTTP 404")
	}
	if _, err := manager.fetch(context.Background(), server.URL+"/large", 4); err == nil {
		t.Fatal("fetch() accepted a body larger than its limit")
	}
}

func TestLookupsSkipPrivateAddressesAndFailOpenForMissingDatabase(t *testing.T) {
	manager := testManager(testutil.TempDir(t), "", nil)
	privateIPs := []string{"10.0.0.1", "172.16.0.1", "192.168.1.1", "127.0.0.1", "::1", "fc00::1"}
	for _, value := range privateIPs {
		if record, found, err := manager.LookupCountry(net.ParseIP(value)); err != nil || found || record.CountryCode != "" {
			t.Errorf("LookupCountry(%s) = (%+v, %t, %v), want empty/no lookup/no error", value, record, found, err)
		}
	}
	if _, found, err := manager.LookupCountry(net.ParseIP("8.8.8.8")); !errors.Is(err, ErrDatabaseUnavailable) || found {
		t.Errorf("LookupCountry(public) error = %v, found = %t, want database-unavailable and no result", err, found)
	}
}

func TestPrivateOrNonRoutableAddressCheck(t *testing.T) {
	for _, value := range []string{"0.0.0.0", "224.0.0.1", "169.254.0.1", "fe80::1", "ff02::1"} {
		if !isPrivateOrNonRoutable(net.ParseIP(value)) {
			t.Errorf("isPrivateOrNonRoutable(%s) = false, want true", value)
		}
	}
	if isPrivateOrNonRoutable(net.ParseIP("1.1.1.1")) {
		t.Error("isPrivateOrNonRoutable(1.1.1.1) = true, want false")
	}
}

func TestManagerLifecycleAndPublicLookups(t *testing.T) {
	if _, err := NewManager(" "); err == nil {
		t.Fatal("NewManager accepted an empty data directory")
	}
	directory := filepath.Join(testutil.TempDir(t), "security", "geoip")
	manager, err := NewManagerWithHTTP(directory, nil)
	if err != nil {
		t.Fatalf("NewManagerWithHTTP() error = %v", err)
	}
	if manager.Directory() != directory || manager.client.Timeout != 30*time.Second {
		t.Fatalf("manager directory/timeout = %q/%s", manager.Directory(), manager.client.Timeout)
	}
	if info, err := os.Stat(directory); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("GeoIP directory mode = %v, err = %v", info.Mode().Perm(), err)
	}

	asnReader := &fakeDatabaseReader{}
	countryReader := &fakeDatabaseReader{}
	ipv4Reader := &fakeDatabaseReader{}
	ipv6Reader := &fakeDatabaseReader{}
	manager.readers = map[string]databaseReader{
		"asn": asnReader, "country": countryReader,
		"city_ipv4": ipv4Reader, "city_ipv6": ipv6Reader,
	}
	if missing := manager.MissingDatabases(); len(missing) != 0 {
		t.Fatalf("MissingDatabases() = %v with all readers loaded", missing)
	}

	asn, found, err := manager.LookupASN(net.ParseIP("8.8.8.8"))
	if err != nil || !found || asn.ASN != 64512 || asn.Org != "Example Network" {
		t.Fatalf("LookupASN() = %+v, %t, %v", asn, found, err)
	}
	country, found, err := manager.LookupCountry(net.ParseIP("8.8.8.8"))
	if err != nil || !found || country.CountryCode != "US" {
		t.Fatalf("LookupCountry() = %+v, %t, %v", country, found, err)
	}
	city4, found, err := manager.LookupCity(net.ParseIP("8.8.8.8"))
	if err != nil || !found || city4.City != "Test City" || ipv4Reader.lookupCalls != 1 || ipv6Reader.lookupCalls != 0 {
		t.Fatalf("IPv4 LookupCity() = %+v, %t, %v; reader counts = %d/%d", city4, found, err, ipv4Reader.lookupCalls, ipv6Reader.lookupCalls)
	}
	city6, found, err := manager.LookupCity(net.ParseIP("2001:4860::1"))
	if err != nil || !found || city6.City != "Test City" || ipv6Reader.lookupCalls != 1 {
		t.Fatalf("IPv6 LookupCity() = %+v, %t, %v; IPv6 lookups = %d", city6, found, err, ipv6Reader.lookupCalls)
	}
	if _, found, err := manager.LookupCity(net.ParseIP("127.0.0.1")); err != nil || found || ipv4Reader.lookupCalls != 1 {
		t.Fatalf("private LookupCity() = found %t, err %v, IPv4 lookups %d", found, err, ipv4Reader.lookupCalls)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	for _, reader := range []*fakeDatabaseReader{asnReader, countryReader, ipv4Reader, ipv6Reader} {
		if !reader.closed {
			t.Error("Close() left an MMDB reader open")
		}
	}
	if len(manager.MissingDatabases()) != len(databaseAssets) {
		t.Fatal("Close() did not clear the active reader set")
	}
}

func TestManagerOpenToleratesMissingAssetsAndReportsInvalidFiles(t *testing.T) {
	manager := testManager(testutil.TempDir(t), "", nil)
	loaded := &fakeDatabaseReader{}
	manager.open = func(path string) (databaseReader, error) {
		if filepath.Base(path) == databaseAssets[0].File {
			return loaded, nil
		}
		return nil, os.ErrNotExist
	}
	if err := manager.Open(); err != nil {
		t.Fatalf("Open() with missing assets = %v", err)
	}
	missing := manager.MissingDatabases()
	if len(missing) != len(databaseAssets)-1 || loaded.closed {
		t.Fatalf("missing assets = %v, loaded reader closed=%t", missing, loaded.closed)
	}
	if err := manager.Close(); err != nil || !loaded.closed {
		t.Fatalf("Close() error = %v, reader closed=%t", err, loaded.closed)
	}

	invalid := testManager(testutil.TempDir(t), "", nil)
	invalid.open = func(string) (databaseReader, error) { return nil, errors.New("invalid database") }
	if err := invalid.Open(); err == nil || !strings.Contains(err.Error(), databaseAssets[0].File) {
		t.Fatalf("Open() with invalid files = %v", err)
	}
	if err := invalid.Close(); err != nil {
		t.Fatalf("Close() after failed Open() = %v", err)
	}
}

func TestManagerLookupsFailOpenForMissingAndInvalidAddresses(t *testing.T) {
	manager := testManager(testutil.TempDir(t), "", nil)
	if _, _, err := manager.LookupASN(net.ParseIP("8.8.8.8")); !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("LookupASN() without database = %v", err)
	}
	if err := manager.lookup("country", nil, &CountryRecord{}); err == nil || !strings.Contains(err.Error(), "invalid IP") {
		t.Fatalf("lookup(nil) = %v", err)
	}
	reader := &fakeDatabaseReader{lookupErr: errors.New("bad record")}
	manager.readers["city_ipv6"] = reader
	if _, _, err := manager.LookupCity(net.ParseIP("2001:4860::1")); err == nil || !strings.Contains(err.Error(), "lookup city_ipv6") {
		t.Fatalf("LookupCity() with MMDB error = %v", err)
	}
}
