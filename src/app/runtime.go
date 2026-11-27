package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/apimgr/countries/src/auditlog"
	"github.com/apimgr/countries/src/backup"
	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/common/logbuffer"
	"github.com/apimgr/countries/src/common/network"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
	"github.com/apimgr/countries/src/data"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/geoip"
	appLogging "github.com/apimgr/countries/src/logging"
	appmode "github.com/apimgr/countries/src/mode"
	"github.com/apimgr/countries/src/notifications"
	"github.com/apimgr/countries/src/overlay"
	"github.com/apimgr/countries/src/paths"
	"github.com/apimgr/countries/src/pidfile"
	"github.com/apimgr/countries/src/privilege"
	backgroundscheduler "github.com/apimgr/countries/src/scheduler"
	"github.com/apimgr/countries/src/securitydata"
	"github.com/apimgr/countries/src/server"
	"github.com/apimgr/countries/src/server/banner"
	"github.com/apimgr/countries/src/service"
	"github.com/apimgr/countries/src/ssl"
	"github.com/apimgr/countries/src/updater"
	"github.com/shirou/gopsutil/v3/disk"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

var unresolvedFQDNWarning sync.Once
var torAddressLogged sync.Once
var errUpdateStaged = errors.New("update downloaded and staged; restart with elevated privileges to apply it")
var auditMirrorDB *sql.DB

var (
	Version      = "devel"
	CommitID     = "N/A"
	BuildDate    = "N/A"
	BuildEpoch   = "0"
	OfficialSite = ""
)

