package database

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tursodatabase/libsql-client-go/libsql"
)

type PoolOptions struct {
	MaxOpen     int
	MaxIdle     int
	MaxLifetime time.Duration
	MaxIdleTime time.Duration
}

type DatabaseOptions struct {
	Driver              string
	URL                 string
	Token               string
	DataDir             string
	Pool                PoolOptions
	Cache               ReplicaOptions
	SkipSchema          bool
	AllowOfflineStartup bool
}

// OpenConfigured opens the SQLite or remote libSQL database selected by the
// saved server configuration. It never falls back to a different database.
func OpenConfigured(options DatabaseOptions) (*sql.DB, error) {
	driverName := normalizeDatabaseDriver(options.Driver)
	if scheme := connectionScheme(options.URL); scheme == "libsql" || scheme == "https" || scheme == "http" {
		driverName = "libsql"
	} else if scheme == "file" {
		driverName = "sqlite"
	}
	if driverName == "" {
		driverName = "sqlite"
	}
	if driverName == "sqlite" {
		databasePath, err := sqlitePath(options.URL, options.DataDir)
		if err != nil {
			return nil, err
		}
		pool := normalizePoolOptions(options.Pool)
		return OpenSQLiteDatabaseWithPool(databasePath, pool)
	}
	if driverName != "libsql" {
		return nil, errors.New("server.database.url: unsupported database driver; use the URL form")
	}
	connector, err := libSQLConnector(options.URL, options.Token)
	if err != nil {
		return nil, err
	}
	var replica *ReadReplica
	if options.Cache.Enabled {
		cachePath := strings.ReplaceAll(options.Cache.Path, "{data_dir}", options.DataDir)
		if strings.TrimSpace(cachePath) == "" {
			cachePath = filepath.Join(options.DataDir, "db", "cache.db")
		}
		if !filepath.IsAbs(cachePath) {
			cachePath = filepath.Join(options.DataDir, cachePath)
		}
		var replicaErr error
		replica, replicaErr = OpenReadReplica(cachePath, options.Cache)
		if replicaErr != nil {
			return nil, fmt.Errorf("open SQLite database cache: %w", replicaErr)
		}
	}
	db := sql.OpenDB(instrumentedConnector{base: connector, rewrite: true, replica: replica})
	pool := normalizePoolOptions(options.Pool)
	db.SetMaxOpenConns(pool.MaxOpen)
	db.SetMaxIdleConns(pool.MaxIdle)
	db.SetConnMaxLifetime(pool.MaxLifetime)
	db.SetConnMaxIdleTime(pool.MaxIdleTime)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = db.PingContext(ctx)
	cancel()
	if err != nil {
		if replica != nil && options.AllowOfflineStartup && options.Cache.OfflineMode && replica.HasFreshCache() {
			replica.SetRemote(db)
			replica.SetReady()
			replica.mu.Lock()
			replica.openedOffline = true
			replica.mu.Unlock()
			replicaRegistry.Store(db, replica)
			return db, nil
		}
		_ = db.Close()
		if replica != nil {
			_ = replica.Close()
		}
		return nil, fmt.Errorf("database connection failed: %w", err)
	}
	if !options.SkipSchema {
		if err := EnsureSchema(context.Background(), db); err != nil {
			_ = db.Close()
			if replica != nil {
				_ = replica.Close()
			}
			return nil, err
		}
	}
	if replica != nil {
		replica.SetRemote(db)
		if err := replica.Synchronize(context.Background()); err != nil {
			// The remote database remains authoritative; a cache sync failure only
			// marks the replica unavailable and does not prevent online startup.
			replica.warn(err)
		}
		replica.SetReady()
		replicaRegistry.Store(db, replica)
		replica.Start()
	}
	return db, nil
}

// OpenedOffline reports whether OpenConfigured returned this remote handle
// using a previously synchronized local read cache.
func OpenedOffline(db *sql.DB) bool {
	value, ok := replicaRegistry.Load(db)
	if !ok {
		return false
	}
	replica := value.(*ReadReplica)
	replica.mu.RLock()
	defer replica.mu.RUnlock()
	return replica.openedOffline
}

