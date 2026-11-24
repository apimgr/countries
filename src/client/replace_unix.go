//go:build !windows

package main

import (
	"io"
	"os"
	"path/filepath"

	"github.com/apimgr/countries/src/common/i18n"
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
	if err := os.Rename(stagedPath, currentPath); err != nil {
		return clientUpdateWrap(lang, "replace_binary", err)
	}
	return nil
}