func buildEpoch() int64 {
	n, err := strconv.ParseInt(BuildEpoch, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func init() {
	if epoch := buildEpoch(); epoch > 0 {
		BuildDate = time.Unix(epoch, 0).UTC().Format("2006-01-02T15:04:05Z")
	}
}

const projectName = "countries"

func selectServerLanguage(flagValue, configValue string) string {
	for _, value := range []string{flagValue, configValue} {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || value == "auto" {
			continue
		}
		if i18n.IsSupported(value) {
			return value
		}
		return "en"
	}
	for _, name := range []string{"LC_ALL", "LANG"} {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
		value = strings.SplitN(strings.SplitN(value, ".", 2)[0], "_", 2)[0]
		if value == "" {
			continue
		}
		if i18n.IsSupported(value) {
			return value
		}
		return "en"
	}
	return "en"
}

func localDatabasePath(cfg *config.Config, dataDir string) string {
	if isRemoteDatabase(cfg.Server.Database.Driver, cfg.Server.Database.URL) {
		return filepath.Join(paths.GetDatabaseDir(dataDir), "server.db")
	}
	databasePath := strings.ReplaceAll(cfg.Server.Database.URL, "{data_dir}", dataDir)
	if cfg.Server.Database.URL == "" || cfg.Server.Database.URL == "{data_dir}/db/server.db" {
		return filepath.Join(paths.GetDatabaseDir(dataDir), "server.db")
	}
	return databasePath
}

func isRemoteDatabase(driver, connectionURL string) bool {
	driver = strings.ToLower(strings.TrimSpace(driver))
	urlScheme := strings.ToLower(strings.SplitN(connectionURL, "://", 2)[0])
	return driver == "libsql" || driver == "turso" || urlScheme == "libsql" || urlScheme == "https" || urlScheme == "http"
}

var updaterReleaseURL = "https://api.github.com/repos/apimgr/countries/releases?per_page=100"

func runWithContext(serviceContext context.Context, startupConfig config.Config) int {
	if !flagsReady {
		ParseFlags(os.Args[1:])
	}
	flagsReady = false
	if preScanCommandHelp(os.Args[1:]) {
		return 0
	}
	logbuffer.Install()
	log.SetFlags(0)
	var logManager *appLogging.Manager
	defer func() {
		if logManager != nil {
			_ = logManager.Close()
		}
		log.SetOutput(logbuffer.Writer{Buffer: logbuffer.Default(), Output: os.Stderr})
	}()
	setLogOutput := func(manager *appLogging.Manager) {
		log.SetFlags(0)
		log.SetOutput(logbuffer.Writer{Buffer: logbuffer.Default(), Output: io.MultiWriter(os.Stderr, manager)})
	}
	startedElevated := privilege.IsElevated()
	configDir, dataDir, logsDir := paths.GetDefaultDirs(projectName)
	cacheDir := paths.GetCacheDir(projectName)
	backupDirResolved := paths.GetBackupDir(projectName)

	switch strings.ToLower(*color) {
	case "auto":
	case "yes":
		if err := os.Unsetenv("NO_COLOR"); err != nil {
			fmt.Fprintf(os.Stderr, "failed to enable color output: %v\n", err)
			os.Exit(1)
		}
	case "no":
		if err := os.Setenv("NO_COLOR", "1"); err != nil {
			fmt.Fprintf(os.Stderr, "failed to disable color output: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "invalid --color value %q; choose auto, yes, or no\n", *color)
		os.Exit(64)
	}
	if *debug {
		if err := os.Setenv("DEBUG", "1"); err != nil {
			fmt.Fprintf(os.Stderr, "failed to enable debug logging: %v\n", err)
			os.Exit(1)
		}
	}

	if *help {
		printHelp()
		os.Exit(0)
	}

	if *version {
		printVersion()
		os.Exit(0)
	}
	if *shellCmd != "" {
		args := flag.Args()
		shell := ""
		if len(args) > 0 {
			shell = args[0]
		}
		if err := runServerShell(*shellCmd, shell); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 64
		}
		return 0
	}

	if *configDirFlag != "" {
		configDir = *configDirFlag
	}
	if *dataDirFlag != "" {
		dataDir = *dataDirFlag
	}
	if *cacheDirFlag != "" {
		cacheDir = *cacheDirFlag
	}
	if *logsDirFlag != "" {
		logsDir = *logsDirFlag
	}
	if *backupDirFlag != "" {
		backupDirResolved = *backupDirFlag
	}

	if envConfig := os.Getenv("CONFIG_DIR"); envConfig != "" && *configDirFlag == "" {
		configDir = envConfig
	}
	if envData := os.Getenv("DATA_DIR"); envData != "" && *dataDirFlag == "" {
		dataDir = envData
	}
	if envCache := os.Getenv("CACHE_DIR"); envCache != "" && *cacheDirFlag == "" {
		cacheDir = envCache
	}
	envLogs := os.Getenv("LOG_DIR")
	if envLogs == "" {
		envLogs = os.Getenv("LOGS_DIR")
	}
	if envLogs != "" && *logsDirFlag == "" {
		logsDir = envLogs
	}
	if envBackup := os.Getenv("BACKUP_DIR"); envBackup != "" && *backupDirFlag == "" {
		backupDirResolved = envBackup
	}
	for _, directory := range []*string{&configDir, &dataDir, &cacheDir, &logsDir, &backupDirResolved} {
		safe, err := paths.SafeDirPath(*directory)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid directory path: %v\n", err)
			return 64
		}
		*directory = safe
	}
	configPath := filepath.Join(configDir, "server.yml")
	if *serviceCmd == "--install" && startedElevated && runtime.GOOS != "windows" {
		databaseDir := paths.GetDatabaseDir(dataDir)
		if err := paths.EnsureDirs(configDir, dataDir, logsDir, cacheDir, backupDirResolved, databaseDir, filepath.Join(configDir, "yaml.d"), filepath.Join(configDir, "ssl")); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to prepare service directories: %v\n", err)
			os.Exit(1)
		}
		identity, err := ensureServiceAccount(projectName, dataDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to prepare service account: %v\n", err)
			os.Exit(1)
		}
		if identity.Created || identity.GroupCreated {
			if err := service.RecordServiceAccount(identity, projectName); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to record service account: %v\n", err)
				os.Exit(1)
			}
		}
		if err := chownServicePaths(identity, configDir, dataDir, logsDir, cacheDir, backupDirResolved, databaseDir); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to secure service directories: %v\n", err)
			os.Exit(1)
		}
	}
	if *serviceCmd != "" {
		if maybeEscalateSystemService(*serviceCmd, startedElevated) {
			return 0
		}
		handleServiceCommand(*serviceCmd, configDir, dataDir, cacheDir, logsDir, backupDirResolved)
		return 0
	}
	if *maintenanceCmd != "" {
		handleMaintenanceCommand(*maintenanceCmd, configDir, dataDir, logsDir, configPath)
		return 0
	}
	if args := flag.Args(); len(args) > 0 && args[0] == "email" {
		cfg, loadErr := config.Load(configPath)
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", loadErr)
			os.Exit(1)
		}
		if err := runEmailCommand(args[1:], cfg, configPath, configDir); err != nil {
			fmt.Fprintf(os.Stderr, "Email command failed: %v\n", err)
			os.Exit(1)
		}
		return 0
	}
	if updateCmd.set {
		cfg, loadErr := config.Load(configPath)
		if loadErr != nil {
			log.Fatalf("Failed to load configuration: %v", loadErr)
		}
		if err := appmode.Initialize(resolveRuntimeMode(*modeFlag, os.Getenv("MODE"), cfg.Server.Mode)); err != nil {
			fmt.Fprintf(os.Stderr, "Invalid application mode: %v\n", err)
			os.Exit(78)
		}
		command, args := resolveUpdateInvocation(updateCmd.value, flag.Args())
		if err := handleUpdateCommandWithArgsElevation(command, cfg, args, startedElevated, dataDir, configPath); err != nil {
			log.Printf("Update command failed: %v", err)
			if errors.Is(err, errUpdateStaged) {
				return 75
			}
			os.Exit(1)
		}
		return 0
	}
	if *status {
		settings := config.PeekStartupSettings(configPath)
		checkPort := resolveServerPort(*port, os.Getenv("PORT"), settings.Port)
		var serviceErr error
		if runtime.GOOS == "windows" {
			serviceErr = service.WindowsServiceStatus()
		}
		healthErr := checkHealth(checkPort)
		if serviceErr != nil || healthErr != nil {
			if serviceErr != nil {
				fmt.Fprintf(os.Stderr, "Windows service status failed: %v\n", serviceErr)
			}
			if healthErr != nil {
				fmt.Fprintf(os.Stderr, "Health check failed: %v\n", healthErr)
			}
			os.Exit(1)
		}
		fmt.Println("OK")
		return 0
	}
	var startupUpdateErr error
	if startedElevated && runtime.GOOS != "windows" {
		if executable, executableErr := os.Executable(); executableErr != nil {
			startupUpdateErr = fmt.Errorf("resolve executable for staged update: %w", executableErr)
		} else if stagedVersion, stagedErr := updater.StagedVersion(dataDir); stagedErr == nil {
			checksumCtx, checksumCancel := context.WithTimeout(context.Background(), 15*time.Second)
			checksum, checksumErr := updater.ReleaseChecksum(checksumCtx, nil, updaterReleaseURL, stagedVersion, updater.BinaryName(projectName, ""))
			checksumCancel()
			if checksumErr != nil {
				startupUpdateErr = updater.DiscardStaged(dataDir, fmt.Errorf("verify staged update against official release: %w", checksumErr))
			} else if applied, applyErr := updater.ApplyStagedVerified(dataDir, executable, Version, buildEpoch(), checksum); applyErr != nil {
				startupUpdateErr = applyErr
			} else if applied {
				if restartErr := updater.RestartSelf(); restartErr != nil {
					startupUpdateErr = fmt.Errorf("restart after applying staged update: %w", restartErr)
				}
			}
		} else if !errors.Is(stagedErr, os.ErrNotExist) {
			startupUpdateErr = updater.DiscardStaged(dataDir, stagedErr)
		}
	}

	var serviceIdentity *privilege.Identity
	if startedElevated && runtime.GOOS != "windows" {
		identity, accountErr := ensureServiceAccount(projectName, dataDir)
		if accountErr != nil {
			log.Fatalf("Failed to prepare service account: %v", accountErr)
		}
		serviceIdentity = &identity
	}
	databaseDir := paths.GetDatabaseDir(dataDir)
	yamlDirectory := filepath.Join(configDir, "yaml.d")
	sslDirectory := filepath.Join(configDir, "ssl")
	if err := service.EnforcePrivateACL(startedElevated || service.IsWindowsService(), configDir, dataDir); err != nil {
		log.Fatalf("Failed to secure private configuration and data paths: %v", err)
	}
	if err := paths.EnsureDirs(configDir, dataDir, logsDir, cacheDir, backupDirResolved, databaseDir, yamlDirectory, sslDirectory); err != nil {
		log.Fatalf("Failed to create directories: %v", err)
	}
	defaultLogging := config.DefaultConfig().Server.Logging
	if manager, err := appLogging.Open(logsDir, defaultLogging, "production"); err != nil {
		log.Printf("WARN: could not initialize application log files: %v", err)
	} else {
		logManager = manager
		setLogOutput(logManager)
	}
	if startedElevated && paths.IsContainer() && filepath.Clean(configDir) == filepath.Join("/config", projectName) {
		configParent := filepath.Dir(configDir)
		if err := os.Chown(configParent, 0, 0); err != nil {
			log.Fatalf("Failed to secure container configuration directory: %v", err)
		}
		if err := os.Chmod(configParent, 0755); err != nil {
			log.Fatalf("Failed to set container configuration directory permissions: %v", err)
		}
	}
	if serviceIdentity != nil && (serviceIdentity.Created || serviceIdentity.GroupCreated) {
		if err := service.RecordServiceAccount(*serviceIdentity, projectName); err != nil {
			log.Fatalf("Failed to record service account ownership: %v", err)
		}
	}
	if err := paths.EnsurePrivateTree(sslDirectory); err != nil {
		log.Fatalf("Failed to secure SSL directory: %v", err)
	}
	if serviceIdentity != nil {
		if err := chownServicePaths(*serviceIdentity, configDir, dataDir, logsDir, cacheDir, backupDirResolved, databaseDir); err != nil {
			log.Fatalf("Failed to set service directory ownership: %v", err)
		}
	}
	if err := config.PrepareStartupConfig(configPath); err != nil {
		log.Fatalf("Failed to prepare startup configuration permissions: %v", err)
	}
	startupSettings := config.PeekStartupSettings(configPath)
	pidFilePath := *pidFileFlag
	if pidFilePath == "" {
		pidFilePath = paths.GetPIDFilePath(projectName, dataDir, startedElevated)
	}
	if startupSettings.PIDFile && !paths.IsContainer() && runtime.GOOS != "windows" && pidFilePath != "" && *pidFileFlag == "" {
		if err := os.MkdirAll(filepath.Dir(pidFilePath), 0755); err != nil {
			log.Fatalf("Failed to create PID directory: %v", err)
		}
		if startedElevated {
			if err := os.Chmod(filepath.Dir(pidFilePath), 0755); err != nil {
				log.Fatalf("Failed to set PID directory permissions: %v", err)
			}
		}
		if serviceIdentity != nil {
			if err := os.Chown(filepath.Dir(pidFilePath), serviceIdentity.UID, serviceIdentity.GID); err != nil {
				log.Fatalf("Failed to set PID directory ownership: %v", err)
			}
		}
	}
	generatedInitialPort := strings.TrimSpace(*port) == "" && strings.TrimSpace(os.Getenv("PORT")) == "" && strings.TrimSpace(startupSettings.Port) == ""
	if generatedInitialPort {
		initialPort, portErr := config.SelectInitialServerPort(os.Getenv("PORT"))
		if portErr != nil {
			log.Fatalf("Failed to select initial server port: %v", portErr)
		}
		startupSettings.Port = initialPort
	}
	serverPort := resolveServerPort(*port, os.Getenv("PORT"), startupSettings.Port)
	_, _, portErr := resolveServerListeners(serverPort, false)
	invalidConfiguredPort := portErr != nil && strings.TrimSpace(*port) == "" && strings.TrimSpace(os.Getenv("PORT")) == ""
	if portErr != nil {
		fallbackPort, fallbackErr := config.SelectInitialServerPort("")
		if fallbackErr != nil {
			log.Fatalf("Failed to select a replacement server port: %v", fallbackErr)
		}
		log.Printf("WARN: invalid server port %q; using %s", serverPort, fallbackPort)
		serverPort = fallbackPort
	}
	serverAddress := resolveServerAddress(*address, os.Getenv("LISTEN"), startupSettings.Address)
	serverAddress, addressFallback, addressErr := fallbackIPv4ListenerAddress(serverAddress)
	if addressErr != nil {
		log.Fatalf("Failed to check dual-stack listener support: %v", addressErr)
	}
	if addressFallback {
		log.Printf("IPv6 is unavailable; falling back to IPv4 listener 0.0.0.0")
	}
	if parent, daemonErr := startDaemonIfRequested(*daemon, startupSettings.Daemonize, false); daemonErr != nil {
		log.Fatalf("Failed to daemonize: %v", daemonErr)
	} else if parent {
		return 0
	}

	httpPort, httpsPort, listenerErr := resolveServerListeners(serverPort, false)
	if listenerErr != nil {
		log.Fatalf("Invalid server port configuration: %v", listenerErr)
	}
	httpAddr, httpsAddr := listenerAddress(serverAddress, httpPort), listenerAddress(serverAddress, httpsPort)
	listenerAddresses := []string{httpAddr, httpsAddr}
	if serviceIdentity == nil {
		if err := rejectUnprivilegedPortUse(listenerAddresses...); err != nil {
			log.Fatal(err)
		}
	}

	var err error
	var preboundListeners map[string]net.Listener
	if serviceIdentity != nil {
		preboundListeners, err = bindPrivilegedListeners(listenerAddresses...)
		if err != nil {
			log.Fatalf("Failed to bind privileged listeners: %v", err)
		}
		if err := dropServicePrivileges(*serviceIdentity); err != nil {
			closeListeners(preboundListeners)
			log.Fatalf("Failed to drop service privileges: %v", err)
		}
	} else if err := rejectUnprivilegedPortUse(listenerAddresses...); err != nil {
		log.Fatal(err)
	}
	if startupSettings.PIDFile && !paths.IsContainer() && runtime.GOOS != "windows" {
		pidMode := os.FileMode(0600)
		if startedElevated {
			pidMode = 0644
		}
		if err := pidfile.Acquire(pidFilePath, pidMode); err != nil {
			log.Fatalf("Failed to acquire server PID file: %v", err)
		}
		defer func() {
			if err := pidfile.Remove(pidFilePath); err != nil {
				log.Printf("Could not remove server PID file: %v", err)
			}
		}()
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	cfg.Listener = startupConfig.Listener
	// Values supplied by the process entry are the startup merge base; the
	// persisted configuration remains authoritative when it provides a value.
	if cfg.Lang == "" {
		cfg.Lang = startupConfig.Lang
	}
	if cfg.Server.Port == "" {
		cfg.Server.Port = startupConfig.Server.Port
	}
	if cfg.Server.Address == "" {
		cfg.Server.Address = startupConfig.Server.Address
	}
	if *baseURL != "" {
		cfg.Server.BaseURL = *baseURL
	}
	cfg.Lang = selectServerLanguage(*langFlag, cfg.Lang)
	if (generatedInitialPort && cfg.Server.Port != serverPort) || invalidConfiguredPort {
		cfg.Server.Port = serverPort
		if err := config.SaveNodeConfiguration(configPath, cfg); err != nil {
			log.Fatalf("Failed to save selected server port: %v", err)
		}
	}
	if envValue, exists := os.LookupEnv("I2P_ENABLED"); exists {
		cfg.Server.I2P.Enabled = config.ParseBoolOrDefault("I2P_ENABLED", envValue, cfg.Server.I2P.Enabled)
	}
	if i2pFlagSet {
		cfg.Server.I2P.Enabled = *i2pFlag
	}
	if err := appmode.Initialize(resolveRuntimeMode(*modeFlag, os.Getenv("MODE"), cfg.Server.Mode)); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid application mode: %v\n", err)
		os.Exit(78)
	}
	if manager, err := appLogging.Open(logsDir, cfg.Server.Logging, string(appmode.Get())); err != nil {
		log.Printf("WARN: could not apply configured log files; keeping defaults: %v", err)
	} else {
		if logManager != nil {
			_ = logManager.Close()
		}
		logManager = manager
		setLogOutput(logManager)
	}
	auditConfig := cfg.Server.Logging.Audit
	auditOptions := auditlog.Options{
		Enabled: auditConfig.Enabled, SystemMode: startedElevated || service.IsWindowsService(), Filename: auditConfig.Filename, Format: auditConfig.Format,
		Rotate: auditConfig.Rotate, Keep: auditConfig.Keep, Compress: auditConfig.Compress,
		IncludeUserAgent: auditConfig.IncludeUserAgent,
		Events: auditlog.EventCategories{
			Configuration: auditConfig.Events.Configuration, Security: auditConfig.Events.Security,
			Backup: auditConfig.Events.Backup, Server: auditConfig.Events.Server,
			TokenUsage: auditConfig.TokenUsage,
		},
	}
	if err := auditlog.Configure(logsDir, auditOptions); err != nil {
		log.Printf("WARN: invalid audit logging configuration; using defaults: %v", err)
		fallbackOptions := auditlog.DefaultOptions()
		fallbackOptions.SystemMode = auditOptions.SystemMode
		if fallbackErr := auditlog.Configure(logsDir, fallbackOptions); fallbackErr != nil {
			log.Printf("ERROR: could not configure audit logging: %v", fallbackErr)
		}
	}
	if debugFlagSet {
		appmode.SetDebugEnabled(*debug)
	} else if debugValue, set := os.LookupEnv("DEBUG"); set && debugValue != "" {
		appmode.SetDebugEnabled(config.ParseBoolOrDefault("DEBUG", debugValue, appmode.DebugEnabled()))
	}
	if appmode.DebugEnabled() {
		runtime.SetBlockProfileRate(cfg.Server.Debug.BlockProfileRate)
		runtime.SetMutexProfileFraction(cfg.Server.Debug.MutexProfileFraction)
	} else {
		runtime.SetBlockProfileRate(0)
		runtime.SetMutexProfileFraction(0)
	}
	database.SetDebugQueryLogging(cfg.Server.Debug.LogQueries)
	sslEnabled := cfg.Server.SSL.Enabled || cfg.Server.SSL.LetsEncrypt.Enabled
	httpPort, httpsPort, listenerErr = resolveServerListeners(serverPort, sslEnabled)
	if listenerErr != nil {
		log.Fatalf("Invalid server port configuration: %v", listenerErr)
	}
	httpAddr, httpsAddr = listenerAddress(serverAddress, httpPort), listenerAddress(serverAddress, httpsPort)
	if cfg.Listener != nil {
		if cfg.Listener.Addr().String() != httpAddr {
			_ = cfg.Listener.Close()
			log.Fatalf("provided listener address %q does not match configured HTTP address %q", cfg.Listener.Addr(), httpAddr)
		}
		if preboundListeners == nil {
			preboundListeners = make(map[string]net.Listener)
		}
		if preboundListeners[httpAddr] != nil {
			_ = cfg.Listener.Close()
			log.Fatalf("multiple listeners were provided for HTTP address %q", httpAddr)
		}
		preboundListeners[httpAddr] = cfg.Listener
	}
	challengeType := strings.ToLower(strings.TrimSpace(cfg.Server.SSL.LetsEncrypt.Challenge))
	if challengeType == "" {
		challengeType = "http-01"
	}
	challengeType = normalizeSSLChallenge(challengeType)
	if cfg.Server.SSL.LetsEncrypt.Enabled && challengeType == "http-01" && httpsPort == "80" {
		log.Fatal("HTTP-01 requires port 80 for HTTP challenges, but the configured HTTPS listener occupies it; use DNS-01 or TLS-ALPN-01")
	}
	if cfg.Server.SSL.LetsEncrypt.Enabled && challengeType == "tls-alpn-01" && httpPort == "443" {
		log.Fatal("TLS-ALPN-01 requires port 443 for HTTPS challenges, but the configured HTTP listener occupies it; use DNS-01 or HTTP-01")
	}
	sslManager := newSSLManager(cfg, configDir, httpsPort != "")
	if validationErr := sslManager.Validate(); validationErr != nil {
		log.Fatalf("Invalid SSL configuration: %v", validationErr)
	}

	data, err := data.ReadFile("countries.json")
	if err != nil {
		log.Fatalf("Failed to read embedded country data: %v", err)
	}

	countriesService, err := countries.NewService(data)
	if err != nil {
		log.Fatalf("Failed to initialize countries service: %v", err)
	}

	log.Printf("Loaded %d countries", countriesService.Count())

	server.Version = Version
	server.Commit = CommitID
	server.BuildDate = BuildDate

	databasePath := localDatabasePath(cfg, dataDir)
	maxLifetime, _ := time.ParseDuration(cfg.Server.Database.Pool.MaxLifetime)
	maxIdleTime, _ := time.ParseDuration(cfg.Server.Database.Pool.MaxIdleTime)
	db, err := database.OpenConfigured(database.DatabaseOptions{
		Driver:  cfg.Server.Database.Driver,
		URL:     cfg.Server.Database.URL,
		Token:   cfg.Server.Database.Token,
		DataDir: dataDir,
		Pool:    database.PoolOptions{MaxOpen: cfg.Server.Database.Pool.MaxOpen, MaxIdle: cfg.Server.Database.Pool.MaxIdle, MaxLifetime: maxLifetime, MaxIdleTime: maxIdleTime},
		Cache:   database.ReplicaOptions{Enabled: cfg.Server.Database.Cache.Enabled, Path: cfg.Server.Database.Cache.Path, SyncInterval: cfg.Server.Database.Cache.SyncInterval, OfflineMode: cfg.Server.Database.Cache.OfflineMode, MaxAge: cfg.Server.Database.Cache.MaxAge},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize server database: %v\n", err)
		os.Exit(70)
	}
	auditMirrorDB = db
	if err := RecoverPGPRotation(context.Background(), db, filepath.Join(configDir, "security")); err != nil {
		log.Printf("FATAL: could not recover PGP key rotation: %v", err)
		_ = database.Close(db)
		os.Exit(70)
	}
	if err := config.MigrateConfigLayout(configPath); err != nil {
		log.Printf("WARN: configuration layout migration skipped: %v", err)
	}
	for legacyName, value := range config.ConsumeLegacyApplicationSecrets() {
		name := strings.TrimPrefix(legacyName, "server.security.")
		if strings.HasSuffix(legacyName, "_expires") {
			name = strings.TrimSuffix(name, "_expires") + "_until"
		}
		if _, secretErr := database.InsertAppSecretIfAbsent(context.Background(), db, name, value, time.Now().UTC()); secretErr != nil {
			fmt.Fprintf(os.Stderr, "Failed to import migrated application secret %s\n", name)
			os.Exit(70)
		}
	}
	if err := database.EnsureGeneratedSecrets(context.Background(), db); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize generated server secrets: %v\n", err)
		os.Exit(70)
	}
	for _, key := range cfg.GeneratedSecrets {
		recordConfigSecretAudit(logsDir, key)
	}
	if err := RecoverPendingSecretRotations(context.Background(), db, cfg, configDir, logsDir, time.Now().UTC()); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to finish pending secret rotation; server startup stopped.")
		os.Exit(70)
	}
	defer func() {
		if err := database.Close(db); err != nil {
			log.Printf("Failed to close server database: %v", err)
		}
	}()
	if cfg.Server.SSL.LetsEncrypt.Enabled && normalizeSSLChallenge(challengeType) == "dns-01" {
		if err := syncDNSValidationState(db, cfg.Server.SSL.LetsEncrypt.DNSProvider, cfg.Server.SSL.LetsEncrypt.DNSCredentials); err != nil {
			log.Printf("Could not update DNS validation state: %v", err)
		}
		sslManager.SetCertificateIssuedCallback(func() {
			validatedAt := time.Now().UTC()
			if err := database.SetAppSecret(context.Background(), db, "dns_validated_at", validatedAt.Format(time.RFC3339), validatedAt); err != nil {
				log.Printf("Could not record DNS provider validation: %v", err)
			}
		})
	} else if err := syncDNSValidationState(db, cfg.Server.SSL.LetsEncrypt.DNSProvider, cfg.Server.SSL.LetsEncrypt.DNSCredentials); err != nil {
		log.Printf("Could not update DNS validation state: %v", err)
	}
	var geoIPManager *geoip.GeoIPManager
	if cfg.Server.GeoIP.Enabled {
		manager, managerErr := geoip.NewManager(dataDir)
		if managerErr != nil {
			log.Printf("GeoIP unavailable; continuing without country lookups: %v", managerErr)
		} else {
			geoIPManager = manager
			if openErr := manager.Open(); openErr != nil {
				log.Printf("GeoIP database load incomplete; country checks will fail open: %v", openErr)
			}
			missing := manager.MissingDatabases()
			stale := manager.IsStale(time.Now().UTC(), 21*24*time.Hour)
			if len(missing) > 0 || stale {
				if len(missing) > 0 {
					log.Printf("Downloading missing GeoIP databases: %s", strings.Join(missing, ", "))
				} else {
					log.Printf("Refreshing stale GeoIP databases")
				}
				updateCtx, cancelUpdate := context.WithTimeout(context.Background(), 8*time.Minute)
				if updateErr := manager.Update(updateCtx); updateErr != nil {
					log.Printf("GeoIP initial database update incomplete; country checks will fail open: %v", updateErr)
				}
				cancelUpdate()
			}
			defer func() {
				if closeErr := manager.Close(); closeErr != nil {
					log.Printf("Close GeoIP databases: %v", closeErr)
				}
			}()
		}
	}
	securityDir := strings.ReplaceAll(cfg.Data.SecurityDir, "{data_dir}", dataDir)
	trustedRanges := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("fe80::/10"),
	}
	for _, value := range cfg.Server.TrustedProxies.Additional {
		if addr, parseErr := netip.ParseAddr(strings.TrimSpace(value)); parseErr == nil {
			trustedRanges = append(trustedRanges, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
		} else if prefix, parseErr := netip.ParsePrefix(strings.TrimSpace(value)); parseErr == nil {
			trustedRanges = append(trustedRanges, prefix.Masked())
		}
	}
	extraBlocklists := make([]securitydata.BlocklistSource, 0, len(cfg.Server.Security.Blocklists.ExtraSources))
	for _, source := range cfg.Server.Security.Blocklists.ExtraSources {
		extraBlocklists = append(extraBlocklists, securitydata.BlocklistSource{ID: source.ID, Type: source.Type, URL: source.URL, Enabled: source.Enabled})
	}
	securityDataManager, securityDataErr := securitydata.New(securitydata.Options{
		SecurityDir: securityDir, BlocklistsEnabled: cfg.Server.Security.Blocklists.Enabled,
		ExtraBlocklists: extraBlocklists, NVDAPIKey: cfg.Data.CVE.APIKey, NVDSource: cfg.Data.CVE.Source,
		TrivyEnabled: cfg.Data.Trivy.Enabled, TrivySource: cfg.Data.Trivy.Source, TrustedRanges: trustedRanges,
	})
	if securityDataErr != nil {
		log.Printf("Security data unavailable; continuing with fail-open checks: %v", securityDataErr)
	}
	emailManager, emailErr := prepareNotificationManager(cfg, configPath, configDir)
	if emailErr != nil {
		log.Printf("SMTP connection failed; email notifications disabled until next startup: %v", emailErr)
	}
	defer emailManager.Close()
	if startupUpdateErr != nil {
		log.Printf("update.failed error=%q", startupUpdateErr.Error())
		recordUpdateAudit(logsDir, "update.failed", "error", "failure", nil)
		notifySchedulerFailure(emailManager, "Update installation", startupUpdateErr)
		if notifyErr := emailManager.Notify("update_failed", map[string]string{"version": Version, "new_version": Version, "current_version": Version, "error": startupUpdateErr.Error()}); notifyErr != nil {
			log.Printf("Could not send update failure notification: %v", notifyErr)
		}
	}
	log.Printf("email.configured=%t", cfg.Server.Notifications.Email.Enabled)
	cacheTimeout, _ := time.ParseDuration(cfg.Server.Cache.Timeout)
	cacheClient, cacheErr := database.OpenCache(database.CacheOptions{
		Type: cfg.Server.Cache.Type, URL: cfg.Server.Cache.URL, Host: cfg.Server.Cache.Host,
		Port: cfg.Server.Cache.Port, Username: cfg.Server.Cache.Username, Password: cfg.Server.Cache.Password,
		Database: cfg.Server.Cache.Database, TLS: cfg.Server.Cache.TLS, TLSSkipVerify: cfg.Server.Cache.TLSSkipVerify,
		TLSCA: cfg.Server.Cache.TLSCA, TLSCert: cfg.Server.Cache.TLSCert, TLSKey: cfg.Server.Cache.TLSKey,
		ConfigDir: configDir, PoolSize: cfg.Server.Cache.PoolSize, MinIdle: cfg.Server.Cache.MinIdle, Timeout: cacheTimeout,
	})
	if cacheErr != nil {
		log.Printf("WARN: cache unavailable; database-backed rate limiting will remain active")
	}
	srv := server.New(countriesService, cfg, serverAddress, serverPort, Version, BuildDate, CommitID, db)
	srv.SetCacheClient(cacheClient, cacheErr)
	srv.SetDataDir(dataDir)
	srv.SetAuditLogDir(logsDir)
	srv.SetNotificationManager(emailManager)
	srv.SetMetricsDataDir(dataDir)
	if geoIPManager != nil {
		srv.SetGeoIPManager(geoIPManager)
	}
	if securityDataManager != nil {
		srv.SetSecurityDataManager(securityDataManager)
	}
	diskMonitor := newDiskLowMonitor(cfg.Server.Notifications.DiskLow.ThresholdPercent, cfg.Server.Notifications.DiskLow.Repeat)
	srv.SetDiskHealthStatus(diskMonitor.Status)
	healthStore := srv.HealthStore()
	if healthStore != nil {
		if err := healthStore.RegisterStat("notifications_total", func() int64 { return emailManager.Sent.Load() }); err != nil {
			log.Printf("Could not register notification health statistic: %v", err)
		}
		healthStore.RefreshChecks(context.Background())
	}

	ctx, cancel := context.WithCancel(serviceContext)
	defer cancel()
	var backgroundScheduler *backgroundscheduler.Scheduler
	configWatcher := config.NewConfigWatcher(configPath, cfg, func(_, next *config.Config, restartSettings []string) {
		*cfg = *next
		if *baseURL != "" {
			cfg.Server.BaseURL = *baseURL
		}
		cfg.Lang = selectServerLanguage(*langFlag, cfg.Lang)
		database.SetDebugQueryLogging(cfg.Server.Debug.LogQueries)
		if appmode.DebugEnabled() {
			runtime.SetBlockProfileRate(cfg.Server.Debug.BlockProfileRate)
			runtime.SetMutexProfileFraction(cfg.Server.Debug.MutexProfileFraction)
		}
		srv.ApplyReloadedConfig(cfg)
		emailManager.Config = cfg
		emailManager.Sender.Config = smtpSenderConfig(cfg.Server.Notifications.Email.SMTP)
		emailManager.FQDN = notificationFQDN(cfg)
		if backgroundScheduler != nil {
			if err := updateSchedulerConfiguration(backgroundScheduler, cfg.Server.Scheduler); err != nil {
				log.Printf("WARN: could not apply scheduler configuration: %v", err)
			}
		}
		srv.SetPendingRestart(len(restartSettings) > 0, restartSettings)
		log.Println("Configuration hot-reloaded")
	})
	configWatcher.Start(ctx)
	reasonChan := make(chan string, 1)
	backupDir := backupDirResolved
	backupSchedule, backupScheduleErr := backup.NewScheduler(backup.ScheduledOptions{
		ProjectName: projectName, BackupDir: backupDir,
		Backup: backup.Options{
			ConfigDir: configDir, DataDir: dataDir, DatabasePath: databasePath, DatabaseDriver: cfg.Server.Database.Driver,
			SecretsDB: db,
			BackupDir: backupDir, AppVersion: Version, CreatedBy: "scheduler",
			Password: cfg.Server.Backup.Password, Compliance: cfg.Server.Compliance.Enabled,
			Limits: backupLimits(cfg.Server.Restore),
		},
		Retention: backup.RetentionPolicy{
			MaxBackups: cfg.Server.Backup.Retention.MaxBackups, KeepWeekly: cfg.Server.Backup.Retention.KeepWeekly,
			KeepMonthly: cfg.Server.Backup.Retention.KeepMonthly, KeepYearly: cfg.Server.Backup.Retention.KeepYearly,
			MaxTotalSize: cfg.Server.Backup.Retention.MaxTotalSize,
		},
	})
	if backupScheduleErr != nil {
		log.Printf("Backup scheduler unavailable: %v", backupScheduleErr)
	} else if _, retentionErr := backupSchedule.ApplyRetention(ctx); retentionErr != nil {
		log.Printf("Initial backup retention sweep failed: %v", retentionErr)
	}
	if securityDataManager != nil {
		securityHealth := securityDataManager.Health()
		blocklistsNeedUpdate := false
		for key, health := range securityHealth {
			if strings.HasPrefix(key, "blocklist_") && health.Status == "error" {
				blocklistsNeedUpdate = true
				break
			}
		}
		if cfg.Server.Security.Blocklists.Enabled && blocklistsNeedUpdate {
			go func() {
				if updateErr := securityDataManager.UpdateBlocklists(ctx); updateErr != nil {
					log.Printf("Initial blocklist update incomplete; previous lists remain active: %v", updateErr)
				}
				if refreshErr := srv.RefreshHealth(ctx); refreshErr != nil && ctx.Err() == nil {
					log.Printf("Security data health refresh failed: %v", refreshErr)
				}
			}()
		}
		if securityHealth["cve_nvd"].Status == "error" || cfg.Data.Trivy.Enabled && securityHealth["trivy_db"].Status == "error" {
			go func() {
				if updateErr := securityDataManager.UpdateCVE(ctx); updateErr != nil {
					log.Printf("Initial CVE database update incomplete; continuing without fresh CVE data: %v", updateErr)
				}
				if refreshErr := srv.RefreshHealth(ctx); refreshErr != nil && ctx.Err() == nil {
					log.Printf("Security data health refresh failed: %v", refreshErr)
				}
			}()
		}
	}
	overlayManager := overlay.NewManager(overlay.Options{
		Config: *cfg, ConfigDir: configDir, DataDir: dataDir, LogsDir: logsDir, Handler: srv.OverlayHandler(),
		TorHandler:      srv.TorOverlayHandler(),
		OnTorBandwidth:  srv.SetTorBandwidthUsage,
		OnTorHTTPClient: srv.SetTorHTTPClient,
		OnStatus: func(torState overlay.TorStatus, i2pState overlay.I2PStatus) {
			srv.SetOverlayStatus(server.HealthOverlayInfo{Enabled: torState.Enabled, Running: torState.Running, Status: torState.Status, Hostname: torState.Hostname}, server.HealthI2PInfo{Enabled: i2pState.Enabled, Running: i2pState.Running, Status: i2pState.Status, State: i2pState.State, Hostname: i2pState.Hostname, Provider: i2pState.Provider})
			if torState.Running && torState.Hostname != "" {
				torAddressLogged.Do(func() { log.Printf("Tor: %s", torState.Hostname) })
			}
		},
	})
	overlayManager.Start(ctx)
	defer overlayManager.Close()
	healthRefreshDone := make(chan struct{})
	if healthStore != nil {
		go func() {
			defer close(healthRefreshDone)
			refresh := func() {
				refreshCtx, refreshCancel := context.WithTimeout(ctx, 5*time.Second)
				defer refreshCancel()
				if refreshErr := srv.RefreshHealth(refreshCtx); refreshErr != nil && ctx.Err() == nil {
					log.Printf("Health statistics refresh failed: %v", refreshErr)
				}
			}
			refresh()
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					refresh()
				}
			}
		}()
	} else {
		close(healthRefreshDone)
	}
	catchUpWindow, catchUpErr := time.ParseDuration(cfg.Server.Scheduler.CatchUpWindow)
	if catchUpErr != nil || catchUpWindow <= 0 {
		catchUpWindow = time.Hour
	}
	schedulerLogFile, err := os.OpenFile(filepath.Join(logsDir, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatalf("Failed to open scheduler log: %v", err)
	}
	defer func() {
		if err := schedulerLogFile.Close(); err != nil {
			log.Printf("Failed to close scheduler log: %v", err)
		}
	}()
	var schedulerLogMu sync.Mutex
	rotateSchedulerLog := func(now time.Time) error {
		schedulerLogMu.Lock()
		defer schedulerLogMu.Unlock()
		info, statErr := os.Stat(schedulerLogFile.Name())
		if os.IsNotExist(statErr) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if info.Size() < 50<<20 && now.Sub(info.ModTime()) < 7*24*time.Hour {
			return nil
		}
		name := schedulerLogFile.Name()
		if err := schedulerLogFile.Close(); err != nil {
			return err
		}
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
		file, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		schedulerLogFile = file
		return nil
	}
	backgroundScheduler = backgroundscheduler.NewWithOptions(backgroundscheduler.Options{
		Store: database.NewSchedulerStore(db), Timezone: cfg.Server.Scheduler.Timezone,
		DefaultTimezone: "America/New_York", CatchUpWindow: catchUpWindow,
		Logf: func(level, format string, args ...any) {
			line := time.Now().UTC().Format(time.RFC3339) + " [" + strings.ToUpper(level) + "] " + fmt.Sprintf(format, args...) + "\n"
			schedulerLogMu.Lock()
			defer schedulerLogMu.Unlock()
			if _, writeErr := (logbuffer.Writer{Buffer: logbuffer.Default(), Output: schedulerLogFile}).Write([]byte(line)); writeErr != nil {
				if _, stderrErr := fmt.Fprintf(os.Stderr, "scheduler log write failed: %v\n", writeErr); stderrErr != nil {
					log.Print("scheduler log write failed and stderr is unavailable")
				}
			}
		},
	})
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["geoip_update"]; ok && taskConfig.Enabled && cfg.Server.GeoIP.Enabled {
		retryDelay, parseErr := time.ParseDuration(taskConfig.RetryDelay)
		if parseErr != nil || retryDelay <= 0 {
			retryDelay = time.Hour
		}
		err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "geoip_update", Name: "GeoIP database update", Schedule: taskConfig.Schedule,
			Enabled: true, RetryOnFail: taskConfig.RetryOnFail, RetryDelay: retryDelay,
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("geoip_update")
				if geoIPManager == nil {
					updateErr := fmt.Errorf("GeoIP manager is unavailable")
					srv.RecordScheduledTaskEnd("geoip_update", time.Since(started), updateErr, time.Now())
					notifySchedulerFailure(emailManager, "GeoIP database update", updateErr)
					return updateErr
				}
				updateErr := geoIPManager.Update(taskCtx)
				srv.RecordScheduledTaskEnd("geoip_update", time.Since(started), updateErr, time.Now())
				if updateErr != nil {
					notifySchedulerFailure(emailManager, "GeoIP database update", updateErr)
				}
				return updateErr
			},
		})
		if err != nil {
			log.Printf("Could not register GeoIP update task: %v", err)
		}
	}
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["token_cleanup"]; ok && taskConfig.Enabled {
		if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "token_cleanup", Name: "Token and challenge cleanup", Schedule: taskConfig.Schedule,
			Enabled: true, RetryOnFail: taskConfig.RetryOnFail, RetryDelay: taskRetryDelay(taskConfig.RetryDelay),
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("token_cleanup")
				_, _, cleanupErr := database.CleanupOwnerTokenState(taskCtx, db, time.Now(), cfg.Server.Tokens.IdlePurgeDays)
				srv.RecordScheduledTaskEnd("token_cleanup", time.Since(started), cleanupErr, time.Now())
				if cleanupErr != nil {
					notifySchedulerFailure(emailManager, "Token and challenge cleanup", cleanupErr)
				}
				return cleanupErr
			},
		}); err != nil {
			log.Printf("Could not register token cleanup task: %v", err)
		}
	}
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["healthcheck_self"]; ok && taskConfig.Enabled {
		if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "healthcheck_self", Name: "Self health check", Schedule: taskConfig.Schedule,
			Enabled: true, RetryOnFail: taskConfig.RetryOnFail, RetryDelay: taskRetryDelay(taskConfig.RetryDelay),
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("healthcheck_self")
				checkErr := db.PingContext(taskCtx)
				if checkErr == nil && countriesService.Count() == 0 {
					checkErr = fmt.Errorf("country dataset is empty")
				}
				srv.RecordScheduledTaskEnd("healthcheck_self", time.Since(started), checkErr, time.Now())
				if checkErr != nil {
					if db.PingContext(taskCtx) == nil {
						notifySchedulerFailure(emailManager, "Self health check", checkErr)
					}
				}
				return checkErr
			},
		}); err != nil {
			log.Printf("Could not register self health check task: %v", err)
		}
	}
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["database_check"]; ok && taskConfig.Enabled {
		repeat, _ := time.ParseDuration(cfg.Server.Notifications.DatabaseError.Repeat)
		retryInterval, _ := time.ParseDuration(cfg.Server.Maintenance.SelfHealing.RetryInterval)
		if retryInterval <= 0 {
			retryInterval = 30 * time.Second
		}
		monitor := &backgroundscheduler.DatabaseMonitor{
			Failures: cfg.Server.Notifications.DatabaseError.Failures,
			Repeat:   repeat,
			Probe: func(ctx context.Context) error {
				return probeDatabaseHandles(ctx, db)
			},
			Notify: func(since time.Time, message string) {
				if notifyErr := emailManager.Notify("database_error", map[string]string{
					"error": message, "since": since.Format(time.RFC3339),
				}); notifyErr != nil {
					log.Printf("Could not send database error notification: %v", notifyErr)
				}
			},
			Logf: func(level, format string, args ...any) { log.Printf("%s: %s", level, fmt.Sprintf(format, args...)) },
		}
		selfHealingExhausted := false
		if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "database_check", Name: "Database connection check", Schedule: taskConfig.Schedule,
			Enabled: true, Func: func(taskCtx context.Context) error {
				if selfHealingExhausted {
					return nil
				}
				started := time.Now()
				srv.RecordScheduledTaskStart("database_check")
				var checkErr error
			retryLoop:
				for {
					checkErr = monitor.Check(taskCtx)
					if checkErr == nil {
						if srv.IsMaintenanceMode() {
							log.Print("Issue resolved, exiting maintenance mode")
							srv.SetMaintenanceMode(false)
							if notifyErr := emailManager.Notify("scheduler_error", map[string]string{"task_name": "Database connection", "error": "Database connection recovered"}); notifyErr != nil {
								log.Printf("Could not send database recovery notification: %v", notifyErr)
							}
						}
						break
					}
					attempts, since := monitor.State()
					maximum := cfg.Server.Maintenance.SelfHealing.MaxAttempts
					exhausted := maximum > 0 && attempts >= maximum
					srv.SetSelfHealingState("database_connection", since, attempts, exhausted)
					if exhausted {
						selfHealingExhausted = true
						recordSelfHealingExhaustedAudit(logsDir, "database_connection", attempts)
						if notifyErr := emailManager.Notify("scheduler_error", map[string]string{"task_name": "Database connection", "error": "Self-healing attempts exhausted; operator action is required"}); notifyErr != nil {
							log.Printf("Could not send self-healing exhaustion notification: %v", notifyErr)
						}
						break
					}
					timer := time.NewTimer(retryInterval)
					select {
					case <-taskCtx.Done():
						timer.Stop()
						checkErr = taskCtx.Err()
						break retryLoop
					case <-timer.C:
						continue
					}
				}
				srv.RecordScheduledTaskEnd("database_check", time.Since(started), checkErr, time.Now())
				return checkErr
			},
		}); err != nil {
			log.Printf("Could not register database check task: %v", err)
		}
	}
	if overlayManager.TorBinaryFound() {
		taskConfig := cfg.Server.Scheduler.Tasks["tor_health"]
		schedule := taskConfig.Schedule
		if strings.TrimSpace(schedule) == "" {
			schedule = "@every 10m"
		}
		if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "tor_health", Name: "Tor hidden service health", Schedule: schedule, Enabled: true, RetryOnFail: true, RetryDelay: time.Minute,
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("tor_health")
				checkErr := overlayManager.HealthCheckTor(taskCtx)
				if checkErr != nil {
					if restartErr := overlayManager.RestartTor(taskCtx); restartErr != nil {
						checkErr = restartErr
					} else {
						checkErr = overlayManager.HealthCheckTor(taskCtx)
					}
				}
				srv.RecordScheduledTaskEnd("tor_health", time.Since(started), checkErr, time.Now())
				return checkErr
			},
		}); err != nil {
			log.Printf("Could not register Tor health task: %v", err)
		}
	}
	if cfg.Server.I2P.Enabled {
		taskConfig := cfg.Server.Scheduler.Tasks["i2p_health"]
		schedule := taskConfig.Schedule
		if strings.TrimSpace(schedule) == "" {
			schedule = "@every 10m"
		}
		if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "i2p_health", Name: "I2P eepsite health", Schedule: schedule, Enabled: true, RetryOnFail: true, RetryDelay: time.Minute,
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("i2p_health")
				checkErr := overlayManager.HealthCheckI2P(taskCtx)
				if checkErr != nil {
					if restartErr := overlayManager.RestartI2P(taskCtx); restartErr != nil {
						checkErr = restartErr
					} else {
						checkErr = overlayManager.HealthCheckI2P(taskCtx)
					}
				}
				srv.RecordScheduledTaskEnd("i2p_health", time.Since(started), checkErr, time.Now())
				return checkErr
			},
		}); err != nil {
			log.Printf("Could not register I2P health task: %v", err)
		}
	}
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["blocklist_update"]; ok && taskConfig.Enabled {
		if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "blocklist_update", Name: "IP and domain blocklist update", Schedule: taskConfig.Schedule,
			Enabled: true, RetryOnFail: taskConfig.RetryOnFail, RetryDelay: taskRetryDelay(taskConfig.RetryDelay),
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("blocklist_update")
				var updateErr error
				if securityDataManager == nil {
					updateErr = fmt.Errorf("security data manager is unavailable")
				} else {
					updateErr = securityDataManager.UpdateBlocklists(taskCtx)
				}
				srv.RecordScheduledTaskEnd("blocklist_update", time.Since(started), updateErr, time.Now())
				if updateErr != nil {
					notifySchedulerFailure(emailManager, "Blocklist update", updateErr)
				}
				_ = srv.RefreshHealth(taskCtx)
				return updateErr
			},
		}); err != nil {
			log.Printf("Could not register blocklist update task: %v", err)
		}
	}
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["cve_update"]; ok && taskConfig.Enabled {
		if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "cve_update", Name: "NVD and Trivy vulnerability database update", Schedule: taskConfig.Schedule,
			Enabled: true, RetryOnFail: taskConfig.RetryOnFail, RetryDelay: taskRetryDelay(taskConfig.RetryDelay),
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("cve_update")
				var updateErr error
				if securityDataManager == nil {
					updateErr = fmt.Errorf("security data manager is unavailable")
				} else {
					updateErr = securityDataManager.UpdateCVE(taskCtx)
				}
				srv.RecordScheduledTaskEnd("cve_update", time.Since(started), updateErr, time.Now())
				if updateErr != nil {
					notifySchedulerFailure(emailManager, "CVE database update", updateErr)
				}
				_ = srv.RefreshHealth(taskCtx)
				return updateErr
			},
		}); err != nil {
			log.Printf("Could not register CVE update task: %v", err)
		}
	}
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["update_check"]; ok && taskConfig.Enabled {
		if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "update_check", Name: "Release update check", Schedule: taskConfig.Schedule,
			Enabled: true, RetryOnFail: taskConfig.RetryOnFail, RetryDelay: taskRetryDelay(taskConfig.RetryDelay),
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("update_check")
				buildTime, _ := strconv.ParseInt(BuildEpoch, 10, 64)
				checkCtx, checkCancel := context.WithTimeout(taskCtx, 20*time.Second)
				release, checkErr := updater.CheckWithDefer(checkCtx, nil, updaterReleaseURL, cfg.Server.Update.Branch, Version, buildTime, cfg.Server.Update.DeferDays, time.Now().UTC())
				checkCancel()
				if checkErr == nil && release != nil {
					lastNotified, found, readErr := database.GetAppSecret(taskCtx, db, "update_notified_version")
					if readErr != nil {
						checkErr = readErr
					} else if !found || lastNotified != release.TagName {
						log.Printf("Update available: %s (current: %s)", release.TagName, Version)
						manualCommand := "countries --update yes"
						if cfg.Server.Update.AutoInstall {
							if startedElevated && runtime.GOOS != "windows" {
								stageCtx, stageCancel := context.WithTimeout(taskCtx, 2*time.Minute)
								stageErr := updater.Stage(stageCtx, nil, release, updater.BinaryName(projectName, ""), projectName, dataDir)
								stageCancel()
								if stageErr != nil {
									checkErr = stageErr
									log.Printf("update.failed version=%q error=%q", release.TagName, stageErr.Error())
									recordUpdateAudit(logsDir, "update.failed", "error", "failure", map[string]any{"version": release.TagName})
									notifySchedulerFailure(emailManager, "Update installation", stageErr)
									if notifyErr := emailManager.Notify("update_failed", map[string]string{"version": release.TagName, "new_version": release.TagName, "current_version": Version, "error": stageErr.Error()}); notifyErr != nil {
										log.Printf("Could not send update failure notification: %v", notifyErr)
									}
								} else {
									recordUpdateAudit(logsDir, "update.staged", "info", "success", map[string]any{"version": release.TagName})
									log.Printf("Update staged: %s", release.TagName)
									if notifyErr := emailManager.Notify("update_staged", map[string]string{"version": release.TagName, "new_version": release.TagName, "current_version": Version, "command": "restart to apply"}); notifyErr != nil {
										log.Printf("Could not send update staged notification: %v", notifyErr)
									}
									select {
									case reasonChan <- "update restart":
									default:
									}
									cancel()
								}
							} else {
								manualCommand = "sudo countries --update yes"
								recordUpdateAudit(logsDir, "update.install_skipped", "warn", "success", map[string]any{"reason": "not_elevated", "version": release.TagName})
								log.Printf("Automatic update install skipped: server did not start elevated")
							}
						}
						if notifyErr := emailManager.Notify("update_available", map[string]string{"version": release.TagName, "new_version": release.TagName, "current_version": Version, "channel": cfg.Server.Update.Branch, "command": manualCommand}); notifyErr != nil {
							log.Printf("Could not send update notification: %v", notifyErr)
						}
						if secretErr := database.SetAppSecret(taskCtx, db, "update_notified_version", release.TagName, time.Now().UTC()); checkErr == nil {
							checkErr = secretErr
						}
					}
				}
				srv.RecordScheduledTaskEnd("update_check", time.Since(started), checkErr, time.Now())
				if checkErr != nil {
					notifySchedulerFailure(emailManager, "Update check", checkErr)
				}
				return checkErr
			},
		}); err != nil {
			log.Printf("Could not register update check task: %v", err)
		}
	}
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["backup_daily"]; ok && taskConfig.Enabled {
		if backupSchedule == nil {
			log.Printf("Could not register daily backup task: %v", backupScheduleErr)
		} else if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "backup_daily", Name: "Daily backup", Schedule: taskConfig.Schedule,
			Enabled: true, RetryOnFail: taskConfig.RetryOnFail, RetryDelay: taskRetryDelay(taskConfig.RetryDelay),
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("backup_daily")
				result, runErr := backupSchedule.RunDaily(taskCtx)
				if runErr == nil && result.Skipped {
					log.Printf("Daily backup skipped: %s", result.SkipReason)
				}
				srv.RecordScheduledTaskEnd("backup_daily", time.Since(started), runErr, time.Now())
				if runErr != nil {
					notifySchedulerFailure(emailManager, "Daily backup", runErr)
					if notifyErr := emailManager.Notify("backup_failed", map[string]string{"error": runErr.Error(), "timestamp": time.Now().UTC().Format(time.RFC3339)}); notifyErr != nil {
						log.Printf("Could not send backup failure notification: %v", notifyErr)
					}
				} else if result.Full != nil {
					if notifyErr := emailManager.Notify("backup_complete", map[string]string{"filename": filepath.Base(result.Full.Path), "size": strconv.FormatInt(result.Full.Size, 10)}); notifyErr != nil {
						log.Printf("Could not send backup completion notification: %v", notifyErr)
					}
				}
				return runErr
			},
		}); err != nil {
			log.Printf("Could not register daily backup task: %v", err)
		}
	}
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["backup_hourly"]; ok && taskConfig.Enabled {
		if backupSchedule == nil {
			log.Printf("Could not register hourly backup task: %v", backupScheduleErr)
		} else if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "backup_hourly", Name: "Hourly backup", Schedule: taskConfig.Schedule,
			Enabled: true, RetryOnFail: taskConfig.RetryOnFail, RetryDelay: taskRetryDelay(taskConfig.RetryDelay),
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("backup_hourly")
				result, runErr := backupSchedule.RunHourly(taskCtx)
				if runErr == nil && result.Skipped {
					log.Printf("Hourly backup skipped: %s", result.SkipReason)
				}
				srv.RecordScheduledTaskEnd("backup_hourly", time.Since(started), runErr, time.Now())
				if runErr != nil {
					notifySchedulerFailure(emailManager, "Hourly backup", runErr)
					if notifyErr := emailManager.Notify("backup_failed", map[string]string{"error": runErr.Error(), "timestamp": time.Now().UTC().Format(time.RFC3339)}); notifyErr != nil {
						log.Printf("Could not send backup failure notification: %v", notifyErr)
					}
				}
				return runErr
			},
		}); err != nil {
			log.Printf("Could not register hourly backup task: %v", err)
		}
	}
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["security_cleanup"]; ok && taskConfig.Enabled {
		if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "security_cleanup", Name: "Security report and retired key cleanup", Schedule: taskConfig.Schedule,
			Enabled: true, RetryOnFail: taskConfig.RetryOnFail, RetryDelay: taskRetryDelay(taskConfig.RetryDelay),
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("security_cleanup")
				cleanupErr := runSecurityCleanup(taskCtx, db, configDir, logsDir, cfg.Server.Security.Reports.RetentionDays, time.Now().UTC())
				srv.RecordScheduledTaskEnd("security_cleanup", time.Since(started), cleanupErr, time.Now())
				if cleanupErr != nil {
					notifySchedulerFailure(emailManager, "Security cleanup", cleanupErr)
				}
				return cleanupErr
			},
		}); err != nil {
			log.Printf("Could not register security cleanup task: %v", err)
		}
	}
	if len(cfg.Web.Security.Keyservers) > 0 {
		if taskConfig, ok := cfg.Server.Scheduler.Tasks["keyserver_retry"]; ok && taskConfig.Enabled {
			if err := backgroundScheduler.Register(backgroundscheduler.Task{
				ID: "keyserver_retry", Name: "PGP keyserver publication retries", Schedule: taskConfig.Schedule, Enabled: true, RetryOnFail: taskConfig.RetryOnFail, RetryDelay: taskRetryDelay(taskConfig.RetryDelay),
				Func: func(taskCtx context.Context) error {
					started := time.Now()
					srv.RecordScheduledTaskStart("keyserver_retry")
					retryErr := retryPGPPublication(taskCtx, cfg, db, configDir, time.Now().UTC())
					srv.RecordScheduledTaskEnd("keyserver_retry", time.Since(started), retryErr, time.Now())
					if retryErr != nil {
						notifySchedulerFailure(emailManager, "PGP keyserver publication", retryErr)
					}
					return retryErr
				},
			}); err != nil {
				log.Printf("Could not register PGP keyserver retry task: %v", err)
			}
		}
	}
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["log_rotation"]; ok && taskConfig.Enabled {
		if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "log_rotation", Name: "Log rotation", Schedule: taskConfig.Schedule,
			Enabled: true, RetryOnFail: taskConfig.RetryOnFail, RetryDelay: taskRetryDelay(taskConfig.RetryDelay),
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("log_rotation")
				rotationErr := taskCtx.Err()
				if rotationErr == nil {
					rotationErr = rotateSchedulerLog(time.Now())
				}
				srv.RecordScheduledTaskEnd("log_rotation", time.Since(started), rotationErr, time.Now())
				if rotationErr != nil {
					notifySchedulerFailure(emailManager, "Log rotation", rotationErr)
				}
				return rotationErr
			},
		}); err != nil {
			log.Printf("Could not register log rotation task: %v", err)
		}
	}
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["disk_check"]; ok && taskConfig.Enabled && cfg.Server.Notifications.DiskLow.Enabled {
		if err := backgroundScheduler.Register(backgroundscheduler.Task{
			ID: "disk_check", Name: "Disk space check", Schedule: taskConfig.Schedule,
			Enabled: true, RetryOnFail: taskConfig.RetryOnFail, RetryDelay: taskRetryDelay(taskConfig.RetryDelay),
			Func: func(taskCtx context.Context) error {
				started := time.Now()
				srv.RecordScheduledTaskStart("disk_check")
				diskErr := diskMonitor.Check(taskCtx, dataDir, logsDir, backupDirResolved, emailManager)
				srv.RecordScheduledTaskEnd("disk_check", time.Since(started), diskErr, time.Now())
				if diskErr != nil {
					notifySchedulerFailure(emailManager, "Disk space check", diskErr)
				}
				return diskErr
			},
		}); err != nil {
			log.Printf("Could not register disk check task: %v", err)
		}
	}
	sslRenewalSchedule := "0 3 * * *"
	sslRenewalRetry := time.Hour
	if taskConfig, ok := cfg.Server.Scheduler.Tasks["ssl_renewal"]; ok {
		if strings.TrimSpace(taskConfig.Schedule) != "" {
			sslRenewalSchedule = taskConfig.Schedule
		}
		sslRenewalRetry = taskRetryDelay(taskConfig.RetryDelay)
	}
	if err := backgroundScheduler.Register(backgroundscheduler.Task{
		ID: "ssl_renewal", Name: "SSL certificate renewal", Schedule: sslRenewalSchedule,
		Enabled: true, RetryOnFail: true, RetryDelay: sslRenewalRetry,
		Func: func(taskCtx context.Context) error {
			started := time.Now()
			srv.RecordScheduledTaskStart("ssl_renewal")
			renewalErr := sslManager.RenewManagedCertificate(taskCtx, sslDomains(cfg))
			srv.RecordScheduledTaskEnd("ssl_renewal", time.Since(started), renewalErr, time.Now())
			if renewalErr != nil {
				log.Printf("SSL certificate renewal failed: %v", renewalErr)
				if notifyErr := emailManager.Notify("ssl_renewal_failed", map[string]string{"error": renewalErr.Error(), "timestamp": time.Now().UTC().Format(time.RFC3339)}); notifyErr != nil {
					log.Printf("Could not send SSL renewal notification: %v", notifyErr)
				}
			}
			return renewalErr
		},
	}); err != nil {
		log.Printf("Could not register SSL renewal task: %v", err)
	}
	backgroundScheduler.Start()
	srv.SetScheduler(backgroundScheduler)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		for sig := range sigChan {
			switch sig {
			case syscall.SIGHUP:
				log.Println("Received SIGHUP, reloading configuration...")
				if err := configWatcher.ReloadNow(); err != nil {
					log.Printf("Failed to reload configuration: %v", err)
				}
			default:
				log.Printf("Received signal %v, shutting down...", sig)
				select {
				case reasonChan <- "signal":
				default:
				}
				cancel()
				return
			}
		}
	}()

	primaryAddr := httpAddr
	if primaryAddr == "" {
		primaryAddr = httpsAddr
	}
	startupURLs := make([]string, 0, 2)
	if portNumber, err := strconv.Atoi(httpPort); err == nil && portNumber > 0 {
		if displayURL := network.GetDisplayURL(projectName, portNumber, false); displayURL != "" {
			startupURLs = append(startupURLs, displayURL)
		}
	}
	if portNumber, err := strconv.Atoi(httpsPort); err == nil && portNumber > 0 {
		if displayURL := network.GetDisplayURL(projectName, portNumber, true); displayURL != "" {
			startupURLs = append(startupURLs, displayURL)
		}
	}
	if len(startupURLs) == 0 {
		unresolvedFQDNWarning.Do(func() { log.Printf("WARN: could not resolve server FQDN; display URLs are unavailable") })
	}
	var forceColor *bool
	if strings.EqualFold(*color, "yes") {
		value := true
		forceColor = &value
	} else if strings.EqualFold(*color, "no") {
		value := false
		forceColor = &value
	}
	var setupToken string
	for _, key := range cfg.GeneratedSecrets {
		if key == "server.token" {
			setupToken = cfg.Server.Token
			break
		}
	}
	torStatus, i2pStatus := waitForStartupOverlayAddresses(ctx, overlayManager, overlayManager.TorBinaryFound(), cfg.Server.I2P.Enabled)
	var torOnion, i2pAddress string
	if torStatus.Enabled && torStatus.Running {
		torOnion = torStatus.Hostname
	}
	if i2pStatus.Enabled && i2pStatus.Running {
		i2pAddress = i2pStatus.Hostname
	}
	smtp := cfg.Server.Notifications.Email.SMTP
	smtpAddress := ""
	if cfg.Server.Notifications.Email.Enabled && smtp.Host != "" {
		smtpAddress = net.JoinHostPort(smtp.Host, strconv.Itoa(smtp.Port))
	}
	banner.PrintServerStartupBanner(banner.ServerBannerInfo{
		AppName: projectName, Version: Version, AppMode: string(appmode.Get()),
		StartupDatetime: time.Now().In(serverTimezone()).Format("January 02, 2006 at 15:04:05 MST"),
		URLs:            startupURLs, OnionAddress: torOnion, I2PAddress: i2pAddress, SMTPAddress: smtpAddress,
		SetupToken: setupToken, ForceColor: forceColor, Lang: cfg.Lang,
	})
	log.Printf("Starting countries server on %s", primaryAddr)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- srv.RunListenersWithPrebound(ctx, httpAddr, httpsAddr, sslManager, sslDomains(cfg), preboundListeners)
	}()
	ready, runErr := waitForListener(ctx, primaryAddr, serverDone)
	started := ready
	if ready {
		log.Printf("server.ready address=%q version=%q mode=%q", primaryAddr, Version, cfg.Server.Mode)
		if notifyErr := emailManager.Notify("startup", map[string]string{"hostname": notificationHostname(), "version": Version, "mode": cfg.Server.Mode, "timestamp": time.Now().UTC().Format(time.RFC3339)}); notifyErr != nil {
			log.Printf("Could not send startup notification: %v", notifyErr)
		}
		select {
		case runErr = <-serverDone:
		case <-ctx.Done():
			runErr = <-serverDone
		}
	}
	gracefulShutdown := ctx.Err() != nil
	backgroundScheduler.Stop()
	srv.SetSchedulerRunning(false)
	if err := srv.CloseCache(); err != nil {
		log.Printf("WARN: failed to flush rate-limit counters or close cache: %v", err)
	}
	cancel()
	overlayManager.Close()
	<-healthRefreshDone
	if err := srv.FlushHealth(context.Background()); err != nil {
		log.Printf("Could not flush health statistics: %v", err)
	}
	shutdownReason := ""
	if gracefulShutdown && started {
		shutdownReason = "signal"
		select {
		case shutdownReason = <-reasonChan:
		default:
		}
		log.Printf("server.stopped reason=%q", shutdownReason)
		emailManager.Sender.Config.Timeout = 5 * time.Second
		if notifyErr := emailManager.Notify("shutdown", map[string]string{"hostname": notificationHostname(), "version": Version, "mode": cfg.Server.Mode, "reason": shutdownReason, "timestamp": time.Now().UTC().Format(time.RFC3339)}); notifyErr != nil {
			log.Printf("Could not send shutdown notification: %v", notifyErr)
		}
	}
	if runErr != nil {
		log.Printf("Server error: %v", runErr)
		return 1
	}
	if shutdownReason == "update restart" {
		return 75
	}
	return 0
}

