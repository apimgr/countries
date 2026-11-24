// Package main implements the countries-cli client.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/common/display"
	"github.com/apimgr/countries/src/common/i18n"
	"gopkg.in/yaml.v3"
)

var (
	Version      = "dev"
	CommitID     = "unknown"
	BuildDate    = "N/A"
	BuildEpoch   = "0"
	OfficialSite = ""
)

const projectName = "countries"
const internalOrg = "apimgr"
const internalName = "countries"

const maxResponseBytes = 16 << 20
const maxRequestBodyBytes = 1 << 20

var clientUICommand bool
var clientProcessLanguage = "en"

type clientExitError struct {
	code int
	err  error
}

func (e clientExitError) Error() string { return e.err.Error() }
func (e clientExitError) Unwrap() error { return e.err }

func withClientExitCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return clientExitError{code: code, err: err}
}

func clientExitCode(err error) int {
	var coded clientExitError
	if errors.As(err, &coded) {
		return coded.code
	}
	return 1
}

type cliConfig struct {
	Server struct {
		Primary string `yaml:"primary"`
		Token   string `yaml:"token"`
	} `yaml:"server"`
	Lang    string `yaml:"lang"`
	Display struct {
		Mode string `yaml:"mode"`
	} `yaml:"display"`
	TUI struct {
		Theme string `yaml:"theme"`
	} `yaml:"tui"`
	Update struct {
		Auto    bool   `yaml:"auto"`
		Channel string `yaml:"channel"`
	} `yaml:"update"`
	Defaults struct {
		Output string `yaml:"output"`
	} `yaml:"defaults"`
}

type country struct {
	Name struct {
		Common   string `json:"common"`
		Official string `json:"official"`
	} `json:"name"`
	CCA2      string    `json:"cca2"`
	CCA3      string    `json:"cca3"`
	Capital   []string  `json:"capital"`
	Region    string    `json:"region"`
	Timezones []string  `json:"timezones"`
	LatLng    []float64 `json:"latlng"`
}

type apiEnvelope struct {
	Success    bool            `json:"success"`
	OK         bool            `json:"ok"`
	Data       json.RawMessage `json:"data"`
	Error      string          `json:"error"`
	Message    string          `json:"message"`
	Count      int             `json:"count"`
	Total      int             `json:"total"`
	Pagination struct {
		Total int `json:"total"`
	} `json:"pagination"`
}

type options struct {
	server    string
	token     string
	tokenFile string
	config    string
	lang      string
	shell     string
	output    string
	color     string
	update    string
	debug     bool
	version   bool
	help      bool
}

func clientText(lang, key string) string {
	return i18n.Translate(lang, "client."+key)
}

func clientTextFormat(lang, key string, args map[string]string) string {
	return i18n.TranslateFormat(lang, "client."+key, args)
}

func clientWrap(key string, err error) error {
	return fmt.Errorf("%s: %w", clientText(clientProcessLanguage, key), err)
}

func localizedFlagError(lang string, err error) error {
	message := err.Error()
	if strings.HasPrefix(message, "flag provided but not defined: ") {
		flag := strings.TrimPrefix(message, "flag provided but not defined: ")
		return errors.New(clientTextFormat(lang, "flag.unknown", map[string]string{"flag": flag}))
	}
	if strings.HasPrefix(message, "flag needs an argument: ") {
		flag := strings.TrimPrefix(message, "flag needs an argument: ")
		return errors.New(clientTextFormat(lang, "flag.needs_argument", map[string]string{"flag": flag}))
	}
	if strings.HasPrefix(message, "invalid value ") {
		separator := strings.Index(message, " for flag ")
		if separator > 0 {
			value := strings.Trim(strings.TrimPrefix(message[:separator], "invalid value "), "\"'")
			rest := strings.TrimPrefix(message[separator+len(" for flag "):], "-")
			flag := rest
			if end := strings.Index(flag, ":"); end >= 0 {
				flag = flag[:end]
			}
			return errors.New(clientTextFormat(lang, "flag.invalid_value", map[string]string{"flag": "--" + flag, "value": value}))
		}
	}
	return errors.New(clientText(lang, "flag.parse_error"))
}

