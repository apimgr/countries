package main

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/apimgr/countries/src/app"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/service"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Printf("Failed to load configuration: %v", err)
		var parseError *config.CLIFlagParseError
		if errors.As(err, &parseError) {
			os.Exit(2)
		}
		os.Exit(1)
	}
	cfg.Version = Version
	cfg.CommitID = CommitID
	cfg.BuildEpoch = BuildEpoch
	cfg.OfficialSite = OfficialSite
	if service.IsWindowsService() {
		if err := service.RunWindowsService(func(ctx context.Context) error { return app.Run(ctx, cfg) }); err != nil {
			log.Printf("Windows service stopped with error: %v", err)
			os.Exit(1)
		}
		return
	}
	if err := app.Run(context.Background(), cfg); err != nil {
		log.Printf("Server stopped with error: %v", err)
		os.Exit(1)
	}
}