func preScanCommandHelp(args []string) bool {
	for index := 0; index < len(args); index++ {
		name, value, hasValue := strings.Cut(args[index], "=")
		if name != "--shell" && name != "--service" && name != "--maintenance" && name != "--update" {
			continue
		}
		if !hasValue && index+1 < len(args) {
			value = args[index+1]
		}
		if value != "--help" && value != "-h" && value != "help" {
			continue
		}
		switch name {
		case "--shell":
			_ = runServerShell("help", "")
		case "--service":
			fmt.Println("Service commands: start, stop, restart, reload, status, --install, --uninstall, --disable")
		case "--maintenance":
			fmt.Println("Maintenance commands: backup, restore, update, mode, setup, pgp, security, secret, token, data, compliance, scheduler, email, tor")
		case "--update":
			fmt.Println("Update commands: check, yes, branch {stable|beta|devel}")
		}
		return true
	}
	return false
}

func recordUpdateAudit(logDir, eventName, severity, result string, details map[string]any) {
	err := auditlog.WriteAndMirror(logDir, auditMirrorDB, auditlog.Event{
		Event: eventName, Category: "update", Severity: severity,
		Actor: auditlog.Actor{Type: "system", ID: projectName}, Result: result, Details: details,
	}, time.Now().UTC())
	if err != nil {
		log.Printf("Could not record audit event %s: %v", eventName, err)
	}
}

