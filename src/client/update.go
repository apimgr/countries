package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/paths"
	"github.com/apimgr/countries/src/updater"
)

const (
	maxClientUpdateBytes     = 256 << 20
	pendingClientUpdateEvent = "COUNTRIES_CLI_UPDATE_EVENT"
)

func clientUpdateError(lang, key string, args map[string]string) error {
	return errors.New(i18n.TranslateFormat(lang, "client.update.error."+key, args))
}

func clientUpdateWrap(lang, key string, err error) error {
	return fmt.Errorf("%s: %w", i18n.Translate(lang, "client.update.error."+key), err)
}

type clientVersion struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type clientDiscovery struct {
	Versions   map[string]clientVersion `json:"cli_versions"`
	MinVersion string                   `json:"cli_min_version"`
	Data       json.RawMessage          `json:"data"`
}

func checkAndApplyClientUpdate(serverURL, token, action string, auto, interactive bool, input io.Reader, output, stderr io.Writer, selectedLanguage ...string) error {
	return checkAndApplyClientUpdateWithPrompt(serverURL, token, action, auto, interactive, input, output, stderr, selectedLanguage, nil)
}

func checkAndApplyClientUpdateWithPrompt(serverURL, token, action string, auto, interactive bool, input io.Reader, output, stderr io.Writer, selectedLanguage []string, guiPrompt func(string, string) (bool, error)) error {
	if serverURL == "" {
		return nil
	}
	lang := "en"
	if len(selectedLanguage) > 0 && i18n.IsSupported(selectedLanguage[0]) {
		lang = selectedLanguage[0]
	}
	client := &http.Client{Timeout: 15 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/api/autodiscover", nil)
	if err != nil {
		return clientUpdateWrap(lang, "create_discovery", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Language", lang)
	request.Header.Set("User-Agent", projectName+"-cli/"+Version)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return clientUpdateWrap(lang, "discover", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	closeErr := response.Body.Close()
	if readErr != nil {
		return clientUpdateWrap(lang, "read_discovery", readErr)
	}
	if closeErr != nil {
		return clientUpdateWrap(lang, "close_discovery", closeErr)
	}
	if response.StatusCode == http.StatusNotFound {
		if action == "check" {
			_, err := fmt.Fprintln(output, i18n.Translate(lang, "client.update.none_metadata"))
			return err
		}
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return clientUpdateError(lang, "discovery_http", map[string]string{"status": strconv.Itoa(response.StatusCode)})
	}
	var discovery clientDiscovery
	if err := json.Unmarshal(body, &discovery); err != nil {
		return clientUpdateWrap(lang, "decode_discovery", err)
	}
	if len(discovery.Versions) == 0 && len(discovery.Data) > 0 {
		if err := json.Unmarshal(discovery.Data, &discovery); err != nil {
			return clientUpdateWrap(lang, "decode_data", err)
		}
	}
	key := runtime.GOOS + "-" + runtime.GOARCH
	available, hasVersion := discovery.Versions[key]
	minimum := strings.TrimSpace(discovery.MinVersion)
	forced := minimum != "" && compareClientVersions(Version, minimum) < 0
	if forced {
		if err := recordClientAudit(stderr, "cli.update_forced", map[string]string{"current_version": Version, "minimum": minimum, "os_arch": key}, lang); err != nil {
			return err
		}
	}
	if !hasVersion || compareClientVersions(available.Version, Version) <= 0 {
		if action == "check" {
			_, err := fmt.Fprintln(output, i18n.Translate(lang, "client.update.none_available"))
			return err
		}
		if forced {
			return errors.New(i18n.TranslateFormat(lang, "client.update.too_old", map[string]string{"minimum": minimum, "binary": filepath.Base(os.Args[0])}))
		}
		return nil
	}
	if action == "check" {
		_, err := fmt.Fprintln(output, i18n.TranslateFormat(lang, "client.update.available", map[string]string{"version": available.Version, "current": Version}))
		return err
	}
	install := action == "yes"
	if action != "yes" && !interactive && auto {
		install = true
	}
	if action != "yes" && interactive {
		answer, err := false, error(nil)
		if guiPrompt != nil {
			answer, err = guiPrompt(available.Version, lang)
		} else {
			answer, err = askClientUpdate(input, output, available.Version, lang)
		}
		if err != nil {
			return err
		}
		install = answer
	}
	if !install {
		if forced {
			return errors.New(i18n.TranslateFormat(lang, "client.update.too_old", map[string]string{"minimum": minimum, "binary": filepath.Base(os.Args[0])}))
		}
		return nil
	}
	if err := installClientUpdate(ctx, client, serverURL, token, key, available, stderr, lang); err != nil {
		return err
	}
	event, err := json.Marshal(map[string]string{"old_version": Version, "new_version": available.Version, "os_arch": key})
	if err != nil {
		return clientUpdateWrap(lang, "encode_event", err)
	}
	if err := os.Setenv(pendingClientUpdateEvent, string(event)); err != nil {
		return clientUpdateWrap(lang, "prepare_event", err)
	}
	if err := updater.RestartSelf(); err != nil {
		_ = os.Unsetenv(pendingClientUpdateEvent)
		return clientUpdateWrap(lang, "restart", err)
	}
	return nil
}

func writePendingClientUpdateEvent(stderr io.Writer) error {
	data := os.Getenv(pendingClientUpdateEvent)
	if data == "" {
		return nil
	}
	if err := os.Unsetenv(pendingClientUpdateEvent); err != nil {
		return clientUpdateWrap(clientProcessLanguage, "clear_event", err)
	}
	fields := make(map[string]string)
	if err := json.Unmarshal([]byte(data), &fields); err != nil {
		return clientUpdateWrap(clientProcessLanguage, "decode_event", err)
	}
	return recordClientAudit(stderr, "cli.update_completed", fields, clientProcessLanguage)
}

func installClientUpdate(ctx context.Context, client *http.Client, serverURL, token, osArch string, release clientVersion, stderr io.Writer, selectedLanguage ...string) error {
	lang := "en"
	if len(selectedLanguage) > 0 && i18n.IsSupported(selectedLanguage[0]) {
		lang = selectedLanguage[0]
	}
	if len(release.SHA256) != sha256.Size*2 {
		return clientUpdateError(lang, "invalid_checksum", nil)
	}
	if _, err := hex.DecodeString(release.SHA256); err != nil {
		return clientUpdateError(lang, "invalid_checksum", nil)
	}
	parsed, err := normalizeBaseURL(serverURL, lang)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(parsed, "https://") {
		return clientUpdateError(lang, "https", nil)
	}
	url := parsed + "/cli/binaries/" + projectName + "-cli-" + osArch
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return clientUpdateWrap(lang, "create_binary_request", err)
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("Accept-Language", lang)
	request.Header.Set("User-Agent", projectName+"-cli/"+Version)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return clientUpdateWrap(lang, "download", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return clientUpdateError(lang, "download_http", map[string]string{"status": strconv.Itoa(response.StatusCode)})
	}
	tempDir, err := paths.CreateTempDir(internalName)
	if err != nil {
		return clientUpdateWrap(lang, "workspace", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	tempPath := filepath.Join(tempDir, "cli.update.tmp")
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return clientUpdateWrap(lang, "create_file", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxClientUpdateBytes+1))
	fileErr := file.Close()
	if copyErr != nil {
		return clientUpdateWrap(lang, "save", copyErr)
	}
	if fileErr != nil {
		return clientUpdateWrap(lang, "close_file", fileErr)
	}
	if written == 0 || written > maxClientUpdateBytes {
		return clientUpdateError(lang, "size", map[string]string{"size": strconv.FormatInt(written, 10)})
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, release.SHA256) {
		if err := recordClientAudit(stderr, "cli.update_checksum_invalid", map[string]string{"expected_version": release.Version, "os_arch": osArch}, lang); err != nil {
			return err
		}
		return clientUpdateError(lang, "checksum", nil)
	}
	currentPath, err := os.Executable()
	if err != nil {
		return clientUpdateWrap(lang, "binary_path", err)
	}
	currentPath, err = filepath.EvalSymlinks(currentPath)
	if err != nil {
		return clientUpdateWrap(lang, "install_path", err)
	}
	if err := replaceClientBinary(currentPath, tempPath, lang); err != nil {
		return clientUpdateError(lang, "permission", map[string]string{"path": currentPath, "message": err.Error()})
	}
	return nil
}

func askClientUpdate(input io.Reader, output io.Writer, version string, selectedLanguage ...string) (bool, error) {
	lang := "en"
	if len(selectedLanguage) > 0 && i18n.IsSupported(selectedLanguage[0]) {
		lang = selectedLanguage[0]
	}
	if _, err := fmt.Fprint(output, i18n.TranslateFormat(lang, "client.update.prompt", map[string]string{"version": version})); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && len(line) == 0 {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func recordClientAudit(stderr io.Writer, event string, fields map[string]string, selectedLanguage ...string) error {
	lang := "en"
	if len(selectedLanguage) > 0 && i18n.IsSupported(selectedLanguage[0]) {
		lang = selectedLanguage[0]
	}
	entry := map[string]interface{}{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "event": event, "fields": fields}
	line, err := json.Marshal(entry)
	if err != nil {
		return clientUpdateWrap(lang, "audit_encode", err)
	}
	logPath := filepath.Join(paths.GetLogsDir(internalName), "cli.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return clientUpdateWrap(lang, "audit_directory", err)
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return clientUpdateWrap(lang, "audit_open", err)
	}
	_, writeErr := fmt.Fprintln(file, string(line))
	closeErr := file.Close()
	if writeErr != nil {
		return clientUpdateWrap(lang, "audit_write", writeErr)
	}
	if closeErr != nil {
		return clientUpdateWrap(lang, "audit_close", closeErr)
	}
	if stderr == nil || stderr == io.Discard {
		return nil
	}
	if _, err := fmt.Fprintln(stderr, string(line)); err != nil {
		return clientUpdateWrap(lang, "audit_output", err)
	}
	return nil
}

func compareClientVersions(left, right string) int {
	parts := func(value string) []int {
		result := make([]int, 0, 3)
		for _, part := range strings.Split(strings.TrimPrefix(strings.TrimSpace(value), "v"), ".") {
			var number int
			for _, r := range part {
				if r < '0' || r > '9' {
					break
				}
				number = number*10 + int(r-'0')
			}
			result = append(result, number)
		}
		return result
	}
	a, b := parts(left), parts(right)
	for index := 0; index < 3; index++ {
		var av, bv int
		if index < len(a) {
			av = a[index]
		}
		if index < len(b) {
			bv = b[index]
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	return 0
}