func normalizeDatabaseDriver(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "sqlite", "sqlite2", "sqlite3":
		return "sqlite"
	case "libsql", "turso":
		return "libsql"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizePoolOptions(pool PoolOptions) PoolOptions {
	if pool.MaxOpen < 1 {
		pool.MaxOpen = 25
	}
	if pool.MaxIdle < 0 {
		pool.MaxIdle = 0
	} else if pool.MaxIdle == 0 {
		pool.MaxIdle = 5
	}
	if pool.MaxIdle > pool.MaxOpen {
		pool.MaxIdle = pool.MaxOpen
	}
	if pool.MaxLifetime <= 0 {
		pool.MaxLifetime = 5 * time.Minute
	}
	if pool.MaxIdleTime <= 0 {
		pool.MaxIdleTime = time.Minute
	}
	return pool
}

func sqlitePath(rawURL, dataDir string) (string, error) {
	value := strings.TrimSpace(rawURL)
	if value == "" || value == "{data_dir}/db/server.db" {
		if dataDir == "" {
			return "", errors.New("server.database.url: missing data directory")
		}
		return filepath.Join(dataDir, "db", "server.db"), nil
	}
	if strings.Contains(value, ";") || strings.HasPrefix(strings.ToLower(value), "jdbc:") {
		return "", errors.New("server.database.url: unsupported connection string; use the URL form")
	}
	if strings.HasPrefix(strings.ToLower(value), "file:") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "file" || parsed.Host != "" && parsed.Host != "localhost" || parsed.RawQuery != "" {
			return "", errors.New("server.database.url: unsupported SQLite URL; use a file path or file: URL")
		}
		value = parsed.Path
	}
	if strings.Contains(value, "://") {
		return "", errors.New("server.database.url: unsupported SQLite URL; use a file path or file: URL")
	}
	value = strings.ReplaceAll(value, "{data_dir}", dataDir)
	if !filepath.IsAbs(value) {
		absolute, err := filepath.Abs(value)
		if err != nil {
			return "", fmt.Errorf("resolve server.database.url: %w", err)
		}
		value = absolute
	}
	return value, nil
}

func libSQLConnector(rawURL, separateToken string) (driver.Connector, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("server.database.url: invalid connection string; use the URL form")
	}
	if parsed.Scheme != "libsql" && parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, errors.New("server.database.url: invalid connection scheme; use libsql:// or https://")
	}
	if parsed.Scheme == "http" {
		allowed, err := isPrivateDatabaseHost(parsed.Hostname())
		if err != nil || !allowed {
			return nil, errors.New("server.database.url: unencrypted HTTP is allowed only for a private host")
		}
	}
	query := parsed.Query()
	urlToken := query.Get("authToken")
	if urlToken == "" {
		urlToken = query.Get("auth_token")
	}
	if query.Get("jwt") != "" {
		return nil, errors.New("server.database.url: unsupported credential option; use authToken or server.database.token")
	}
	query.Del("authToken")
	query.Del("auth_token")
	query.Del("tls")
	parsed.RawQuery = query.Encode()
	token := urlToken
	if token == "" {
		token = separateToken
	}
	options := []libsql.Option{libsql.WithTls(parsed.Scheme != "http")}
	if token != "" {
		options = append(options, libsql.WithAuthToken(token))
	}
	connector, err := libsql.NewConnector(parsed.String(), options...)
	if err != nil {
		return nil, errors.New("server.database.url: invalid connection string; use the URL form")
	}
	return connector, nil
}

func isPrivateDatabaseHost(host string) (bool, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		return address.IsPrivate() || address.IsLoopback(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return false, err
	}
	for _, address := range addresses {
		parsed, ok := netip.AddrFromSlice(address.IP)
		if !ok || (!parsed.IsPrivate() && !parsed.IsLoopback()) {
			return false, nil
		}
	}
	return true, nil
}

type CacheOptions struct {
	Type          string
	URL           string
	Host          string
	Port          int
	Username      string
	Password      string
	Database      int
	TLS           bool
	TLSSkipVerify bool
	TLSCA         string
	TLSCert       string
	TLSKey        string
	ConfigDir     string
	PoolSize      int
	MinIdle       int
	Timeout       time.Duration
}

// OpenCache creates the configured Valkey/Redis client. A configured but
// unreachable cache is returned alongside its ping error so callers can keep
// the client for recovery and use the documented database fallback meanwhile.
func OpenCache(options CacheOptions) (redis.UniversalClient, error) {
	typeName := strings.ToLower(strings.TrimSpace(options.Type))
	switch connectionScheme(options.URL) {
	case "redis", "rediss":
		typeName = "redis"
	case "valkey", "valkeys":
		typeName = "valkey"
	}
	switch connectionScheme(options.URL) {
	case "redis", "rediss":
		typeName = "redis"
	case "valkey", "valkeys":
		typeName = "valkey"
	}
	if typeName == "" || typeName == "memory" || typeName == "none" {
		return nil, nil
	}
	if typeName != "redis" && typeName != "valkey" {
		return nil, errors.New("server.cache.type: unsupported cache type")
	}
	clientOptions, requestedTLS, err := cacheClientOptions(options)
	if err != nil {
		return nil, err
	}
	if requestedTLS {
		tlsSettings, err := cacheTLSConfig(options, clientOptions.Addr)
		if err != nil {
			return nil, err
		}
		clientOptions.TLSConfig = tlsSettings
	}
	if options.PoolSize > 0 {
		clientOptions.PoolSize = options.PoolSize
	}
	if options.MinIdle > 0 {
		clientOptions.MinIdleConns = options.MinIdle
	}
	if options.Timeout > 0 {
		clientOptions.DialTimeout = options.Timeout
		clientOptions.ReadTimeout = options.Timeout
		clientOptions.WriteTimeout = options.Timeout
	}
	client := redis.NewClient(clientOptions)
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	err = client.Ping(ctx).Err()
	cancel()
	if err != nil {
		return client, fmt.Errorf("cache connection failed")
	}
	return client, nil
}