func recordConfigSecretAudit(logDir, key string) {
	if err := auditlog.WriteAndMirror(logDir, auditMirrorDB, auditlog.Event{
		Event: "config.secret_generated", Category: "config", Severity: "info",
		Actor: auditlog.Actor{Type: "system", ID: projectName}, Result: "success",
		Details: map[string]any{"key": key},
	}, time.Now().UTC()); err != nil {
		log.Printf("Could not record generated config secret event: %v", err)
	}
}

func recordSelfHealingExhaustedAudit(logDir, reason string, attempts int) {
	if err := auditlog.WriteAndMirror(logDir, auditMirrorDB, auditlog.Event{
		Event: "server.self_healing_exhausted", Category: "server", Severity: "critical",
		Actor: auditlog.Actor{Type: "system", ID: projectName}, Result: "failure",
		Details: map[string]any{"reason": reason, "attempts": attempts},
	}, time.Now().UTC()); err != nil {
		log.Printf("Could not record self-healing exhaustion audit event: %v", err)
	}
}

func serverTimezone() *time.Location {
	name := strings.TrimSpace(os.Getenv("TZ"))
	if name == "" {
		name = "America/New_York"
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		log.Printf("WARN: invalid server timezone %q; using America/New_York", name)
		location, _ = time.LoadLocation("America/New_York")
	}
	if location == nil {
		return time.FixedZone("UTC", 0)
	}
	return location
}

