package app

import (
	"github.com/apimgr/countries/src/config"
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
)

func applyCommandOptions(options config.CommandOptions) {
	port, address, baseURL = &options.Port, &options.Address, &options.BaseURL
	langFlag, configDirFlag, dataDirFlag = &options.Lang, &options.ConfigDir, &options.DataDir
	cacheDirFlag, logsDirFlag, backupDirFlag = &options.CacheDir, &options.LogsDir, &options.BackupDir
	pidFileFlag, color, shellCmd = &options.PIDFile, &options.Color, &options.Shell
	serviceCmd, maintenanceCmd, modeFlag = &options.Service, &options.Maintenance, &options.Mode
	version, debug, status = &options.Version, &options.Debug, &options.Status
	daemon, i2pFlag, help = &options.Daemon, &options.I2P, &options.Help
	updateCmd = optionalUpdateFlag{value: options.Update, set: options.UpdateSet}
	debugFlagSet, i2pFlagSet = options.DebugSet, options.I2PSet
}