func main() {
	clientProcessLanguage = selectLanguage("", "")
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, clientTextFormat(clientProcessLanguage, "error.prefix", map[string]string{"message": err.Error()}))
		os.Exit(clientExitCode(err))
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet(projectName+"-cli", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var opts options
	fs.StringVar(&opts.server, "server", "", "Server base URL")
	fs.StringVar(&opts.token, "token", "", "API token for authenticated operations")
	fs.StringVar(&opts.tokenFile, "token-file", "", "Read API token from file")
	fs.StringVar(&opts.config, "config", "", "Config profile or YAML file")
	fs.StringVar(&opts.lang, "lang", "", "Language for output")
	fs.StringVar(&opts.shell, "shell", "", "Shell integration: completions, init, or help")
	fs.StringVar(&opts.output, "output", "", "Output format: table, plain, json")
	fs.StringVar(&opts.color, "color", "auto", "Color output: auto, yes, no")
	fs.StringVar(&opts.update, "update", "", "Check updates or update the CLI: check, yes")
	fs.BoolVar(&opts.debug, "debug", false, "Print request diagnostics to stderr")
	fs.BoolVar(&opts.version, "version", false, "Print version information")
	fs.BoolVar(&opts.help, "help", false, "Show help")
	fs.BoolVar(&opts.help, "h", false, "Show help")
	var shortVersion bool
	fs.BoolVar(&shortVersion, "v", false, "Print version information")
	if err := fs.Parse(args); err != nil {
		lang := selectLanguage(opts.lang, "")
		clientProcessLanguage = lang
		return withClientExitCode(64, localizedFlagError(lang, err))
	}
	clientProcessLanguage = selectLanguage(opts.lang, "")
	configPath, err := resolveConfigPath(opts.config)
	if err != nil {
		return withClientExitCode(2, err)
	}
	cfg, err := loadConfigAt(configPath)
	if err != nil {
		return withClientExitCode(2, err)
	}
	lang := selectLanguage(opts.lang, cfg.Lang)
	clientProcessLanguage = lang
	cfg.Lang = lang
	if err := writePendingClientUpdateEvent(stderr); err != nil {
		return err
	}
	if opts.help {
		return printHelpLanguage(stdout, lang)
	}
	if opts.version || shortVersion {
		return printVersionLanguage(stdout, lang)
	}
	if opts.shell != "" {
		shellArgs := append([]string{"--shell", opts.shell}, fs.Args()...)
		_, err := shellCommand(shellArgs, stdout, lang)
		return err
	}
	if err := validateColor(opts.color, lang); err != nil {
		return withClientExitCode(64, err)
	}
	baseURL := strings.TrimSpace(opts.server)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("COUNTRIES_SERVER_PRIMARY"))
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(cfg.Server.Primary)
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(OfficialSite)
	}
	pos := fs.Args()
	mode, modeErr := clientDisplayMode(cfg.Display.Mode, len(pos) == 0 && opts.update == "", stdout, lang)
	if modeErr != nil {
		return withClientExitCode(2, modeErr)
	}
	interactive := mode == "gui" || mode == "tui"
	interactiveUpdate := interactiveTerminal(stdout) || mode == "tui"
	if baseURL == "" && interactive {
		return withClientExitCode(2, runInteractive(baseURL, &cfg, configPath, os.Stdin, stdout, stderr))
	}
	if baseURL == "" {
		return withClientExitCode(2, errors.New(clientText(lang, "error.server_required")))
	}
	baseURL, err = normalizeBaseURL(baseURL, lang)
	if err != nil {
		return withClientExitCode(2, err)
	}

	format := opts.output
	if format == "" {
		format = cfg.Defaults.Output
	}
	if format == "" {
		format = "table"
	}
	if format != "table" && format != "plain" && format != "json" {
		return withClientExitCode(64, errors.New(clientTextFormat(lang, "error.output_invalid", map[string]string{"format": format})))
	}
	token := strings.TrimSpace(opts.token)
	if token == "" && opts.tokenFile != "" {
		data, readErr := os.ReadFile(opts.tokenFile)
		if readErr != nil {
			return withClientExitCode(2, clientWrap("error.read_token_file", readErr))
		}
		token = strings.TrimSpace(string(data))
	}
	if token == "" {
		token = cfg.Server.Token
	}
	if opts.update != "" && opts.update != "check" && opts.update != "yes" {
		return withClientExitCode(64, errors.New(clientText(lang, "error.update_invalid")))
	}
	if !clientUICommand {
		if err := checkAndApplyClientUpdate(baseURL, token, opts.update, cfg.Update.Auto, interactiveUpdate, os.Stdin, stdout, stderr, lang); err != nil {
			if opts.update != "" || strings.Contains(err.Error(), "this CLI is too old") {
				return err
			}
			if _, writeErr := fmt.Fprintln(stderr, clientTextFormat(lang, "error.update_warning", map[string]string{"message": err.Error()})); writeErr != nil {
				return writeErr
			}
		}
	}
	if opts.update == "check" || opts.update == "yes" && len(pos) == 0 {
		return nil
	}
	if interactive {
		return withClientExitCode(2, runInteractive(baseURL, &cfg, configPath, os.Stdin, stdout, stderr))
	}

	if len(pos) == 0 {
		return withClientExitCode(64, errors.New(clientTextFormat(lang, "error.command_required", map[string]string{"binary": filepath.Base(os.Args[0])})))
	}
	command := strings.ToLower(pos[0])
	args = pos[1:]
	if !contains(clientCommands, command) {
		command = "search"
		args = pos
	}
	client := &http.Client{Timeout: 15 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fetch := func(path string, target interface{}) (result apiEnvelope, retErr error) {
		endpoint := baseURL + path
		if opts.debug {
			if _, err := fmt.Fprintf(stderr, "GET %s\n", endpoint); err != nil {
				return apiEnvelope{}, clientWrap("error.write_request_diagnostics", err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return apiEnvelope{}, clientWrap("error.create_request", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Accept-Language", lang)
		req.Header.Set("User-Agent", projectName+"-cli/"+Version)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return apiEnvelope{}, withClientExitCode(3, clientWrap("error.request_failed", err))
		}
		defer func() {
			if err := resp.Body.Close(); err != nil && retErr == nil {
				retErr = clientWrap("error.close_server_response", err)
			}
		}()
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		if err != nil {
			return apiEnvelope{}, clientWrap("error.read_response", err)
		}
		if len(body) > maxResponseBytes {
			return apiEnvelope{}, errors.New(clientText(lang, "error.response_too_large"))
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var envelope apiEnvelope
			code := 1
			switch resp.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				code = 4
			case http.StatusNotFound:
				code = 5
			}
			if json.Unmarshal(body, &envelope) == nil && envelope.Message != "" {
				return apiEnvelope{}, withClientExitCode(code, errors.New(clientTextFormat(lang, "error.server_http_message", map[string]string{"status": strconv.Itoa(resp.StatusCode), "message": envelope.Message})))
			}
			return apiEnvelope{}, withClientExitCode(code, errors.New(clientTextFormat(lang, "error.server_http", map[string]string{"status": strconv.Itoa(resp.StatusCode)})))
		}
		var envelope apiEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			return apiEnvelope{}, clientWrap("error.decode_server_response", err)
		}
		if !envelope.OK && !envelope.Success && envelope.Error != "" {
			return apiEnvelope{}, fmt.Errorf("%s: %s", envelope.Error, envelope.Message)
		}
		if target != nil && len(envelope.Data) > 0 {
			if err := json.Unmarshal(envelope.Data, target); err != nil {
				return apiEnvelope{}, clientWrap("error.decode_response_data", err)
			}
		}
		return envelope, nil
	}
	if command == "request" {
		if len(args) < 2 {
			return withClientExitCode(64, errors.New(clientText(lang, "usage.request")))
		}
		body := ""
		if len(args) > 2 {
			body = strings.Join(args[2:], " ")
		}
		responseBody, err := executeClientAPIRequest(baseURL, token, args[0], args[1], body, lang)
		if err != nil {
			return err
		}
		if len(responseBody) > 0 {
			if _, err := stdout.Write(responseBody); err != nil {
				return err
			}
			if responseBody[len(responseBody)-1] != '\n' {
				_, err := io.WriteString(stdout, "\n")
				return err
			}
		}
		return nil
	}

	switch command {
	case "list":
		items := make([]country, 0)
		for page := 1; ; page++ {
			var current []country
			envelope, err := fetch(fmt.Sprintf("%s?page=%d&limit=250", api.APIPath("countries"), page), &current)
			if err != nil {
				return err
			}
			items = append(items, current...)
			if len(current) == 0 || envelope.Pagination.Total == 0 || len(items) >= envelope.Pagination.Total {
				break
			}
		}
		return printCountriesLanguage(stdout, items, format, colorEnabled(opts.color, stdout), lang)
	case "get":
		if len(args) != 1 {
			return withClientExitCode(64, errors.New(clientText(lang, "usage.get")))
		}
		var item country
		_, err := fetch(api.APIPath("countries/")+url.PathEscape(strings.ToUpper(args[0])), &item)
		if err != nil {
			return err
		}
		return printCountriesLanguage(stdout, []country{item}, format, colorEnabled(opts.color, stdout), lang)
	case "search":
		if len(args) == 0 {
			return withClientExitCode(64, errors.New(clientText(lang, "usage.search")))
		}
		query := url.QueryEscape(strings.Join(args, " "))
		var items []country
		_, err := fetch(api.APIPath("countries/search?q="+query), &items)
		if err != nil {
			return err
		}
		return printCountriesLanguage(stdout, items, format, colorEnabled(opts.color, stdout), lang)
	case "nearest":
		if len(args) != 2 {
			return withClientExitCode(64, errors.New(clientText(lang, "usage.nearest")))
		}
		lat, err := parseCoordinate(args[0], -90, 90, lang)
		if err != nil {
			return withClientExitCode(64, errors.New(clientTextFormat(lang, "error.field", map[string]string{"field": clientText(lang, "field.latitude"), "message": err.Error()})))
		}
		lon, err := parseCoordinate(args[1], -180, 180, lang)
		if err != nil {
			return withClientExitCode(64, errors.New(clientTextFormat(lang, "error.field", map[string]string{"field": clientText(lang, "field.longitude"), "message": err.Error()})))
		}
		var result map[string]interface{}
		_, err = fetch(fmt.Sprintf("%s?lat=%s&lon=%s", api.APIPath("coordinates"), url.QueryEscape(lat), url.QueryEscape(lon)), &result)
		if err != nil {
			return err
		}
		return printJSON(stdout, result, format)
	case "nearby":
		if len(args) != 3 {
			return withClientExitCode(64, errors.New(clientText(lang, "usage.nearby")))
		}
		lat, err := parseCoordinate(args[0], -90, 90, lang)
		if err != nil {
			return withClientExitCode(64, errors.New(clientTextFormat(lang, "error.field", map[string]string{"field": clientText(lang, "field.latitude"), "message": err.Error()})))
		}
		lon, err := parseCoordinate(args[1], -180, 180, lang)
		if err != nil {
			return withClientExitCode(64, errors.New(clientTextFormat(lang, "error.field", map[string]string{"field": clientText(lang, "field.longitude"), "message": err.Error()})))
		}
		radius, err := parseCoordinate(args[2], 0, 20038, lang)
		if err != nil {
			return withClientExitCode(64, errors.New(clientTextFormat(lang, "error.field", map[string]string{"field": clientText(lang, "field.radius"), "message": err.Error()})))
		}
		var result interface{}
		_, err = fetch(fmt.Sprintf("%s?lat=%s&lon=%s&radius=%s", api.APIPath("nearby"), url.QueryEscape(lat), url.QueryEscape(lon), url.QueryEscape(radius)), &result)
		if err != nil {
			return err
		}
		return printJSON(stdout, result, format)
	case "stats":
		var result interface{}
		_, err := fetch(api.APIPath("stats"), &result)
		if err != nil {
			return err
		}
		return printJSON(stdout, result, format)
	default:
		return withClientExitCode(64, errors.New(clientTextFormat(lang, "error.command_unknown", map[string]string{"command": command, "binary": filepath.Base(os.Args[0])})))
	}
}

func executeClientAPIRequest(serverURL, token, method, target, body, lang string) ([]byte, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, errors.New(clientTextFormat(lang, "request.method_invalid", map[string]string{"method": method}))
	}
	parsed, err := url.ParseRequestURI(target)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, api.APIBasePath()+"/") || path.Clean(parsed.Path) != parsed.Path {
		return nil, errors.New(clientText(lang, "request.path_invalid"))
	}
	for _, excluded := range []string{"/server/mcp", "/server/token/check", "/server/tokens/check", "/server/preferences/tokens/check", "/autodiscover", "/cli/binaries"} {
		if parsed.Path == api.APIPath(excluded) || strings.HasPrefix(parsed.Path, api.APIPath(excluded)+"/") {
			return nil, errors.New(clientText(lang, "request.path_excluded"))
		}
	}
	if len(body) > maxRequestBodyBytes {
		return nil, errors.New(clientText(lang, "request.body_too_large"))
	}
	var payload io.Reader
	if body != "" {
		if !json.Valid([]byte(body)) {
			return nil, errors.New(clientText(lang, "request.body_invalid"))
		}
		payload = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, serverURL+parsed.RequestURI(), payload)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", clientText(lang, "request.create_failed"), err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", lang)
	req.Header.Set("User-Agent", projectName+"-cli/"+Version)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", clientText(lang, "request.failed"), err)
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	closeErr := resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", clientText(lang, "request.response_read_failed"), errors.Join(err, closeErr))
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%s: %w", clientText(lang, "request.response_read_failed"), closeErr)
	}
	if len(responseBody) > maxResponseBytes {
		return nil, errors.New(clientText(lang, "error.response_too_large"))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := ""
		var envelope apiEnvelope
		if json.Unmarshal(responseBody, &envelope) == nil {
			message = envelope.Message
		}
		if message != "" {
			return nil, errors.New(clientTextFormat(lang, "error.server_http_message", map[string]string{"status": strconv.Itoa(resp.StatusCode), "message": message}))
		}
		return nil, errors.New(clientTextFormat(lang, "error.server_http", map[string]string{"status": strconv.Itoa(resp.StatusCode)}))
	}
	return responseBody, nil
}