func waitForStartupOverlayAddresses(ctx context.Context, manager *overlay.Manager, torEnabled, i2pEnabled bool) (overlay.TorStatus, overlay.I2PStatus) {
	if manager == nil {
		return overlay.TorStatus{}, overlay.I2PStatus{}
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		torStatus, i2pStatus := manager.TorStatus(), manager.I2PStatus()
		torReady := !torEnabled || torStatus.Hostname != "" || torStatus.Status == "unavailable"
		i2pReady := !i2pEnabled || i2pStatus.Hostname != "" || i2pStatus.Status == "unavailable" || i2pStatus.State == "no_provider" || i2pStatus.State == "error"
		if torReady && i2pReady {
			return torStatus, i2pStatus
		}
		select {
		case <-ctx.Done():
			return torStatus, i2pStatus
		case <-deadline.C:
			return torStatus, i2pStatus
		case <-ticker.C:
		}
	}
}

func maybeEscalateSystemService(command string, alreadyElevated bool) bool {
	if alreadyElevated || !service.SystemServiceTarget(command) || !term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	if !service.CanElevate() {
		return false
	}
	fmt.Printf("Manage the system service with administrator privileges? [Y/n] ")
	var response string
	if _, err := fmt.Scanln(&response); err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	if response != "" && !strings.EqualFold(response, "y") && !strings.EqualFold(response, "yes") {
		return false
	}
	if service.Elevate(os.Args) == nil {
		return true
	}
	return false
}

func taskRetryDelay(value string) time.Duration {
	delay, err := time.ParseDuration(value)
	if err != nil || delay <= 0 {
		return time.Hour
	}
	return delay
}