func connectionScheme(value string) string {
	value = strings.TrimSpace(value)
	separator := strings.Index(value, "://")
	if separator < 1 {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "file:") {
			return "file"
		}
		return ""
	}
	return strings.ToLower(value[:separator])
}

func cacheClientOptions(options CacheOptions) (*redis.Options, bool, error) {
	if strings.TrimSpace(options.URL) == "" {
		if strings.TrimSpace(options.Host) == "" || options.Port < 1 || options.Port > 65535 {
			return nil, false, errors.New("server.cache: invalid host or port")
		}
		tlsEnabled := options.TLS
		return &redis.Options{Addr: net.JoinHostPort(options.Host, strconv.Itoa(options.Port)), Username: options.Username, Password: options.Password, DB: options.Database}, tlsEnabled, nil
	}
	urlValue := strings.TrimSpace(options.URL)
	separator := strings.Index(urlValue, "://")
	if separator < 1 {
		return nil, false, errors.New("server.cache.url: invalid connection string; use the URL form")
	}
	scheme := strings.ToLower(urlValue[:separator])
	switch scheme {
	case "valkey":
		scheme = "redis"
	case "valkeys":
		scheme = "rediss"
	case "redis", "rediss":
	default:
		return nil, false, errors.New("server.cache.url: invalid connection scheme; use redis://, rediss://, valkey:// or valkeys://")
	}
	remainder := urlValue[separator+3:]
	userinfo := ""
	if at := strings.LastIndexByte(remainder, '@'); at >= 0 {
		userinfo = remainder[:at]
		remainder = remainder[at+1:]
	}
	parsed, err := url.Parse(scheme + "://" + remainder)
	if err != nil || parsed.Hostname() == "" || parsed.Fragment != "" {
		return nil, false, errors.New("server.cache.url: invalid connection string; use the URL form")
	}
	clientOptions := &redis.Options{Addr: parsed.Host, DB: options.Database, Username: options.Username, Password: options.Password}
	if parsed.Path != "" && parsed.Path != "/" {
		database, err := strconv.Atoi(strings.TrimPrefix(parsed.Path, "/"))
		if err != nil || database < 0 {
			return nil, false, errors.New("server.cache.url: invalid database number")
		}
		clientOptions.DB = database
	}
	if userinfo != "" {
		username, password, hasPassword := strings.Cut(userinfo, ":")
		if hasPassword {
			clientOptions.Username = decodeURLUserInfo(username)
			clientOptions.Password = decodeURLUserInfo(password)
		} else {
			clientOptions.Username = ""
			clientOptions.Password = decodeURLUserInfo(username)
		}
	}
	query := parsed.Query()
	tlsEnabled := scheme == "rediss" || options.TLS
	if tlsValue := query.Get("tls"); tlsValue != "" {
		parsedTLS, ok := parseConnectionBool(tlsValue)
		if !ok {
			return nil, false, errors.New("server.cache.url: invalid tls option")
		}
		tlsEnabled = parsedTLS
	}
	if db := query.Get("db"); db != "" {
		parsedDB, err := strconv.Atoi(db)
		if err != nil || parsedDB < 0 {
			return nil, false, errors.New("server.cache.url: invalid database number")
		}
		clientOptions.DB = parsedDB
	}
	return clientOptions, tlsEnabled, nil
}

func decodeURLUserInfo(value string) string {
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			continue
		}
		if index+2 >= len(value) || !isHex(value[index+1]) || !isHex(value[index+2]) {
			return value
		}
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func parseConnectionBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enabled":
		return true, true
	case "0", "false", "no", "off", "disabled":
		return false, true
	default:
		return false, false
	}
}

func cacheTLSConfig(options CacheOptions, address string) (*tls.Config, error) {
	serverName, _, err := net.SplitHostPort(address)
	if err != nil {
		serverName = address
	}
	settings := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName, InsecureSkipVerify: options.TLSSkipVerify}
	if options.TLSCA != "" {
		path := resolveCacheTLSPath(options.ConfigDir, options.TLSCA)
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read server.cache.tls_ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(content) {
			return nil, errors.New("server.cache.tls_ca: invalid CA certificate")
		}
		settings.RootCAs = pool
		settings.InsecureSkipVerify = false
	}
	if options.TLSCert != "" || options.TLSKey != "" {
		if options.TLSCert == "" || options.TLSKey == "" {
			return nil, errors.New("server.cache.tls_cert and server.cache.tls_key must be set together")
		}
		cert, err := tls.LoadX509KeyPair(resolveCacheTLSPath(options.ConfigDir, options.TLSCert), resolveCacheTLSPath(options.ConfigDir, options.TLSKey))
		if err != nil {
			return nil, errors.New("server.cache TLS client certificate could not be loaded")
		}
		settings.Certificates = []tls.Certificate{cert}
	}
	return settings, nil
}

func resolveCacheTLSPath(configDir, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(configDir, "ssl", value)
}
