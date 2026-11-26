package database

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeDatabaseAndPoolOptions(t *testing.T) {
	for input, want := range map[string]string{"": "sqlite", " SQLite3 ": "sqlite", "TURSO": "libsql", "Custom": "custom"} {
		if got := normalizeDatabaseDriver(input); got != want {
			t.Errorf("normalizeDatabaseDriver(%q) = %q, want %q", input, got, want)
		}
	}
	pool := normalizePoolOptions(PoolOptions{MaxOpen: -1, MaxIdle: 100, MaxLifetime: -time.Second, MaxIdleTime: 0})
	if pool.MaxOpen != 25 || pool.MaxIdle != 25 || pool.MaxLifetime != 5*time.Minute || pool.MaxIdleTime != time.Minute {
		t.Fatalf("normalized pool = %+v", pool)
	}
	pool = normalizePoolOptions(PoolOptions{MaxOpen: 8, MaxIdle: -1, MaxLifetime: time.Second, MaxIdleTime: time.Second})
	if pool.MaxOpen != 8 || pool.MaxIdle != 0 {
		t.Fatalf("normalized explicit pool = %+v", pool)
	}
}

func TestSQLitePathAndConfiguredOpen(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		url  string
		want string
	}{
		{url: "", want: filepath.Join(root, "db", "server.db")},
		{url: "{data_dir}/custom.db", want: filepath.Join(root, "custom.db")},
		{url: "file:" + filepath.Join(root, "remote.db"), want: filepath.Join(root, "remote.db")},
		{url: "file://localhost" + filepath.Join(root, "local.db"), want: filepath.Join(root, "local.db")},
	} {
		got, err := sqlitePath(test.url, root)
		if err != nil || got != test.want {
			t.Errorf("sqlitePath(%q) = (%q, %v), want %q", test.url, got, err, test.want)
		}
	}
	for _, invalid := range []string{"jdbc:sqlite:test.db", "sqlite://host/db", "file://remote/path", "file:/tmp/db?mode=ro", "bad;mode=ro"} {
		if _, err := sqlitePath(invalid, root); err == nil {
			t.Errorf("sqlitePath(%q) accepted unsupported URL", invalid)
		}
	}
	if _, err := sqlitePath("", ""); err == nil {
		t.Fatal("sqlitePath() accepted a missing data directory")
	}
	db, err := OpenConfigured(DatabaseOptions{Driver: "sqlite", URL: "{data_dir}/server.db", DataDir: root, SkipSchema: true})
	if err != nil {
		t.Fatalf("OpenConfigured(SQLite): %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenConfigured(DatabaseOptions{Driver: "mysql", URL: "mysql://localhost/db", DataDir: root}); err == nil {
		t.Fatal("OpenConfigured() accepted an unsupported driver")
	}
	if _, err := OpenConfigured(DatabaseOptions{URL: "http://8.8.8.8/db", DataDir: root}); err == nil {
		t.Fatal("OpenConfigured() accepted unencrypted public HTTP")
	}
}

func TestLibSQLConnectorValidation(t *testing.T) {
	for _, invalid := range []string{"", "libsql://", "libsql://user@db.example", "libsql://db.example#fragment", "libsql://db.example?jwt=legacy", "http://8.8.8.8/db"} {
		if _, err := libSQLConnector(invalid, ""); err == nil {
			t.Errorf("libSQLConnector(%q) accepted invalid URL", invalid)
		}
	}
	if _, err := libSQLConnector("libsql://db.example", "token"); err != nil {
		t.Fatalf("libSQLConnector(valid remote): %v", err)
	}
	if _, err := libSQLConnector("http://127.0.0.1:8080/db", ""); err != nil {
		t.Fatalf("libSQLConnector(private HTTP): %v", err)
	}
}

