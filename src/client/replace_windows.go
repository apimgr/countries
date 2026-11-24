//go:build windows

package main

import (
	"io"
	"os"
	"path/filepath"

	"github.com/apimgr/countries/src/common/i18n"
	"golang.org/x/sys/windows"
)

func replaceClientBinary(currentPath, newBinaryPath string, selectedLanguage ...string) error {
	lang := "en"
	if len(selectedLanguage) > 0 && i18n.IsSupported(selectedLanguage[0]) {
		lang = selectedLanguage[0]
	}
	info, err := os.Stat(currentPath)
	if err != nil {
		return clientUpdateWrap(lang, "inspect_current", err)
	}
	directory := filepath.Dir(currentPath)
	staged, err := os.CreateTemp(directory, ".countries-cli-update-*")
	if err != nil {
		return clientUpdateWrap(lang, "stage_beside", err)
	}
	stagedPath := staged.Name()
	defer func() { _ = os.Remove(stagedPath) }()
	source, err := os.Open(newBinaryPath)
	if err != nil {
		_ = staged.Close()
		return clientUpdateWrap(lang, "open_download", err)
	}
	_, copyErr := io.Copy(staged, source)
	sourceErr := source.Close()
	modeErr := staged.Chmod(info.Mode().Perm())
	closeErr := staged.Close()
	if copyErr != nil {
		return clientUpdateWrap(lang, "stage_download", copyErr)
	}
	if sourceErr != nil {
		return clientUpdateWrap(lang, "close_download", sourceErr)
	}
	if modeErr != nil {
		return clientUpdateWrap(lang, "binary_permissions", modeErr)
	}
	if closeErr != nil {
		return clientUpdateWrap(lang, "close_staged", closeErr)
	}
	backupPath := currentPath + ".old"
	if _, err := os.Stat(backupPath); err == nil {
		return clientUpdateError(lang, "backup_exists", map[string]string{"path": backupPath})
	} else if !os.IsNotExist(err) {
		return clientUpdateWrap(lang, "inspect_backup", err)
	}
	if err := os.Rename(currentPath, backupPath); err != nil {
		return clientUpdateWrap(lang, "move_binary", err)
	}
	if err := os.Rename(stagedPath, currentPath); err != nil {
		_ = os.Rename(backupPath, currentPath)
		return clientUpdateWrap(lang, "install_binary", err)
	}
	oldPath, err := windows.UTF16PtrFromString(backupPath)
	if err != nil {
		_ = os.Remove(currentPath)
		_ = os.Rename(backupPath, currentPath)
		return clientUpdateWrap(lang, "encode_backup_path", err)
	}
	if err := windows.MoveFileEx(oldPath, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT); err != nil {
		return clientUpdateWrap(lang, "schedule_cleanup", err)
	}
	return nil
}