func probeDatabaseHandles(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database handle is unavailable")
	}
	for handle := 0; handle < 2; handle++ {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		connection, err := db.Conn(probeCtx)
		if err != nil {
			cancel()
			return err
		}
		var result int
		err = connection.QueryRowContext(probeCtx, "SELECT 1").Scan(&result)
		closeErr := connection.Close()
		cancel()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func runSecurityCleanup(ctx context.Context, db *sql.DB, configDir, logsDir string, retentionDays int, now time.Time) error {
	if retentionDays <= 0 {
		retentionDays = 365
	}
	cutoff := now.AddDate(0, 0, -retentionDays)
	if err := database.Write(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM security_reports WHERE closed_at IS NOT NULL AND closed_at <= ?`, cutoff)
		return err
	}); err != nil {
		return fmt.Errorf("delete expired closed security reports: %w", err)
	}
	if err := CleanupExpiredSecretRotationRows(ctx, db, logsDir, now); err != nil {
		return fmt.Errorf("clean up expired secret rotation data: %w", err)
	}
	retiredKey := filepath.Join(configDir, "security", "pgp.priv.old.asc.enc")
	info, err := os.Stat(retiredKey)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect retired PGP key: %w", err)
	}
	if now.Sub(info.ModTime()) < 30*24*time.Hour {
		return nil
	}
	if err := os.Remove(retiredKey); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove expired retired PGP key: %w", err)
	}
	return nil
}

func waitForListener(ctx context.Context, address string, serverDone <-chan error) (bool, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return false, err
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	probeAddress := net.JoinHostPort(host, port)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	for {
		connection, dialErr := net.DialTimeout("tcp", probeAddress, 250*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return true, nil
		}
		select {
		case runErr := <-serverDone:
			return false, runErr
		case <-ctx.Done():
			return false, nil
		case <-timeout.C:
			return false, fmt.Errorf("listener did not bind within 30 seconds: %w", dialErr)
		case <-ticker.C:
		}
	}
}

func prepareNotificationManager(cfg *config.Config, configPath, configDir string) (*notifications.Manager, error) {
	email := &cfg.Server.Notifications.Email
	if strings.TrimSpace(email.SMTP.Host) == "" {
		if host, port, ok := detectSMTP(email.SMTP, notificationFQDN(cfg)); ok {
			email.SMTP.Host, email.SMTP.Port = host, port
			if err := persistDetectedSMTP(configPath, host, port); err != nil {
				log.Printf("Could not persist detected SMTP server: %v", err)
			}
			log.Printf("Detected SMTP server %s:%d", host, port)
		}
	}
	if ip := privateSMTPAddress(email.SMTP.Host); ip != "" && email.SMTP.TLS != "none" {
		log.Printf("SMTP host %s is a private address: TLS certificate verification skipped", ip)
	}
	sender := notifications.Sender{Config: smtpSenderConfig(email.SMTP)}
	var checkErr error
	if email.SMTP.Host == "" {
		email.Enabled = false
		log.Printf("SMTP not configured; email notifications disabled")
	} else if checkErr = sender.Check(); checkErr != nil {
		email.Enabled = false
	} else {
		email.Enabled = true
	}
	return &notifications.Manager{
		Config: cfg, Templates: server.EmailTemplateFS(), OverrideDir: filepath.Join(configDir, "template", "email"),
		ProjectName: projectName, FQDN: notificationFQDN(cfg), Hostname: notificationHostname(), Version: Version, Mode: cfg.Server.Mode, Sender: sender,
	}, checkErr
}

func smtpSenderConfig(smtp config.SMTPConfig) notifications.SMTPConfig {
	return notifications.SMTPConfig{Host: smtp.Host, Port: smtp.Port, Username: smtp.Username, Password: smtp.Password, TLS: smtp.TLS}
}

func updateSchedulerConfiguration(scheduler *backgroundscheduler.Scheduler, cfg config.SchedulerConfig) error {
	catchUpWindow, err := time.ParseDuration(cfg.CatchUpWindow)
	if err != nil {
		return fmt.Errorf("parse scheduler catch-up window: %w", err)
	}
	tasks := make(map[string]backgroundscheduler.TaskConfiguration, len(cfg.Tasks))
	for id, task := range cfg.Tasks {
		retryDelay := time.Duration(0)
		if strings.TrimSpace(task.RetryDelay) != "" {
			retryDelay, err = time.ParseDuration(task.RetryDelay)
			if err != nil {
				return fmt.Errorf("parse scheduler task %s retry delay: %w", id, err)
			}
		}
		tasks[id] = backgroundscheduler.TaskConfiguration{
			Enabled: task.Enabled, Schedule: task.Schedule, RetryOnFail: task.RetryOnFail, RetryDelay: retryDelay,
		}
	}
	return scheduler.UpdateConfiguration(cfg.Timezone, catchUpWindow, tasks)
}

func notificationFQDN(cfg *config.Config) string {
	if cfg != nil {
		if configured := strings.TrimSpace(cfg.Server.FQDN); configured != "" {
			return configured
		}
	}
	host := network.GetFQDN(projectName)
	if host == "" {
		unresolvedFQDNWarning.Do(func() { log.Printf("WARN: could not resolve server FQDN; URLs will be omitted") })
	}
	return host
}

func notificationHostname() string {
	host, err := os.Hostname()
	if err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}
	return "localhost"
}

const smtpAutodetectTimeout = time.Second

func detectSMTP(smtp config.SMTPConfig, fqdn string) (string, int, bool) {
	return detectSMTPWithCheck(smtp, fqdn, func(probe notifications.SMTPConfig) error {
		return (notifications.Sender{Config: probe}).Check()
	})
}

func detectSMTPWithCheck(smtp config.SMTPConfig, fqdn string, check func(notifications.SMTPConfig) error) (string, int, bool) {
	hosts := []string{"127.0.0.1", "172.17.0.1", defaultGatewayIPv4(), fqdn, globalIPv4()}
	if fqdn != "localhost" {
		hosts = append(hosts, "mail."+fqdn, "smtp."+fqdn)
	}
	seen := make(map[string]bool)
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		for _, port := range []int{25, 465, 587} {
			probe := smtp
			probe.Host, probe.Port = host, port
			sender := smtpSenderConfig(probe)
			sender.Timeout = smtpAutodetectTimeout
			if check(sender) == nil {
				return host, port, true
			}
		}
	}
	return "", 0, false
}

func defaultGatewayIPv4() string {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for index, line := range strings.Split(string(data), "\n") {
		if index == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != "00000000" {
			continue
		}
		flags, parseErr := strconv.ParseUint(fields[3], 16, 32)
		if parseErr != nil || flags&2 == 0 {
			continue
		}
		gateway, parseErr := strconv.ParseUint(fields[2], 16, 32)
		if parseErr == nil {
			return fmt.Sprintf("%d.%d.%d.%d", gateway&255, (gateway>>8)&255, (gateway>>16)&255, (gateway>>24)&255)
		}
	}
	return ""
}

func globalIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, networkInterface := range interfaces {
		addresses, _ := networkInterface.Addrs()
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			return ip.String()
		}
	}
	return ""
}

func persistDetectedSMTP(configPath, host string, port int) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	if len(document.Content) == 0 {
		return fmt.Errorf("server.yml has no document")
	}
	root := document.Content[0]
	serverNode := yamlMappingValue(root, "server", true)
	notificationsNode := yamlMappingValue(serverNode, "notifications", true)
	emailNode := yamlMappingValue(notificationsNode, "email", true)
	smtpNode := yamlMappingValue(emailNode, "smtp", true)
	yamlSetMappingValue(smtpNode, "host", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: host})
	yamlSetMappingValue(smtpNode, "port", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(port)})
	encoded, err := yaml.Marshal(&document)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(configPath), ".server.yml-*")
	if err != nil {
		return err
	}
	tempPath := temporary.Name()
	defer func() {
		if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("remove temporary server configuration: %v", err)
		}
	}()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, configPath)
}

func yamlMappingValue(mapping *yaml.Node, key string, create bool) *yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		mapping.Kind, mapping.Tag = yaml.MappingNode, "!!map"
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	if !create {
		return nil
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valueNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content, keyNode, valueNode)
	return valueNode
}

func yamlSetMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content[index+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func runEmailCommand(args []string, cfg *config.Config, configPath, configDir string) error {
	if len(args) == 0 || args[0] != "test" {
		return fmt.Errorf("usage: countries email test [--to ADDRESS] [--dry-run]")
	}
	recipient := ""
	dryRun := false
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--dry-run":
			dryRun = true
		case "--to":
			if index+1 >= len(args) {
				return fmt.Errorf("--to requires an address")
			}
			index++
			recipient = args[index]
		default:
			return fmt.Errorf("unknown email test option %q", args[index])
		}
	}
	if dryRun {
		manager := &notifications.Manager{Config: cfg, Templates: server.EmailTemplateFS(), OverrideDir: filepath.Join(configDir, "template", "email"), ProjectName: projectName, FQDN: notificationFQDN(cfg), Hostname: notificationHostname(), Version: Version, Mode: cfg.Server.Mode}
		results, err := manager.DryRun()
		if err != nil {
			return err
		}
		for _, result := range results {
			fmt.Println(result)
		}
		return nil
	}
	manager, checkErr := prepareNotificationManager(cfg, configPath, configDir)
	if checkErr != nil || !cfg.Server.Notifications.Email.Enabled {
		return fmt.Errorf("email test requires a valid SMTP configuration")
	}
	if recipient == "" {
		recipient = cfg.Server.Notifications.Email.ReplyTo
	}
	subject, err := manager.SendTest(recipient)
	if err != nil {
		return err
	}
	fmt.Printf("Sent test email to %s using template test: %s\n", recipient, subject)
	return nil
}

func privateSMTPAddress(host string) string {
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return ""
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return ip.String()
	}
	if ip.To4() != nil {
		if ip[12] == 100 && ip[13]&0xc0 == 64 {
			return ip.String()
		}
	}
	return ""
}

type diskVolumeState struct {
	Low        bool
	LastNotice time.Time
}

type diskLowMonitor struct {
	mu        sync.Mutex
	threshold int
	repeat    time.Duration
	volumes   map[string]diskVolumeState
	status    string
}

func newDiskLowMonitor(threshold int, repeatText string) *diskLowMonitor {
	repeat, err := time.ParseDuration(repeatText)
	if err != nil || repeat <= 0 {
		repeat = 24 * time.Hour
	}
	if threshold < 1 || threshold > 100 {
		threshold = 85
	}
	return &diskLowMonitor{threshold: threshold, repeat: repeat, volumes: make(map[string]diskVolumeState), status: "ok"}
}

func (m *diskLowMonitor) Status() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *diskLowMonitor) Check(ctx context.Context, dataDir, logsDir, backupDir string, manager *notifications.Manager) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	partitions, _ := disk.Partitions(false)
	paths := []string{dataDir, logsDir, backupDir}
	seen := make(map[string]bool)
	var pending []struct {
		volume string
		path   string
		used   float64
		free   uint64
	}
	for _, requested := range paths {
		path := existingDiskPath(requested)
		usage, err := disk.Usage(path)
		if err != nil {
			m.setStatus("error")
			return fmt.Errorf("read disk usage for %s: %w", requested, err)
		}
		volume := diskVolumeForPath(path, partitions)
		if seen[volume] {
			continue
		}
		seen[volume] = true
		pending = append(pending, struct {
			volume string
			path   string
			used   float64
			free   uint64
		}{volume: volume, path: requested, used: usage.UsedPercent, free: usage.Free})
	}

	now := time.Now().UTC()
	var low bool
	var notices []struct {
		path string
		used float64
		free uint64
	}
	m.mu.Lock()
	for _, current := range pending {
		state, exists := m.volumes[current.volume]
		if current.used >= float64(m.threshold) {
			low = true
			if !exists || !state.Low || now.Sub(state.LastNotice) >= m.repeat {
				state = diskVolumeState{Low: true, LastNotice: now}
				m.volumes[current.volume] = state
				notices = append(notices, struct {
					path string
					used float64
					free uint64
				}{path: current.path, used: current.used, free: current.free})
				continue
			}
			state.Low = true
			m.volumes[current.volume] = state
			continue
		}
		if exists && state.Low && current.used <= float64(m.threshold-5) {
			delete(m.volumes, current.volume)
			log.Printf("disk.space_recovered path=%q volume=%q used_percent=%.1f", current.path, current.volume, current.used)
		}
	}
	if low {
		m.status = "degraded"
	} else {
		m.status = "ok"
	}
	m.mu.Unlock()

	for _, notice := range notices {
		log.Printf("disk.space_low path=%q used_percent=%.1f threshold_percent=%d free_bytes=%d", notice.path, notice.used, m.threshold, notice.free)
		values := map[string]string{
			"path": notice.path, "used_percent": fmt.Sprintf("%.1f", notice.used), "free": humanBytes(notice.free),
			"threshold_percent": strconv.Itoa(m.threshold),
		}
		if err := manager.Notify("disk_low", values); err != nil {
			log.Printf("Could not send disk low notification: %v", err)
		}
	}
	return nil
}

func (m *diskLowMonitor) setStatus(status string) {
	m.mu.Lock()
	m.status = status
	m.mu.Unlock()
}

func existingDiskPath(path string) string {
	path = filepath.Clean(path)
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

func diskVolumeForPath(path string, partitions []disk.PartitionStat) string {
	best := ""
	for _, partition := range partitions {
		mount := filepath.Clean(partition.Mountpoint)
		relative, err := filepath.Rel(mount, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && len(mount) > len(best) {
			best = mount
		}
	}
	if best != "" {
		return best
	}
	return filepath.Clean(path)
}

func humanBytes(value uint64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	amount, exponent := float64(value), 0
	for amount >= unit && exponent < 5 {
		amount /= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %sB", amount, "KMGTPE"[exponent-1:exponent])
}

func notifySchedulerFailure(manager *notifications.Manager, task string, err error) {
	if err == nil {
		return
	}
	log.Printf("scheduler.task_failed task=%q error=%q", task, err.Error())
	if notifyErr := manager.Notify("scheduler_error", map[string]string{"task_name": task, "error": err.Error(), "next_run": "scheduled retry"}); notifyErr != nil {
		log.Printf("Could not send scheduler error notification: %v", notifyErr)
	}
}

func resolveRuntimeMode(cliMode, envMode, configured string) string {
	if strings.TrimSpace(cliMode) != "" {
		return cliMode
	}
	if strings.TrimSpace(envMode) != "" {
		return envMode
	}
	return configured
}

func runServerShell(command, shell string) error {
	binary := filepath.Base(os.Args[0])
	if shell == "" {
		shell = filepath.Base(os.Getenv("SHELL"))
	}
	if shell == "" || shell == "." || shell == "/" {
		shell = "bash"
	}
	if command == "help" || command == "--help" || command == "-h" {
		_, err := fmt.Fprintf(os.Stdout, "%s shell integration:\n  --shell completions [SHELL]  Print shell completions\n  --shell init [SHELL]         Print shell init command\n  SHELL: bash, zsh, fish, sh, dash, ksh, powershell, pwsh\n", binary)
		return err
	}
	if command != "completions" && command != "init" {
		return fmt.Errorf("usage: --shell [completions|init|help] [SHELL]")
	}
	supported := map[string]bool{"bash": true, "zsh": true, "fish": true, "sh": true, "dash": true, "ksh": true, "powershell": true, "pwsh": true}
	if !supported[shell] {
		return fmt.Errorf("unsupported shell %q", shell)
	}
	quoted := "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'"
	if command == "init" {
		switch shell {
		case "bash", "zsh":
			_, err := fmt.Fprintf(os.Stdout, "source <(%s --shell completions %s)\n", quoted, shell)
			return err
		case "fish":
			_, err := fmt.Fprintf(os.Stdout, "%s --shell completions fish | source\n", quoted)
			return err
		case "sh", "dash", "ksh":
			_, err := fmt.Fprintf(os.Stdout, "eval \"$(%s --shell completions %s)\"\n", quoted, shell)
			return err
		default:
			_, err := fmt.Fprintf(os.Stdout, "Invoke-Expression (& '%s' --shell completions %s)\n", strings.ReplaceAll(binary, "'", "''"), shell)
			return err
		}
	}
	commands := "--help --version --status --shell --service --maintenance --update --config --data --cache --log --backup --pid --address --port --baseurl --daemon --debug --i2p --color --lang --mode"
	function := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, binary)
	switch shell {
	case "bash":
		_, err := fmt.Fprintf(os.Stdout, "_%s_complete() {\n  local cur=\"${COMP_WORDS[COMP_CWORD]}\"\n  COMPREPLY=( $(compgen -W %q -- \"$cur\") )\n}\ncomplete -F _%s_complete %s\n", function, commands, function, quoted)
		return err
	case "zsh":
		_, err := fmt.Fprintf(os.Stdout, "#compdef %s\n_arguments '*:argument:(%s)'\n", binary, commands)
		return err
	case "fish":
		_, err := fmt.Fprintf(os.Stdout, "complete -c %s -f -a '%s'\n", binary, strings.ReplaceAll(commands, " ", " "))
		return err
	case "powershell", "pwsh":
		_, err := fmt.Fprintf(os.Stdout, "Register-ArgumentCompleter -Native -CommandName '%s' -ScriptBlock { param($wordToComplete) '%s'.Split(' ') | Where-Object { $_ -like \"$wordToComplete*\" } }\n", strings.ReplaceAll(binary, "'", "''"), commands)
		return err
	default:
		_, err := fmt.Fprintf(os.Stdout, "_%s_complete() { COMPREPLY=%q; }\n", function, commands)
		return err
	}
}

func printHelp() {
	name := filepath.Base(os.Args[0])
	fmt.Printf(`Countries API Server v%s

Usage: %s [options]

Options:
  --port PORT          Server port (default: from config or random 64xxx; container: 80)
  --baseurl PATH       URL path prefix (default: /)
  --address ADDRESS    Server address (default: from config or [::])
  --config DIR         Configuration directory
  --data DIR           Data directory
  --log DIR            Logs directory
  --pid FILE           Server PID file path
  -v, --version        Print version information
  --debug              Enable debug logging
  --daemon             Run as a background daemon
  --color auto|yes|no  Color output (NO_COLOR is honored in auto mode)
  --lang CODE          Language for output (default: auto)
  --shell completions [SHELL]  Print shell completions
  --shell init [SHELL]         Print shell init command
  --status             Check service status (for healthcheck)
  --i2p               Enable the I2P eepsite (default on in a container)
  -h, --help           Show this help message

Mode Commands:
  --mode production    Run in production mode
  --mode development   Run in development mode
  --mode debug         Run in debug mode

Update Commands:
  --update [check|yes|branch {stable|beta|devel}]  Check or install updates

Service Commands:
  --service start      Start the service
  --service stop       Stop the service
  --service restart    Restart the service
  --service reload     Reload configuration
  --service status     Show service status
  --service --install  Install as system or user service
  --service --uninstall Remove system service
  --service --disable  Disable the service

Maintenance Commands:
  --maintenance backup [file]   Backup configuration and data
  --maintenance backup test <file> Verify a backup archive
  --maintenance restore [file]  Restore from backup
  --maintenance update          Check for and install updates
  --maintenance mode            Show current application mode
  --maintenance setup           Run initial setup wizard
  --maintenance pgp <action>    Manage the security OpenPGP keypair
  --maintenance secret rotate <name> Rotate an allowed project secret
  --maintenance token <action>   List or revoke owner tokens
  --maintenance data <action>    Export or delete owner data
  --maintenance security <action> Manage security reports
  --maintenance compliance report Print the compliance summary

`, Version, name)
}

func printVersion() {
	fmt.Printf("%s %s (%s)\nBuilt: %s\nGo: %s\nOS/Arch: %s/%s\n",
		filepath.Base(os.Args[0]), Version, CommitID, BuildDate, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func resolveServerPort(flagPort, envPort, configPort string) string {
	for _, port := range []string{flagPort, envPort, configPort} {
		if port = strings.TrimSpace(port); port != "" {
			return port
		}
	}
	return "8080"
}

func resolveServerAddress(flagAddress, listenEnv, configAddress string) string {
	for _, address := range []string{flagAddress, listenEnv, configAddress} {
		if address = strings.TrimSpace(address); address != "" {
			return address
		}
	}
	return "::"
}

func fallbackIPv4ListenerAddress(address string) (string, bool, error) {
	trimmed := strings.TrimSpace(address)
	if trimmed == "[::]" {
		trimmed = "::"
	}
	if trimmed != "::" {
		return address, false, nil
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("::", "0"))
	if err == nil {
		_ = listener.Close()
		return address, false, nil
	}
	errorText := strings.ToLower(err.Error())
	if strings.Contains(errorText, "address family not supported") || strings.Contains(errorText, "protocol not supported") {
		return "0.0.0.0", true, nil
	}
	return "", false, err
}

func resolveServerListeners(portSpec string, sslEnabled bool) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(portSpec), ",")
	if len(parts) < 1 || len(parts) > 2 {
		return "", "", fmt.Errorf("expected one port or an HTTP,HTTPS pair")
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		port, err := strconv.Atoi(parts[i])
		if err != nil || port < 1 || port > 65535 {
			return "", "", fmt.Errorf("port %q must be between 1 and 65535", parts[i])
		}
	}
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	if parts[0] == "443" || sslEnabled {
		return "", parts[0], nil
	}
	return parts[0], "", nil
}

func listenerAddress(address, port string) string {
	if port == "" {
		return ""
	}
	address = strings.TrimSpace(address)
	if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		address = strings.TrimSuffix(strings.TrimPrefix(address, "["), "]")
	}
	return net.JoinHostPort(address, port)
}

func normalizeSSLChallenge(challenge string) string {
	switch strings.ToLower(strings.TrimSpace(challenge)) {
	case "", "http", "http01", "http-01":
		return "http-01"
	case "tls", "tlsalpn01", "tls-alpn", "tls-alpn-01":
		return "tls-alpn-01"
	case "dns", "dns01", "dns-01":
		return "dns-01"
	default:
		return strings.ToLower(strings.TrimSpace(challenge))
	}
}

func newSSLManager(cfg *config.Config, configDir string, hasHTTPSListener bool) *ssl.Manager {
	settings := cfg.Server.SSL
	return ssl.NewManager(ssl.Config{
		Enabled:  settings.Enabled || hasHTTPSListener || settings.LetsEncrypt.Enabled,
		CertPath: filepath.Join(configDir, "ssl"), Cert: settings.Cert, Key: settings.Key,
		MinVersion: settings.MinVersion,
		LetsEncrypt: ssl.LetsEncryptConfig{
			Enabled: settings.LetsEncrypt.Enabled, Email: settings.LetsEncrypt.Email,
			Challenge: settings.LetsEncrypt.Challenge, Staging: settings.LetsEncrypt.Staging,
			DNSProviderType: settings.LetsEncrypt.DNSProvider,
			DNSCredentials:  settings.LetsEncrypt.DNSCredentials,
		},
	})
}

func sslDomains(cfg *config.Config) []string {
	values := make([]string, 0)
	detectedDomains := network.GetAllDomains()
	if strings.TrimSpace(os.Getenv("DOMAIN")) == "" && cfg != nil && strings.TrimSpace(cfg.Server.FQDN) != "" {
		detectedDomains = []string{cfg.Server.FQDN}
	}
	for _, domain := range detectedDomains {
		if !isOverlayDomain(domain) {
			values = append(values, domain)
		}
	}
	if len(values) == 0 {
		if fqdn := notificationFQDN(cfg); fqdn != "" {
			values = []string{fqdn}
		}
	}
	domains := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		domain := strings.TrimSpace(value)
		if domain == "" {
			continue
		}
		if isOverlayDomain(domain) {
			continue
		}
		key := strings.ToLower(domain)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		domains = append(domains, domain)
	}
	return domains
}

func isOverlayDomain(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasSuffix(lower, ".onion") || strings.HasSuffix(lower, ".i2p") || strings.HasSuffix(lower, ".exit")
}

func dnsCredentialsFingerprint(provider string, credentials map[string]string) string {
	keys := make([]string, 0, len(credentials))
	for key := range credentials {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var material strings.Builder
	material.WriteString(strings.ToLower(strings.TrimSpace(provider)))
	for _, key := range keys {
		material.WriteByte(0)
		material.WriteString(key)
		material.WriteByte(0)
		material.WriteString(credentials[key])
	}
	digest := sha256.Sum256([]byte(material.String()))
	return hex.EncodeToString(digest[:])
}

func syncDNSValidationState(db *sql.DB, provider string, credentials map[string]string) error {
	ctx := context.Background()
	fingerprint := dnsCredentialsFingerprint(provider, credentials)
	oldFingerprint, hasFingerprint, err := database.GetAppSecret(ctx, db, "dns_config_fingerprint")
	if err != nil {
		return err
	}
	_, hasValidation, err := database.GetAppSecret(ctx, db, "dns_validated_at")
	if err != nil {
		return err
	}
	if hasValidation && (!hasFingerprint || oldFingerprint != fingerprint) {
		if err := database.DeleteAppSecret(ctx, db, "dns_validated_at"); err != nil {
			return err
		}
	}
	return database.SetAppSecret(ctx, db, "dns_config_fingerprint", fingerprint, time.Now().UTC())
}

func checkHealth(port string) error {
	url := fmt.Sprintf("http://127.0.0.1:%s%s", port, api.APIPath("server/healthz"))
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}
	if err := resp.Body.Close(); err != nil {
		return fmt.Errorf("close health response: %w", err)
	}
	return nil
}

func handleServiceCommand(cmd, configDir string, resolvedDirs ...string) {
	var err error
	switch cmd {
	case "start":
		err = service.Start()
	case "stop":
		err = service.Stop()
	case "restart":
		err = service.Restart()
	case "reload":
		err = service.Reload()
	case "status":
		err = service.Status()
	case "--install":
		fmt.Println("Installing countries service...")
		err = service.Install()
	case "--uninstall":
		fmt.Println("Uninstalling countries service...")
		if len(resolvedDirs) == 4 {
			err = service.UninstallWithPaths(service.UninstallPaths{Config: configDir, Data: resolvedDirs[0], Cache: resolvedDirs[1], Logs: resolvedDirs[2], Backup: resolvedDirs[3], PID: service.PIDDirectory()})
		} else {
			err = service.Uninstall()
		}
	case "--disable":
		err = service.Disable()
	case "--help":
		fmt.Println("Service commands: start, stop, restart, reload, status, --install, --uninstall, --disable")
	default:
		fmt.Printf("Unknown service command: %s\n", cmd)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Service command %q failed: %v\n", cmd, err)
		os.Exit(1)
	}
}

func handleMaintenanceCommand(cmd, configDir, dataDir, logsDir, configPath string) {
	args := flag.Args()

	switch cmd {
	case "backup":
		if len(args) > 0 && args[0] == "test" {
			if len(args) > 2 {
				fmt.Fprintln(os.Stderr, "Usage: countries --maintenance backup test [backup-file]")
				os.Exit(64)
			}
			backupFile := ""
			if len(args) == 2 {
				backupFile = args[1]
			} else {
				var selectErr error
				backupFile, selectErr = backup.NewestFullArchive(context.Background(), projectName, paths.GetBackupDir(projectName))
				if selectErr != nil {
					fmt.Fprintf(os.Stderr, "Failed to select newest full backup: %v\n", selectErr)
					os.Exit(1)
				}
				if backupFile == "" {
					fmt.Fprintln(os.Stderr, "No full backup is available to test")
					os.Exit(1)
				}
			}
			cfg, err := config.Load(configPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
				os.Exit(1)
			}
			password, err := promptBackupPassword(backupFile, os.Stdin, os.Stderr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to read backup password: %v\n", err)
				os.Exit(1)
			}
			verified, err := backup.Verify(context.Background(), backupFile, password, backupLimits(cfg.Server.Restore))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Backup verification failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Backup verified: %s (version %s, %s)\n", backupFile, verified.Manifest.AppVersion, verified.Manifest.Type)
			return
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
			os.Exit(1)
		}
		backupFile, includeSSL, includeData, err := parseBackupArgs(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid backup arguments: %v\n", err)
			os.Exit(64)
		}
		if backupFile == "" {
			backupDir := paths.GetBackupDir(projectName)
			extension := ".tar.gz"
			if cfg.Server.Backup.Password != "" {
				extension += ".enc"
			}
			backupFile = filepath.Join(backupDir, fmt.Sprintf("countries_backup_%s%s", time.Now().UTC().Format("2006-01-02_150405"), extension))
		}
		var secretsDB *sql.DB
		if isRemoteDatabase(cfg.Server.Database.Driver, cfg.Server.Database.URL) {
			secretsDB, err = openMaintenanceRemoteDatabase(cfg, dataDir)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Failed to connect to configured remote database")
				os.Exit(1)
			}
			defer func() { _ = secretsDB.Close() }()
		}
		if err := backup.EnsureBackupDir(filepath.Dir(backupFile)); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to prepare backup directory: %v\n", err)
			os.Exit(1)
		}
		options, err := manualBackupOptions(configDir, dataDir, cfg, includeSSL, includeData, secretsDB)
		if err == nil {
			_, err = backup.Create(context.Background(), backupFile, options)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create backup: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Backup created: %s\n", backupFile)
	case "restore":
		if len(args) > 1 {
			fmt.Println("Usage: countries --maintenance restore [backup-file]")
			os.Exit(1)
		}
		backupFile := ""
		defaultSelection := len(args) == 0
		if !defaultSelection {
			backupFile = args[0]
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
			os.Exit(1)
		}
		databasePath := localDatabasePath(cfg, dataDir)
		if defaultSelection {
			backupFile, err = backup.NewestFullArchive(context.Background(), projectName, paths.GetBackupDir(projectName))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to select newest full backup: %v\n", err)
				os.Exit(1)
			}
			if backupFile == "" {
				fmt.Fprintln(os.Stderr, "No full backup is available to restore")
				os.Exit(1)
			}
		}
		password, err := promptBackupPassword(backupFile, os.Stdin, os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to read backup password: %v\n", err)
			os.Exit(1)
		}
		var remoteDatabase *sql.DB
		if isRemoteDatabase(cfg.Server.Database.Driver, cfg.Server.Database.URL) {
			remoteDatabase, err = openMaintenanceRemoteDatabase(cfg, dataDir)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Failed to connect to configured remote database")
				os.Exit(1)
			}
			defer func() { _ = remoteDatabase.Close() }()
		}
		if err := authorizeRestoreWithConfirmation(cfg, databasePath, remoteDatabase, privilege.IsElevated(), os.Stdin, os.Stderr, defaultSelection); err != nil {
			fmt.Fprintf(os.Stderr, "Restore authorization failed: %v\n", err)
			os.Exit(1)
		}
		verified, verifyErr := backup.Verify(context.Background(), backupFile, password, backupLimits(cfg.Server.Restore))
		if verifyErr != nil {
			fmt.Fprintf(os.Stderr, "Backup verification failed: %v\n", verifyErr)
			os.Exit(1)
		}
		if verified.Manifest.AppVersion != Version {
			fmt.Fprintf(os.Stderr, "Warning: backup was created by version %s; current version is %s. Schema updates may be applied during restore.\n", verified.Manifest.AppVersion, Version)
		}
		if remoteDatabase != nil {
			if err := database.EnsureSchema(context.Background(), remoteDatabase); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to initialize remote database schema: %v\n", err)
				os.Exit(1)
			}
		}
		_, err = backup.Restore(context.Background(), backupFile, password, backup.RestoreOptions{
			ConfigDir: configDir, DataDir: dataDir, DatabasePath: databasePath,
			RemoteDatabase: remoteDatabase, SecretsDB: remoteDatabase,
			Limits: backupLimits(cfg.Server.Restore),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to restore backup: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Restore completed")
	case "update":
		cfg, err := config.Load(configPath)
		if err != nil {
			log.Printf("Failed to load configuration: %v", err)
			os.Exit(1)
		}
		command := "yes"
		if len(args) > 0 {
			command = args[0]
		}
		if err := handleUpdateCommandWithArgsElevation(command, cfg, args, privilege.IsElevated(), dataDir, configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Update command failed: %v\n", err)
			if errors.Is(err, errUpdateStaged) {
				os.Exit(75)
			}
			os.Exit(1)
		}
	case "mode":
		fmt.Printf("Current mode: %s\n", appmode.Get())
	case "setup":
		fmt.Println("Countries API Initial Setup")
		fmt.Println("===========================")
		cfg, err := config.Load(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Config: %s\n", configPath)
		fmt.Printf("Port: %s\n", cfg.Server.Port)
		fmt.Println("Setup complete.")
	case "pgp":
		if err := handlePGPMaintenance(args, configDir, dataDir, logsDir, configPath); err != nil {
			fmt.Fprintf(os.Stderr, "PGP maintenance failed: %v\n", err)
			os.Exit(1)
		}
	case "secret":
		if err := handleSecretMaintenance(args, configDir, dataDir, logsDir, configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Secret maintenance failed: %v\n", err)
			os.Exit(1)
		}
	case "security", "token", "data", "compliance":
		handled, err := runMaintenanceOps(cmd, args, configDir, dataDir, logsDir, configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Maintenance command failed: %v\n", err)
			os.Exit(1)
		}
		if !handled {
			fmt.Printf("Unknown maintenance command: %s\n", cmd)
			os.Exit(1)
		}
	default:
		fmt.Printf("Unknown maintenance command: %s\n", cmd)
		os.Exit(1)
	}
}

func parseBackupArgs(args []string) (filename string, includeSSL, includeData bool, err error) {
	for _, arg := range args {
		switch arg {
		case "--include-ssl":
			includeSSL = true
		case "--include-data":
			includeData = true
		default:
			if strings.HasPrefix(arg, "-") {
				return "", false, false, fmt.Errorf("unknown option %q", arg)
			}
			if filename != "" {
				return "", false, false, errors.New("only one backup filename may be provided")
			}
			filename = arg
		}
	}
	return filename, includeSSL, includeData, nil
}

func manualBackupOptions(configDir, dataDir string, cfg *config.Config, includeSSL, includeData bool, secretsDB ...*sql.DB) (backup.Options, error) {
	if cfg.Server.Compliance.Enabled && cfg.Server.Backup.Password == "" {
		return backup.Options{}, errors.New("compliance mode requires server.backup.encryption_password in secrets.yml")
	}
	databasePath := localDatabasePath(cfg, dataDir)
	backupDir := paths.GetBackupDir(projectName)
	var secretsDatabase *sql.DB
	if len(secretsDB) > 0 {
		secretsDatabase = secretsDB[0]
	}
	return backup.Options{
		ConfigDir: configDir, DataDir: dataDir, DatabasePath: databasePath, DatabaseDriver: cfg.Server.Database.Driver,
		SecretsDB: secretsDatabase,
		BackupDir: backupDir, AppVersion: Version, CreatedBy: "operator",
		Password: cfg.Server.Backup.Password, Compliance: cfg.Server.Compliance.Enabled,
		IncludeSSL: includeSSL, IncludeData: includeData, Limits: backupLimits(cfg.Server.Restore),
	}, nil
}

func openMaintenanceRemoteDatabase(cfg *config.Config, dataDir string) (*sql.DB, error) {
	maxLifetime, _ := time.ParseDuration(cfg.Server.Database.Pool.MaxLifetime)
	maxIdleTime, _ := time.ParseDuration(cfg.Server.Database.Pool.MaxIdleTime)
	return database.OpenConfigured(database.DatabaseOptions{
		Driver: cfg.Server.Database.Driver, URL: cfg.Server.Database.URL, Token: cfg.Server.Database.Token,
		DataDir: dataDir, SkipSchema: true,
		Pool: database.PoolOptions{MaxOpen: cfg.Server.Database.Pool.MaxOpen, MaxIdle: cfg.Server.Database.Pool.MaxIdle, MaxLifetime: maxLifetime, MaxIdleTime: maxIdleTime},
	})
}

func backupLimits(cfg config.RestoreConfig) backup.Limits {
	return backup.Limits{
		MaxArchiveSize: parseSizeLimit(cfg.MaxArchiveSize), MaxExpandedSize: parseSizeLimit(cfg.MaxExpandedSize),
		MaxFiles: cfg.MaxFiles, MaxFileSize: parseSizeLimit(cfg.MaxFileSize), MaxRatio: int64(cfg.MaxRatio),
	}
}

func promptBackupPassword(path string, input *os.File, output *os.File) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	var prefix [len("BKUP1")]byte
	n, readErr := io.ReadFull(file, prefix[:])
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return "", readErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n < len(prefix) || string(prefix[:]) != "BKUP1" {
		return "", nil
	}
	if !term.IsTerminal(int(input.Fd())) {
		return "", errors.New("encrypted backup restore requires an interactive terminal")
	}
	if _, err := fmt.Fprint(output, "Enter backup password: "); err != nil {
		return "", err
	}
	password, err := term.ReadPassword(int(input.Fd()))
	if _, writeErr := fmt.Fprintln(output); err == nil {
		err = writeErr
	}
	return string(password), err
}

func authorizeRestore(cfg *config.Config, databasePath string, remoteDatabase *sql.DB, root bool, input *os.File, output *os.File) error {
	return authorizeRestoreWithConfirmation(cfg, databasePath, remoteDatabase, root, input, output, false)
}

func authorizeRestoreWithConfirmation(cfg *config.Config, databasePath string, remoteDatabase *sql.DB, root bool, input *os.File, output *os.File, forceConfirmation bool) error {
	var empty bool
	var err error
	if remoteDatabase != nil {
		empty, err = remoteRestoreDatabaseIsEmpty(context.Background(), remoteDatabase)
	} else {
		empty, err = restoreDatabaseIsEmpty(databasePath)
	}
	if err != nil {
		return err
	}
	if empty && !forceConfirmation {
		return nil
	}
	if !term.IsTerminal(int(input.Fd())) {
		return errors.New("restoring an existing installation requires an interactive terminal")
	}
	if root {
		if _, err := fmt.Fprint(output, "This will OVERWRITE all data. Type RESTORE to continue: "); err != nil {
			return err
		}
		var confirmation string
		if _, err := fmt.Fscanln(input, &confirmation); err != nil {
			return err
		}
		if confirmation != "RESTORE" {
			return errors.New("restore confirmation did not match")
		}
		return nil
	}
	if cfg.Server.Token == "" {
		return errors.New("restore requires root or the operator token")
	}
	if _, err := fmt.Fprint(output, "This will OVERWRITE all data. Enter operator token to confirm: "); err != nil {
		return err
	}
	token, err := term.ReadPassword(int(input.Fd()))
	if _, writeErr := fmt.Fprintln(output); err == nil {
		err = writeErr
	}
	if err != nil {
		return err
	}
	if len(token) != len(cfg.Server.Token) || subtle.ConstantTimeCompare(token, []byte(cfg.Server.Token)) != 1 {
		return errors.New("operator token is invalid")
	}
	return nil
}

func remoteRestoreDatabaseIsEmpty(ctx context.Context, db *sql.DB) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'srv_%'`)
	if err != nil {
		return false, fmt.Errorf("inspect remote database schema: %w", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return false, err
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if len(tables) == 0 {
		return true, nil
	}
	for _, remoteName := range tables {
		canonical := strings.TrimPrefix(remoteName, "srv_")
		quotedName := `"` + strings.ReplaceAll(remoteName, `"`, `""`) + `"`
		var count int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quotedName).Scan(&count); err != nil {
			return false, fmt.Errorf("inspect remote database table %s: %w", canonical, err)
		}
		if count == 0 {
			continue
		}
		if canonical == "config_meta" {
			var version int
			if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM `+quotedName).Scan(&version); err != nil {
				return false, err
			}
			if count == 1 && version == 1 {
				continue
			}
		}
		if canonical == "health_request_stats" {
			var requests int64
			if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(requests_total), 0) FROM `+quotedName).Scan(&requests); err != nil {
				return false, err
			}
			if count == 1 && requests == 0 {
				continue
			}
		}
		return false, nil
	}
	return true, nil
}

func restoreDatabaseIsEmpty(databasePath string) (bool, error) {
	if info, err := os.Stat(databasePath); os.IsNotExist(err) {
		return true, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect current database: %w", err)
	} else if info.Size() == 0 {
		return true, nil
	}
	db, err := database.Open(databasePath)
	if err != nil {
		return false, fmt.Errorf("inspect current database: %w", err)
	}
	defer func() { _ = database.Close(db) }()
	for _, table := range []string{"owners", "api_tokens", "app_secrets", "audit_log", "backups", "captcha_challenges", "config", "config_meta", "health_request_hours", "health_request_stats", "pgp_keys", "rate_limits", "scheduler_history", "scheduler_tasks", "security_reports", "security_thanks"} {
		var count int64
		if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			return false, fmt.Errorf("inspect current database table %s: %w", table, err)
		}
		if count == 0 {
			continue
		}
		if table == "config_meta" {
			var version int
			if err := db.QueryRowContext(context.Background(), "SELECT COALESCE(MAX(version),0) FROM config_meta").Scan(&version); err != nil {
				return false, err
			}
			if count == 1 && version == 1 {
				continue
			}
		}
		if table == "health_request_stats" {
			var requests int64
			if err := db.QueryRowContext(context.Background(), "SELECT COALESCE(MAX(requests_total),0) FROM health_request_stats").Scan(&requests); err != nil {
				return false, err
			}
			if count == 1 && requests == 0 {
				continue
			}
		}
		return false, nil
	}
	return true, nil
}

