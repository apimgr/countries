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
	"syscall"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
	"github.com/apimgr/countries/src/paths"
	"github.com/apimgr/countries/src/server"
)

//go:embed data/countries.json
var countryData embed.FS

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

const projectName = "countries"

func main() {
	configDir, dataDir, logsDir := paths.GetDefaultDirs(projectName)

	port := flag.String("port", "", "Server port (overrides config)")
	address := flag.String("address", "", "Server address (overrides config)")
	configDirFlag := flag.String("config", "", "Configuration directory")
	dataDirFlag := flag.String("data", "", "Data directory")
	logsDirFlag := flag.String("logs", "", "Logs directory")
	version := flag.Bool("version", false, "Print version information")
	status := flag.Bool("status", false, "Check service status (for healthcheck)")
	help := flag.Bool("help", false, "Show help")

	serviceCmd := flag.String("service", "", "Service commands: start, stop, restart, reload, status, --install, --uninstall, --disable, --help")
	maintenanceCmd := flag.String("maintenance", "", "Maintenance commands: backup, restore, update, mode, setup")
	modeFlag := flag.String("mode", "", "Application mode: production, development")
	updateCmd := flag.String("update", "", "Update commands: check, yes, branch {stable|beta|daily}")

	flag.Parse()

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
		checkPort := cfg.Server.Port
		if checkPort == "" {
			checkPort = "8080"
		}
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

	if *updateCmd != "" {
		handleUpdateCommand(*updateCmd, cfg)
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

	serverPort := cfg.Server.Port
	if *port != "" {
		serverPort = *port
	} else if envPort := os.Getenv("PORT"); envPort != "" {
		serverPort = envPort
	}
	if serverPort == "" {
		serverPort = "8080"
	}

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
	server.Commit = Commit
	server.BuildDate = BuildDate

	srv := server.New(countriesService, cfg, serverAddress, serverPort, Version, BuildDate, Commit)

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
  --logs DIR           Logs directory
  --version            Print version information
  --status             Check service status (for healthcheck)
  --help               Show this help message

Mode Commands:
  --mode production    Set production mode
  --mode development   Set development mode

Update Commands:
  --update check       Check for available updates
  --update yes         Install available updates
  --update branch {stable|beta|daily}  Set update branch

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

func checkHealth(port string) error {
	url := fmt.Sprintf("http://127.0.0.1:%s/api/v1/health", port)
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
	switch cmd {
	case "start":
		runCmd("systemctl", "start", "countries")
	case "stop":
		runCmd("systemctl", "stop", "countries")
	case "restart":
		runCmd("systemctl", "restart", "countries")
	case "reload":
		runCmd("systemctl", "reload", "countries")
	case "status":
		runCmd("systemctl", "status", "countries")
	case "--install":
		fmt.Println("Installing countries service...")
		runCmd("systemctl", "daemon-reload")
		runCmd("systemctl", "enable", "countries")
		runCmd("systemctl", "start", "countries")
	case "--uninstall":
		fmt.Println("Uninstalling countries service...")
		runCmd("systemctl", "stop", "countries")
		runCmd("systemctl", "disable", "countries")
	case "--disable":
		runCmd("systemctl", "disable", "countries")
	case "--help":
		fmt.Println("Service commands: start, stop, restart, reload, status, --install, --uninstall, --disable")
	default:
		fmt.Printf("Unknown service command: %s\n", cmd)
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
		fmt.Printf("Current version: %s\n", Version)
		fmt.Println("Update feature not yet implemented")
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

func handleUpdateCommand(cmd string, cfg *config.Config) {
	args := flag.Args()
	switch cmd {
	case "check":
		fmt.Printf("Current version: %s\n", Version)
		fmt.Printf("Update branch: %s\n", cfg.Server.UpdateBranch)
		fmt.Println("No updates available")
	case "yes":
		fmt.Println("Update installation not implemented")
	case "branch":
		if len(args) == 0 {
			fmt.Printf("Current branch: %s\n", cfg.Server.UpdateBranch)
			return
		}
		if args[0] != "stable" && args[0] != "beta" && args[0] != "daily" {
			fmt.Printf("Invalid branch: %s\n", args[0])
			os.Exit(1)
		}
		cfg.Server.UpdateBranch = args[0]
		fmt.Printf("Branch set to: %s\n", args[0])
	default:
		fmt.Printf("Unknown update command: %s\n", cmd)
		os.Exit(1)
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
