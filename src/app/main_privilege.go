package app

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/apimgr/countries/src/privilege"
	"github.com/apimgr/countries/src/updater"
)

var ensureServiceAccount = privilege.EnsureSystemAccount
var dropServicePrivileges = privilege.DropPrivileges

func bindPrivilegedListeners(addresses ...string) (map[string]net.Listener, error) {
	listeners := make(map[string]net.Listener)
	for _, address := range addresses {
		if address == "" {
			continue
		}
		_, portValue, err := net.SplitHostPort(address)
		if err != nil {
			closeListeners(listeners)
			return nil, fmt.Errorf("parse listener address %q: %w", address, err)
		}
		port, err := strconv.Atoi(portValue)
		if err != nil {
			closeListeners(listeners)
			return nil, fmt.Errorf("parse listener port %q: %w", portValue, err)
		}
		if port >= 1024 {
			continue
		}
		if listeners[address] != nil {
			continue
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			closeListeners(listeners)
			return nil, fmt.Errorf("bind privileged listener %s: %w", address, err)
		}
		listeners[address] = listener
	}
	return listeners, nil
}

func closeListeners(listeners map[string]net.Listener) {
	for _, listener := range listeners {
		_ = listener.Close()
	}
}

func rejectUnprivilegedPortUse(addresses ...string) error {
	if runtime.GOOS == "windows" || updater.CanApplyStaged() {
		return nil
	}
	for _, address := range addresses {
		if address == "" {
			continue
		}
		_, portValue, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("parse listener address %q: %w", address, err)
		}
		port, err := strconv.Atoi(portValue)
		if err != nil {
			return fmt.Errorf("parse listener port %q: %w", portValue, err)
		}
		if port < 1024 {
			return fmt.Errorf("user mode cannot bind privileged port %d; choose a port at or above 1024 or install a system service", port)
		}
	}
	return nil
}

func chownServicePaths(identity privilege.Identity, roots ...string) error {
	for _, root := range roots {
		if root == "" {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf("unsupported file type at %s", path)
			}
			if err := os.Chown(path, identity.UID, identity.GID); err != nil {
				return fmt.Errorf("change ownership of %s: %w", path, err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("walk service path %s: %w", root, err)
		}
	}
	return nil
}