func parseSizeLimit(value string) int64 {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return 0
	}
	unit := int64(1)
	for _, suffix := range []struct {
		name string
		base int64
	}{{"KIB", 1024}, {"MIB", 1 << 20}, {"GIB", 1 << 30}, {"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000}, {"K", 1024}, {"M", 1 << 20}, {"G", 1 << 30}, {"B", 1}} {
		if strings.HasSuffix(value, suffix.name) {
			unit = suffix.base
			value = strings.TrimSpace(strings.TrimSuffix(value, suffix.name))
			break
		}
	}
	amount, err := strconv.ParseInt(value, 10, 64)
	if err != nil || amount <= 0 || amount > int64(^uint64(0)>>1)/unit {
		return 0
	}
	return amount * unit
}

type optionalUpdateFlag struct {
	value string
	set   bool
}

func (f *optionalUpdateFlag) String() string { return f.value }

func (f *optionalUpdateFlag) Set(value string) error {
	f.value = value
	f.set = true
	return nil
}

func (f *optionalUpdateFlag) IsBoolFlag() bool { return true }

func resolveUpdateInvocation(value string, positional []string) (string, []string) {
	if value != "" && value != "true" {
		return value, positional
	}
	if len(positional) > 0 {
		switch positional[0] {
		case "check", "yes", "branch":
			return positional[0], positional[1:]
		}
	}
	return "yes", positional
}

