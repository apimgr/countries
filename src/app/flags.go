package app

import (
	"flag"
	"os"
)

var (
	port           *string
	address        *string
	baseURL        *string
	langFlag       *string
	configDirFlag  *string
	dataDirFlag    *string
	cacheDirFlag   *string
	logsDirFlag    *string
	backupDirFlag  *string
	pidFileFlag    *string
	version        *bool
	debug          *bool
	color          *string
	status         *bool
	shellCmd       *string
	daemon         *bool
	i2pFlag        *bool
	help           *bool
	serviceCmd     *string
	maintenanceCmd *string
	modeFlag       *string
	updateCmd      optionalUpdateFlag
	debugFlagSet   bool
	i2pFlagSet     bool
	flagsReady     bool
)

// ParseFlags parses command line options before application startup.
func ParseFlags(args []string) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	port = flag.String("port", "", "Server port (overrides config)")
	address = flag.String("address", "", "Server address (overrides config)")
	baseURL = flag.String("baseurl", "", "URL path prefix (overrides config)")
	langFlag = flag.String("lang", "", "Language for command output")
	configDirFlag = flag.String("config", "", "Configuration directory")
	dataDirFlag = flag.String("data", "", "Data directory")
	cacheDirFlag = flag.String("cache", "", "Cache directory")
	logsDirFlag = flag.String("log", "", "Logs directory")
	backupDirFlag = flag.String("backup", "", "Backup directory")
	pidFileFlag = flag.String("pid", "", "Server PID file path")
	version = flag.Bool("version", false, "Print version information")
	debug = flag.Bool("debug", false, "Enable debug logging")
	color = flag.String("color", "auto", "Color output: auto, yes, no")
	status = flag.Bool("status", false, "Check service status (for healthcheck)")
	shellCmd = flag.String("shell", "", "Shell integration: completions, init, or help")
	daemon = flag.Bool("daemon", false, "Run as background daemon")
	i2pFlag = flag.Bool("i2p", false, "Enable the I2P eepsite")
	help = flag.Bool("help", false, "Show help")
	flag.BoolVar(help, "h", false, "Show help")
	flag.BoolVar(version, "v", false, "Print version information")
	serviceCmd = flag.String("service", "", "Service commands: start, stop, restart, reload, status, --install, --uninstall, --disable, --help")
	maintenanceCmd = flag.String("maintenance", "", "Maintenance commands: backup, restore, update, mode, setup, pgp, security, secret, token, data, compliance")
	modeFlag = flag.String("mode", "", "Application mode: production, development, debug")
	updateCmd = optionalUpdateFlag{}
	flag.Var(&updateCmd, "update", "Update commands: check, yes, branch {stable|beta|devel}")
	if err := flag.CommandLine.Parse(args); err != nil {
		panic(err)
	}
	debugFlagSet, i2pFlagSet = false, false
	flag.Visit(func(parsedFlag *flag.Flag) {
		if parsedFlag.Name == "debug" {
			debugFlagSet = true
		}
		if parsedFlag.Name == "i2p" {
			i2pFlagSet = true
		}
	})
	flagsReady = true
}