func TestCacheConfigurationParsing(t *testing.T) {
	if scheme := connectionScheme(" ValKeys://cache.example"); scheme != "valkeys" {
		t.Errorf("connectionScheme() = %q", scheme)
	}
	if scheme := connectionScheme("file:./cache.db"); scheme != "file" {
		t.Errorf("connectionScheme(file) = %q", scheme)
	}
	if _, err := OpenCache(CacheOptions{Type: "memory"}); err != nil {
		t.Fatalf("OpenCache(memory): %v", err)
	}
	if _, err := OpenCache(CacheOptions{Type: "unsupported"}); err == nil {
		t.Fatal("OpenCache() accepted unsupported cache type")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddress := listener.Addr().(*net.TCPAddr)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	client, err := OpenCache(CacheOptions{Type: "redis", Host: "127.0.0.1", Port: closedAddress.Port, Timeout: 100 * time.Millisecond})
	if err == nil || client == nil {
		t.Fatalf("OpenCache(unreachable) = (%v, %v), want recoverable client and ping error", client, err)
	}
	if closeErr := client.Close(); closeErr != nil {
		t.Errorf("close unreachable cache client: %v", closeErr)
	}
	if _, _, err := cacheClientOptions(CacheOptions{Host: "cache.example", Port: 6379}); err != nil {
		t.Fatalf("cacheClientOptions(host): %v", err)
	}
	for _, options := range []CacheOptions{
		{},
		{Host: "cache.example", Port: 70000},
		{URL: "cache.example:6379"},
		{URL: "memcached://cache.example"},
		{URL: "redis://cache.example/not-a-number"},
		{URL: "redis://cache.example/0?db=-1"},
		{URL: "redis://cache.example?tls=maybe"},
		{URL: "redis://cache.example#fragment"},
	} {
		if _, _, err := cacheClientOptions(options); err == nil {
			t.Errorf("cacheClientOptions(%+v) accepted invalid configuration", options)
		}
	}
	options, tlsEnabled, err := cacheClientOptions(CacheOptions{URL: "valkeys://user:p%40ss@cache.example:6380/2?db=4&tls=no"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Addr != "cache.example:6380" || options.Username != "user" || options.Password != "p@ss" || options.DB != 4 || tlsEnabled {
		t.Fatalf("parsed cache options = (%+v, tls=%t)", options, tlsEnabled)
	}
	options, tlsEnabled, err = cacheClientOptions(CacheOptions{URL: "rediss://cache.example/1"})
	if err != nil || options.DB != 1 || !tlsEnabled {
		t.Fatalf("parsed TLS cache options = (%+v, tls=%t, err=%v)", options, tlsEnabled, err)
	}
}

func TestCacheTLSAndAddressValidation(t *testing.T) {
	config, err := cacheTLSConfig(CacheOptions{TLSSkipVerify: true}, net.JoinHostPort("cache.example", "6379"))
	if err != nil || config.ServerName != "cache.example" || !config.InsecureSkipVerify || config.MinVersion == 0 {
		t.Fatalf("cacheTLSConfig() = (%+v, %v)", config, err)
	}
	if _, err := cacheTLSConfig(CacheOptions{TLSCert: "client.crt"}, "cache.example:6379"); err == nil {
		t.Fatal("cacheTLSConfig() accepted an incomplete client certificate pair")
	}
	if _, err := cacheTLSConfig(CacheOptions{TLSCA: "missing.pem", ConfigDir: t.TempDir()}, "cache.example:6379"); err == nil {
		t.Fatal("cacheTLSConfig() accepted a missing CA file")
	}
	if _, err := cacheTLSConfig(CacheOptions{TLSCA: "bad.pem", ConfigDir: t.TempDir()}, "cache.example:6379"); err == nil {
		t.Fatal("cacheTLSConfig() accepted invalid CA data")
	}
	if got := resolveCacheTLSPath("/etc/countries", "client.pem"); got != filepath.Join("/etc/countries", "ssl", "client.pem") {
		t.Errorf("relative TLS path = %q", got)
	}
	absolute := filepath.Join(t.TempDir(), "ca.pem")
	if got := resolveCacheTLSPath("/etc/countries", absolute); got != absolute {
		t.Errorf("absolute TLS path changed to %q", got)
	}
	for _, host := range []string{"127.0.0.1", "10.0.0.5", "8.8.8.8"} {
		allowed, err := isPrivateDatabaseHost(host)
		want := host != "8.8.8.8"
		if err != nil || allowed != want {
			t.Errorf("isPrivateDatabaseHost(%q) = (%t, %v), want %t", host, allowed, err, want)
		}
	}
}

func TestConnectionValueHelpers(t *testing.T) {
	for _, test := range []struct {
		input string
		want  bool
		valid bool
	}{
		{input: " YES ", want: true, valid: true},
		{input: "disabled", want: false, valid: true},
		{input: "maybe", valid: false},
	} {
		got, valid := parseConnectionBool(test.input)
		if got != test.want || valid != test.valid {
			t.Errorf("parseConnectionBool(%q) = (%t, %t)", test.input, got, valid)
		}
	}
	for _, test := range []struct{ input, want string }{
		{input: "p%40ss", want: "p@ss"},
		{input: "plain", want: "plain"},
		{input: "broken%XZ", want: "broken%XZ"},
	} {
		if got := decodeURLUserInfo(test.input); got != test.want {
			t.Errorf("decodeURLUserInfo(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	if !isHex('a') || !isHex('F') || !isHex('4') || isHex('x') {
		t.Fatal("isHex() returned incorrect results")
	}
}
