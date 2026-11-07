package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/paths"
	"github.com/apimgr/countries/src/server"
	"github.com/apimgr/countries/src/service"
	"github.com/apimgr/countries/src/updater"
)

//go:embed data/countries.json
var countryData embed.FS

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

var updaterReleaseURL = "https://api.github.com/repos/apimgr/countries/releases?per_page=100"

func main() {
	configDir, dataDir, logsDir := paths.GetDefaultDirs(projectName)

	port := flag.String("port", "", "Server port (overrides config)")
	address := flag.String("address", "", "Server address (overrides config)")
	configDirFlag := flag.String("config", "", "Configuration directory")
	dataDirFlag := flag.String("data", "", "Data directory")
	logsDirFlag := flag.String("log", "", "Logs directory")
	version := flag.Bool("version", false, "Print version information")
	debug := flag.Bool("debug", false, "Enable debug logging")
	color := flag.String("color", "auto", "Color output: auto, yes, no")
	status := flag.Bool("status", false, "Check service status (for healthcheck)")
	help := flag.Bool("help", false, "Show help")
	flag.BoolVar(help, "h", false, "Show help")
	flag.BoolVar(version, "v", false, "Print version information")

	serviceCmd := flag.String("service", "", "Service commands: start, stop, restart, reload, status, --install, --uninstall, --disable, --help")
	maintenanceCmd := flag.String("maintenance", "", "Maintenance commands: backup, restore, update, mode, setup")
	modeFlag := flag.String("mode", "", "Application mode: production, development")
	var updateCmd optionalUpdateFlag
	flag.Var(&updateCmd, "update", "Update commands: check, yes, branch {stable|beta|devel}")

	flag.Parse()

	switch strings.ToLower(*color) {
	case "auto":
	case "yes":
		os.Unsetenv("NO_COLOR")
	case "no":
		os.Setenv("NO_COLOR", "1")
	default:
		fmt.Fprintf(os.Stderr, "invalid --color value %q; choose auto, yes, or no\n", *color)
		os.Exit(64)
	}
	if *debug {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
		os.Setenv("DEBUG", "1")
	}

	if *help {
		printHelp()
		os.Exit(0)
	}

	if *version {
		fmt.Printf("%s\n", Version)
		os.Exit(0)
	}

	if *configDirFlag != "" {
		configDir = *configDirFlag
	}
	if *dataDirFlag != "" {
		dataDir = *dataDirFlag
	}
	if *logsDirFlag != "" {
		logsDir = *logsDirFlag
	}

	if envConfig := os.Getenv("CONFIG_DIR"); envConfig != "" && *configDirFlag == "" {
		configDir = envConfig
	}
	if envData := os.Getenv("DATA_DIR"); envData != "" && *dataDirFlag == "" {
		dataDir = envData
	}
	if envLogs := os.Getenv("LOGS_DIR"); envLogs != "" && *logsDirFlag == "" {
		logsDir = envLogs
	}

	if err := paths.EnsureDirs(configDir, dataDir, logsDir); err != nil {
		log.Fatalf("Failed to create directories: %v", err)
	}

	configPath := filepath.Join(configDir, "server.yml")
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	if *status {
		checkPort := resolveServerPort(*port, os.Getenv("PORT"), cfg.Server.Port)
		if err := checkHealth(checkPort); err != nil {
			fmt.Fprintf(os.Stderr, "Health check failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("OK")
		os.Exit(0)
	}

	if *modeFlag != "" {
		setApplicationMode(*modeFlag, configPath)
		return
	}

	if updateCmd.set {
		command, args := resolveUpdateInvocation(updateCmd.value, flag.Args())
		if err := handleUpdateCommandWithArgs(command, cfg, args, configPath); err != nil {
			log.Printf("Update command failed: %v", err)
			os.Exit(1)
		}
		return
	}

	if *serviceCmd != "" {
		handleServiceCommand(*serviceCmd, configDir)
		return
	}

	if *maintenanceCmd != "" {
		handleMaintenanceCommand(*maintenanceCmd, configDir, dataDir, logsDir, configPath)
		return
	}

	serverPort := resolveServerPort(*port, os.Getenv("PORT"), cfg.Server.Port)

	serverAddress := cfg.Server.Address
	if *address != "" {
		serverAddress = *address
	} else if envAddr := os.Getenv("ADDRESS"); envAddr != "" {
		serverAddress = envAddr
	}
	if serverAddress == "" {
		serverAddress = "0.0.0.0"
	}

	data, err := countryData.ReadFile("data/countries.json")
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

	driver := strings.ToLower(strings.TrimSpace(cfg.Server.Database.Driver))
	if driver == "" || driver == "sqlite2" || driver == "sqlite3" {
		driver = "sqlite"
	}
	if driver != "sqlite" {
		fmt.Fprintf(os.Stderr, "Unsupported database driver %q\n", cfg.Server.Database.Driver)
		os.Exit(78)
	}
	databasePath := strings.ReplaceAll(cfg.Server.Database.URL, "{data_dir}", dataDir)
	if cfg.Server.Database.URL == "" || cfg.Server.Database.URL == "{data_dir}/db/server.db" {
		databasePath = filepath.Join(paths.GetDatabaseDir(dataDir), "server.db")
	}
	db, err := database.Open(databasePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize server database: %v\n", err)
		os.Exit(70)
	}
	defer db.Close()
	srv := server.New(countriesService, cfg, serverAddress, serverPort, Version, BuildDate, CommitID, db)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		for sig := range sigChan {
			switch sig {
			case syscall.SIGHUP:
				log.Println("Received SIGHUP, reloading configuration...")
				if _, err := config.Load(configPath); err != nil {
					log.Printf("Failed to reload configuration: %v", err)
				} else {
					log.Println("Configuration reloaded successfully")
				}
			default:
				log.Printf("Received signal %v, shutting down...", sig)
				cancel()
				return
			}
		}
	}()

	addr := serverAddress + ":" + serverPort
	log.Printf("Starting countries server on %s", addr)

	if err := srv.Run(ctx, addr); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func printHelp() {
	fmt.Printf(`Countries API Server v%s

Usage: countries [options]

Options:
  --port PORT          Server port (default: from config or 8080)
  --address ADDRESS    Server address (default: from config or 0.0.0.0)
  --config DIR         Configuration directory
  --data DIR           Data directory
  --log DIR            Logs directory
  -v, --version        Print version information
  --debug              Enable debug logging
  --color auto|yes|no  Color output (NO_COLOR is honored in auto mode)
  --status             Check service status (for healthcheck)
  -h, --help           Show this help message

Mode Commands:
  --mode production    Set production mode
  --mode development   Set development mode

Update Commands:
  --update [check|yes|branch {stable|beta|devel}]  Check or install updates

Service Commands:
  --service start      Start the service
  --service stop       Stop the service
  --service restart    Restart the service
  --service reload     Reload configuration
  --service status     Show service status
  --service --install  Install as system service
  --service --uninstall Remove system service
  --service --disable  Disable the service

Maintenance Commands:
  --maintenance backup [file]   Backup configuration and data
  --maintenance restore [file]  Restore from backup
  --maintenance update          Check for and install updates
  --maintenance mode            Show current application mode
  --maintenance setup           Run initial setup wizard

`, Version)
}

func resolveServerPort(flagPort, envPort, configPort string) string {
	for _, port := range []string{flagPort, envPort, configPort} {
		if port = strings.TrimSpace(port); port != "" {
			return port
		}
	}
	return "8080"
}

func checkHealth(port string) error {
	url := fmt.Sprintf("http://127.0.0.1:%s%s", port, api.APIPath("server/healthz"))
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}
	return nil
}

func handleServiceCommand(cmd, configDir string) {
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
		err = service.Uninstall()
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
		backupFile := ""
		if len(args) > 0 {
			backupFile = args[0]
		} else {
			backupDir := paths.GetBackupDir(projectName)
			os.MkdirAll(backupDir, 0755)
			timestamp := time.Now().Format("20060102-150405")
			backupFile = filepath.Join(backupDir, fmt.Sprintf("countries-backup-%s.tar.gz", timestamp))
		}
		fmt.Printf("Creating backup: %s\n", backupFile)
		exec.Command("tar", "-czf", backupFile, configDir, dataDir).Run()
		fmt.Printf("Backup created: %s\n", backupFile)
	case "restore":
		if len(args) == 0 {
			fmt.Println("Usage: countries --maintenance restore <backup-file>")
			os.Exit(1)
		}
		fmt.Printf("Restoring from: %s\n", args[0])
		exec.Command("tar", "-xzf", args[0], "-C", "/").Run()
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
		if err := handleUpdateCommand(command, cfg, configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Update command failed: %v\n", err)
			os.Exit(1)
		}
	case "mode":
		cfg, _ := config.Load(configPath)
		mode := cfg.Server.Mode
		if mode == "" {
			mode = "production"
		}
		fmt.Printf("Current mode: %s\n", mode)
	case "setup":
		fmt.Println("Countries API Initial Setup")
		fmt.Println("===========================")
		cfg, _ := config.Load(configPath)
		fmt.Printf("Config: %s\n", configPath)
		fmt.Printf("Port: %s\n", cfg.Server.Port)
		fmt.Println("Setup complete.")
	default:
		fmt.Printf("Unknown maintenance command: %s\n", cmd)
		os.Exit(1)
	}
}

func setApplicationMode(mode, configPath string) {
	if mode != "production" && mode != "development" {
		fmt.Printf("Invalid mode: %s\n", mode)
		os.Exit(1)
	}
	cfg, _ := config.Load(configPath)
	cfg.Server.Mode = mode
	config.Save(configPath, cfg)
	fmt.Printf("Application mode set to: %s\n", mode)
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
		if err := updater.Install(context.Background(), nil, release, binaryName, binaryPath); err != nil {
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
			if err := config.Save(configPaths[0], cfg); err != nil {
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
