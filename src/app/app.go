// Package app owns countries server startup and lifecycle management.
package app

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/apimgr/countries/src/config"
)

// Run starts the application and returns when it shuts down.
func Run(ctx context.Context, cfg config.Config) error {
	Version = cfg.Version
	if Version == "" {
		Version = "devel"
	}
	CommitID = cfg.CommitID
	if CommitID == "" {
		CommitID = "N/A"
	}
	BuildEpoch = cfg.BuildEpoch
	if BuildEpoch == "" {
		BuildEpoch = "0"
	}
	OfficialSite = cfg.OfficialSite
	BuildDate = "N/A"
	if epoch, err := strconv.ParseInt(BuildEpoch, 10, 64); err == nil && epoch > 0 {
		BuildDate = time.Unix(epoch, 0).UTC().Format("2006-01-02T15:04:05Z")
	}
	if code := runWithContext(ctx, cfg); code != 0 {
		return fmt.Errorf("server exited with status %d", code)
	}
	return nil
}