func handleUpdateCommand(cmd string, cfg *config.Config, configPaths ...string) error {
	return handleUpdateCommandWithArgs(cmd, cfg, flag.Args(), configPaths...)
}

func handleUpdateCommandWithArgs(cmd string, cfg *config.Config, args []string, configPaths ...string) error {
	return handleUpdateCommandWithArgsElevation(cmd, cfg, args, privilege.IsElevated(), paths.GetDataDir(projectName), configPaths...)
}

func handleUpdateCommandWithArgsElevation(cmd string, cfg *config.Config, args []string, startedElevated bool, dataDir string, configPaths ...string) error {
	switch cmd {
	case "check":
		release, err := checkUpdate(cfg)
		if err != nil {
			return err
		}
		if release == nil {
			fmt.Printf("%s is up to date on the %s channel.\n", Version, cfg.Server.Update.Branch)
			return nil
		}
		fmt.Printf("Update available: %s (current: %s)\n", release.TagName, Version)
	case "yes":
		release, err := checkUpdate(cfg)
		if err != nil {
			return err
		}
		if release == nil {
			fmt.Printf("%s is up to date.\n", Version)
			return nil
		}
		binaryPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve current binary: %w", err)
		}
		binaryName := updater.BinaryName(projectName, "")
		if !startedElevated {
			if err := updater.Stage(context.Background(), nil, release, binaryName, projectName, dataDir); err != nil {
				return err
			}
			fmt.Printf("Update %s was staged. Run with elevated privileges to apply it.\n", release.TagName)
			return errUpdateStaged
		}
		if err := updater.Install(context.Background(), nil, release, binaryName, projectName, binaryPath); err != nil {
			return err
		}
		fmt.Printf("Installed %s; restarting.\n", release.TagName)
		return updater.RestartSelf()
	case "branch":
		if len(args) == 0 {
			fmt.Printf("Current branch: %s\n", cfg.Server.Update.Branch)
			return nil
		}
		branch := normalizeUpdateBranch(args[0])
		if branch != "stable" && branch != "beta" && branch != "devel" {
			return fmt.Errorf("invalid update branch %q; choose stable, beta, or devel", args[0])
		}
		cfg.Server.Update.Branch = branch
		if len(configPaths) > 0 {
			if err := config.SaveUpdateBranch(configPaths[0], branch); err != nil {
				return fmt.Errorf("save update branch: %w", err)
			}
		}
		fmt.Printf("Branch set to: %s\n", branch)
	default:
		return fmt.Errorf("unknown update command %q", cmd)
	}
	return nil
}

func checkUpdate(cfg *config.Config) (*updater.Release, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	buildEpoch, _ := strconv.ParseInt(BuildEpoch, 10, 64)
	return updater.Check(ctx, nil, updaterReleaseURL, cfg.Server.Update.Branch, Version, buildEpoch)
}

func normalizeUpdateBranch(branch string) string {
	switch strings.ToLower(strings.TrimSpace(branch)) {
	case "daily", "rolling", "dev", "development":
		return "devel"
	default:
		return strings.ToLower(strings.TrimSpace(branch))
	}
}

func runCmd(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("Command failed: %s %v: %v", name, args, err)
	}
}