func normalizeBaseURL(raw string, selectedLanguage ...string) (string, error) {
	lang := "en"
	if len(selectedLanguage) > 0 && i18n.IsSupported(selectedLanguage[0]) {
		lang = selectedLanguage[0]
	}
	if strings.Contains(raw, "#") {
		return "", errors.New(clientText(lang, "error.url_invalid"))
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return "", fmt.Errorf("%s: %w", clientText(lang, "error.url_invalid"), err)
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New(clientText(lang, "error.url_invalid"))
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func loadConfig() (cliConfig, error) {
	path, err := resolveConfigPath("")
	if err != nil {
		return cliConfig{}, err
	}
	return loadConfigAt(path)
}

func loadConfigAt(path string) (cliConfig, error) {
	var cfg cliConfig
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, clientWrap("error.config_read", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, clientWrap("error.config_parse", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return cfg, clientWrap("error.config_secure", err)
	}
	return cfg, nil
}

func saveConfig(cfg cliConfig) error {
	path, err := resolveConfigPath("")
	if err != nil {
		return err
	}
	return saveConfigAt(cfg, path)
}

func saveConfigAt(cfg cliConfig, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return clientWrap("error.config_directory_create", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return clientWrap("error.config_encode", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return clientWrap("error.config_write", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return clientWrap("error.config_secure", err)
	}
	return nil
}

func resolveConfigPath(configFlag string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", clientWrap("error.config_directory_resolve", err)
	}
	if configFlag == "" {
		return filepath.Join(configDir, internalOrg, internalName, "cli.yml"), nil
	}
	if strings.HasPrefix(configFlag, "~/") {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", clientWrap("error.home_directory_resolve", homeErr)
		}
		configFlag = filepath.Join(home, configFlag[2:])
	}
	path := configFlag
	if !filepath.IsAbs(path) {
		path = filepath.Join(configDir, internalOrg, internalName, path)
	}
	if filepath.Ext(path) == "" {
		if _, statErr := os.Stat(path + ".yml"); statErr == nil {
			path += ".yml"
		} else if _, statErr := os.Stat(path + ".yaml"); statErr == nil {
			path += ".yaml"
		} else {
			path += ".yml"
		}
	}
	return filepath.Clean(path), nil
}

func interactiveTerminal(output io.Writer) bool {
	inputInfo, inputErr := os.Stdin.Stat()
	outputFile, outputIsFile := output.(*os.File)
	if inputErr != nil || !outputIsFile || outputFile != os.Stdout {
		return false
	}
	outputInfo, outputErr := outputFile.Stat()
	return outputErr == nil && inputInfo.Mode()&os.ModeCharDevice != 0 && outputInfo.Mode()&os.ModeCharDevice != 0
}

func runInteractive(serverURL string, cfg *cliConfig, configPath string, input io.Reader, output, stderr io.Writer) error {
	_ = input
	mode, err := clientDisplayMode(cfg.Display.Mode, true, output)
	if err != nil {
		return err
	}
	if mode == "gui" {
		return launchClientGUI(serverURL, cfg, configPath)
	}
	return runClientTUI(serverURL, cfg, configPath, output, stderr)
}

func clientDisplayMode(configured string, noCommand bool, output io.Writer, selectedLanguage ...string) (string, error) {
	lang := "en"
	if len(selectedLanguage) > 0 && i18n.IsSupported(selectedLanguage[0]) {
		lang = selectedLanguage[0]
	}
	if !noCommand {
		return "cli", nil
	}
	env := display.DetectDisplayEnv()
	mode := strings.ToLower(strings.TrimSpace(configured))
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto":
		if env.IsAutoDetectDisplayModeGUI() && clientGUIAvailable() {
			return "gui", nil
		}
		if env.IsTerminal && !env.IsDumbTerminal() {
			return "tui", nil
		}
		if env.IsAutoDetectDisplayModeGUI() && !clientGUIAvailable() && (runtime.GOOS == "freebsd" || runtime.GOOS == "netbsd" || runtime.GOOS == "openbsd") {
			return "cli", nil
		}
		if env.IsAutoDetectDisplayModeCLI() {
			return "cli", nil
		}
		return "cli", nil
	case "gui":
		if !env.HasDisplay || env.IsRemoteSession() || !clientGUIAvailable() {
			return "", errors.New(clientText(lang, "display.gui_unavailable"))
		}
		return "gui", nil
	case "tui":
		if !env.IsTerminal || env.IsDumbTerminal() {
			return "", errors.New(clientText(lang, "display.tui_unavailable"))
		}
		return "tui", nil
	default:
		return "", errors.New(clientTextFormat(lang, "display.invalid", map[string]string{"mode": configured}))
	}
}

func checkServer(serverURL, token string, selectedLanguage ...string) (retErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lang := "en"
	if len(selectedLanguage) > 0 && i18n.IsSupported(selectedLanguage[0]) {
		lang = selectedLanguage[0]
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+api.APIPath("server/healthz"), nil)
	if err != nil {
		return fmt.Errorf("%s: %w", clientText(lang, "error.health_request_create"), err)
	}
	req.Header.Set("Accept-Language", lang)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", clientText(lang, "error.health_request_failed"), err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("%s: %w", clientText(lang, "error.health_response_close"), err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return errors.New(clientTextFormat(lang, "error.health_http", map[string]string{"status": strconv.Itoa(resp.StatusCode)}))
	}
	return nil
}

func validateColor(value string, selectedLanguage ...string) error {
	switch value {
	case "auto", "yes", "no":
		return nil
	default:
		lang := "en"
		if len(selectedLanguage) > 0 && i18n.IsSupported(selectedLanguage[0]) {
			lang = selectedLanguage[0]
		}
		return errors.New(clientTextFormat(lang, "error.color_invalid", map[string]string{"value": value}))
	}
}

func parseCoordinate(value string, min, max float64, selectedLanguage ...string) (string, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < min || parsed > max {
		lang := "en"
		if len(selectedLanguage) > 0 && i18n.IsSupported(selectedLanguage[0]) {
			lang = selectedLanguage[0]
		}
		return "", errors.New(clientTextFormat(lang, "error.coordinate", map[string]string{"min": strconv.FormatFloat(min, 'f', -1, 64), "max": strconv.FormatFloat(max, 'f', -1, 64)}))
	}
	return strconv.FormatFloat(parsed, 'f', -1, 64), nil
}

func colorEnabled(mode string, output io.Writer) bool {
	if mode == "no" || os.Getenv("NO_COLOR") != "" {
		return false
	}
	if mode == "yes" {
		return true
	}
	file, ok := output.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func printCountries(w io.Writer, items []country, format string, color bool) error {
	return printCountriesLanguage(w, items, format, color, "en")
}

func printCountriesLanguage(w io.Writer, items []country, format string, color bool, lang string) error {
	if format == "json" {
		return printJSON(w, items, format)
	}
	if format == "plain" {
		for _, item := range items {
			if _, err := fmt.Fprintf(w, "%s (%s) — %s\n", item.Name.Common, item.CCA2, strings.Join(item.Capital, ", ")); err != nil {
				return err
			}
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	header := strings.Join([]string{clientText(lang, "table.code"), clientText(lang, "table.country"), clientText(lang, "table.official_name"), clientText(lang, "table.capital"), clientText(lang, "table.region"), clientText(lang, "table.timezones")}, "\t")
	if color {
		header = "\x1b[1;36m" + header + "\x1b[0m"
	}
	if _, err := fmt.Fprintln(tw, header); err != nil {
		return err
	}
	for _, item := range items {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", item.CCA2, item.Name.Common, item.Name.Official, strings.Join(item.Capital, ", "), item.Region, strings.Join(item.Timezones, ", ")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func printJSON(w io.Writer, value interface{}, _ string) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printHelp(w io.Writer) error {
	return printHelpLanguage(w, "en")
}

func printHelpLanguage(w io.Writer, lang string) error {
	name := filepath.Base(os.Args[0])
	lines := []string{
		clientText(lang, "help.title"),
		"",
		clientText(lang, "help.usage"),
		"  " + name + " [flags] COMMAND [arguments]",
		"  " + name + " [flags] QUERY",
		"",
		clientText(lang, "help.commands"),
		"  list                         " + clientText(lang, "command.list"),
		"  get CODE                     " + clientText(lang, "command.get"),
		"  search QUERY                 " + clientText(lang, "command.search"),
		"  nearest LAT LON              " + clientText(lang, "command.nearest"),
		"  nearby LAT LON RADIUS_KM     " + clientText(lang, "command.nearby"),
		"  stats                        " + clientText(lang, "command.stats"),
		"  request METHOD /api/v1/PATH [JSON]  " + clientText(lang, "command.request"),
		"",
		clientText(lang, "help.flags"),
		"  --server URL                 " + clientText(lang, "flag.server"),
		"  --config PATH                " + clientText(lang, "flag.config"),
		"  --token TOKEN                " + clientText(lang, "flag.token"),
		"  --token-file FILE            " + clientText(lang, "flag.token_file"),
		"  --lang CODE                  " + clientText(lang, "flag.lang"),
		"  --output FORMAT              " + clientText(lang, "flag.output"),
		"  --color MODE                 " + clientText(lang, "flag.color"),
		"  --update ACTION              " + clientText(lang, "flag.update"),
		"  --debug                      " + clientText(lang, "flag.debug"),
		"  --shell completions [SHELL]  " + clientText(lang, "flag.shell_completions"),
		"  --shell init [SHELL]         " + clientText(lang, "flag.shell_init"),
		"  -h, --help                   " + clientText(lang, "flag.help"),
		"  -v, --version                " + clientText(lang, "flag.version"),
		"  " + clientText(lang, "help.language_count"),
		"",
		clientText(lang, "help.config_hint"),
	}
	_, err := io.WriteString(w, strings.Join(lines, "\n")+"\n")
	return err
}

func printVersion(w io.Writer) error {
	return printVersionLanguage(w, "en")
}

func printVersionLanguage(w io.Writer, lang string) error {
	built := BuildDate
	if epoch, err := strconv.ParseInt(BuildEpoch, 10, 64); err == nil && epoch > 0 {
		built = time.Unix(epoch, 0).UTC().Format(time.RFC3339)
	}
	lines := []string{
		fmt.Sprintf("%s %s (%s)", filepath.Base(os.Args[0]), Version, CommitID),
		clientTextFormat(lang, "version.built", map[string]string{"date": built}),
		clientTextFormat(lang, "version.go", map[string]string{"version": runtime.Version()}),
		clientTextFormat(lang, "version.os_arch", map[string]string{"os": runtime.GOOS, "arch": runtime.GOARCH}),
	}
	_, err := io.WriteString(w, strings.Join(lines, "\n")+"\n")
	return err
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func clientCommandFormScreen(command string) string {
	if command == "request" {
		return "request"
	}
	return "arguments"
}

// selectLanguage picks the first supported language from the flag, config, then the LANG environment, falling back to English.
func selectLanguage(flagValue, configValue string) string {
	envValue := os.Getenv("LC_ALL")
	if envValue == "" {
		envValue = os.Getenv("LANG")
	}
	envValue = strings.SplitN(envValue, ".", 2)[0]
	envValue = strings.SplitN(envValue, "_", 2)[0]
	for _, candidate := range []string{flagValue, configValue, envValue} {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "auto" {
			continue
		}
		if candidate != "" {
			if i18n.IsSupported(candidate) {
				return candidate
			}
			return "en"
		}
	}
	return "en"
}
