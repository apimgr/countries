// Package main implements the countries-cli client.
package main

import (
	"bufio"
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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/apimgr/countries/src/common/api"
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

type cliConfig struct {
	Server struct {
		Primary string `yaml:"primary"`
		Token   string `yaml:"token"`
	} `yaml:"server"`
	Lang string `yaml:"lang"`
	Display struct {
		Mode string `yaml:"mode"`
	} `yaml:"display"`
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
	server  string
	token   string
	config  string
	lang    string
	output  string
	color   string
	debug   bool
	version bool
	help    bool
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet(projectName+"-cli", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opts options
	fs.StringVar(&opts.server, "server", "", "Server base URL")
	fs.StringVar(&opts.token, "token", "", "API token for authenticated operations")
	fs.StringVar(&opts.config, "config", "", "Config profile or YAML file")
	fs.StringVar(&opts.lang, "lang", "", "Language for output")
	fs.StringVar(&opts.output, "output", "", "Output format: table, plain, json")
	fs.StringVar(&opts.color, "color", "auto", "Color output: auto, yes, no")
	fs.BoolVar(&opts.debug, "debug", false, "Print request diagnostics to stderr")
	fs.BoolVar(&opts.version, "version", false, "Print version information")
	fs.BoolVar(&opts.help, "help", false, "Show help")
	fs.BoolVar(&opts.help, "h", false, "Show help")
	var shortVersion bool
	fs.BoolVar(&shortVersion, "v", false, "Print version information")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.help {
		printHelp(stdout)
		return nil
	}
	if opts.version || shortVersion {
		printVersion(stdout)
		return nil
	}
	if err := validateColor(opts.color); err != nil {
		return err
	}

	configPath, err := resolveConfigPath(opts.config)
	if err != nil {
		return err
	}
	cfg, err := loadConfigAt(configPath)
	if err != nil {
		return err
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
	if len(fs.Args()) == 0 && interactiveTerminal(stdout) {
		return runInteractive(baseURL, &cfg, configPath, os.Stdin, stdout, stderr)
	}
	if baseURL == "" {
		return errors.New("server URL is required; set --server, COUNTRIES_SERVER_PRIMARY, or server.primary in cli.yml")
	}
	baseURL, err = normalizeBaseURL(baseURL)
	if err != nil {
		return err
	}

	format := opts.output
	if format == "" {
		format = cfg.Defaults.Output
	}
	if format == "" {
		format = "table"
	}
	if format != "table" && format != "plain" && format != "json" {
		return fmt.Errorf("unsupported output format %q; choose table, plain, or json", format)
	}
	lang := selectLanguage(opts.lang, cfg.Lang)
	_ = lang
	token := strings.TrimSpace(opts.token)
	if token == "" {
		token = cfg.Server.Token
	}

	pos := fs.Args()
	if len(pos) == 0 {
		return errors.New("command required; run countries-cli --help for usage")
	}
	command := strings.ToLower(pos[0])
	args = pos[1:]
	client := &http.Client{Timeout: 15 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fetch := func(path string, target interface{}) (apiEnvelope, error) {
		endpoint := baseURL + path
		if opts.debug {
			fmt.Fprintf(stderr, "GET %s\n", endpoint)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return apiEnvelope{}, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", projectName+"-cli/"+Version)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return apiEnvelope{}, fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		if err != nil {
			return apiEnvelope{}, fmt.Errorf("read response: %w", err)
		}
		if len(body) > maxResponseBytes {
			return apiEnvelope{}, errors.New("server response exceeds the 16 MiB client limit")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var envelope apiEnvelope
			if json.Unmarshal(body, &envelope) == nil && envelope.Message != "" {
				return apiEnvelope{}, fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, envelope.Message)
			}
			return apiEnvelope{}, fmt.Errorf("server returned HTTP %d", resp.StatusCode)
		}
		var envelope apiEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			return apiEnvelope{}, fmt.Errorf("decode server response: %w", err)
		}
		if !envelope.OK && !envelope.Success && envelope.Error != "" {
			return apiEnvelope{}, fmt.Errorf("%s: %s", envelope.Error, envelope.Message)
		}
		if target != nil && len(envelope.Data) > 0 {
			if err := json.Unmarshal(envelope.Data, target); err != nil {
				return apiEnvelope{}, fmt.Errorf("decode response data: %w", err)
			}
		}
		return envelope, nil
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
		return printCountries(stdout, items, format, colorEnabled(opts.color, stdout))
	case "get":
		if len(args) != 1 {
			return errors.New("usage: countries-cli get CODE")
		}
		var item country
		_, err := fetch(api.APIPath("countries/")+url.PathEscape(strings.ToUpper(args[0])), &item)
		if err != nil {
			return err
		}
		return printCountries(stdout, []country{item}, format, colorEnabled(opts.color, stdout))
	case "search":
		if len(args) == 0 {
			return errors.New("usage: countries-cli search QUERY")
		}
		query := url.QueryEscape(strings.Join(args, " "))
		var items []country
		_, err := fetch(api.APIPath("countries/search?q="+query), &items)
		if err != nil {
			return err
		}
		return printCountries(stdout, items, format, colorEnabled(opts.color, stdout))
	case "nearest":
		if len(args) != 2 {
			return errors.New("usage: countries-cli nearest LATITUDE LONGITUDE")
		}
		lat, err := parseCoordinate(args[0], -90, 90)
		if err != nil {
			return fmt.Errorf("latitude: %w", err)
		}
		lon, err := parseCoordinate(args[1], -180, 180)
		if err != nil {
			return fmt.Errorf("longitude: %w", err)
		}
		var result map[string]interface{}
		_, err = fetch(fmt.Sprintf("%s?lat=%s&lon=%s", api.APIPath("coordinates"), url.QueryEscape(lat), url.QueryEscape(lon)), &result)
		if err != nil {
			return err
		}
		return printJSON(stdout, result, format)
	case "nearby":
		if len(args) != 3 {
			return errors.New("usage: countries-cli nearby LATITUDE LONGITUDE RADIUS_KM")
		}
		lat, err := parseCoordinate(args[0], -90, 90)
		if err != nil {
			return fmt.Errorf("latitude: %w", err)
		}
		lon, err := parseCoordinate(args[1], -180, 180)
		if err != nil {
			return fmt.Errorf("longitude: %w", err)
		}
		radius, err := parseCoordinate(args[2], 0, 20038)
		if err != nil {
			return fmt.Errorf("radius: %w", err)
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
		return fmt.Errorf("unknown command %q; run countries-cli --help for usage", command)
	}
}

func normalizeBaseURL(raw string) (string, error) {
	if strings.Contains(raw, "#") {
		return "", errors.New("server URL must be an http or https URL without credentials, query, or fragment")
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return "", fmt.Errorf("invalid server URL: %w", err)
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("server URL must be an http or https URL without credentials, query, or fragment")
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
		return cfg, fmt.Errorf("read CLI configuration: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse CLI configuration: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return cfg, fmt.Errorf("secure CLI configuration: %w", err)
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
		return fmt.Errorf("create CLI configuration directory: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode CLI configuration: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write CLI configuration: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("secure CLI configuration: %w", err)
	}
	return nil
}

func resolveConfigPath(configFlag string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	if configFlag == "" {
		return filepath.Join(configDir, internalOrg, internalName, "cli.yml"), nil
	}
	if strings.HasPrefix(configFlag, "~/") {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", fmt.Errorf("resolve home directory: %w", homeErr)
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
	reader := bufio.NewReader(input)
	if serverURL == "" {
		if _, err := fmt.Fprintln(output, "Countries CLI setup"); err != nil {
			return err
		}
		serverURL = strings.TrimSpace(readPrompt(reader, output, "Server URL: "))
		if serverURL == "\x00" {
			return nil
		}
		serverURL, err := normalizeBaseURL(serverURL)
		if err != nil {
			return err
		}
		token := strings.TrimSpace(readPrompt(reader, output, "API token (optional): "))
		if err := checkServer(serverURL, token); err != nil {
			return fmt.Errorf("server connection failed: %w", err)
		}
		cfg.Server.Primary = serverURL
		cfg.Server.Token = token
		if err := saveConfigAt(*cfg, configPath); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(output, "Configuration saved."); err != nil {
			return err
		}
	}
	for {
		if _, err := fmt.Fprintln(output, "\nCountries CLI\n1) List countries\n2) Get by code\n3) Search\n4) Nearest country\n5) Nearby countries\n6) Statistics\n7) Quit"); err != nil {
			return err
		}
		choice := strings.TrimSpace(readPrompt(reader, output, "Choose an action: "))
		if choice == "\x00" {
			return nil
		}
		var command []string
		switch choice {
		case "1":
			command = []string{"list"}
		case "2":
			command = []string{"get", strings.TrimSpace(readPrompt(reader, output, "Country code: "))}
		case "3":
			command = []string{"search", strings.TrimSpace(readPrompt(reader, output, "Search: "))}
		case "4":
			command = []string{"nearest", strings.TrimSpace(readPrompt(reader, output, "Latitude: ")), strings.TrimSpace(readPrompt(reader, output, "Longitude: "))}
		case "5":
			command = []string{"nearby", strings.TrimSpace(readPrompt(reader, output, "Latitude: ")), strings.TrimSpace(readPrompt(reader, output, "Longitude: ")), strings.TrimSpace(readPrompt(reader, output, "Radius in kilometers: "))}
		case "6":
			command = []string{"stats"}
		case "7", "q", "quit":
			return nil
		default:
			if _, err := fmt.Fprintln(output, "Choose a number from 1 to 7."); err != nil {
				return err
			}
			continue
		}
		args := append([]string{"--server", serverURL}, command...)
		if err := run(args, output, stderr); err != nil {
			if _, writeErr := fmt.Fprintln(stderr, err); writeErr != nil {
				return writeErr
			}
		}
	}
}

func readPrompt(reader *bufio.Reader, output io.Writer, prompt string) string {
	_, _ = io.WriteString(output, prompt)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "\x00"
	}
	return line
}

func checkServer(serverURL, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+api.APIPath("server/healthz"), nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func validateColor(value string) error {
	switch value {
	case "auto", "yes", "no":
		return nil
	default:
		return fmt.Errorf("invalid --color value %q; choose auto, yes, or no", value)
	}
}

func parseCoordinate(value string, min, max float64) (string, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < min || parsed > max {
		return "", fmt.Errorf("must be a number between %g and %g", min, max)
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
	header := "CODE\tCOUNTRY\tOFFICIAL NAME\tCAPITAL\tREGION\tTIMEZONES"
	if color {
		header = "\x1b[1;36m" + header + "\x1b[0m"
	}
	fmt.Fprintln(tw, header)
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", item.CCA2, item.Name.Common, item.Name.Official, strings.Join(item.Capital, ", "), item.Region, strings.Join(item.Timezones, ", "))
	}
	return tw.Flush()
}

func printJSON(w io.Writer, value interface{}, _ string) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, `countries-cli — query a Countries API server

Usage:
  countries-cli [flags] COMMAND [arguments]

Commands:
  list                         List country records
  get CODE                     Read a country by ISO alpha-2 code
  search QUERY                 Search country names
  nearest LAT LON              Find the nearest country centroid
  nearby LAT LON RADIUS_KM     Find countries within a radius
  stats                        Read dataset statistics

Flags:
  --server URL                 Server base URL
  --output FORMAT              table, plain, or json (default: table)
  --color MODE                 auto, yes, or no
  --debug                      Print request diagnostics to stderr
  -h, --help                   Show this help
  -v, --version                Show version information

The server URL can also come from COUNTRIES_SERVER_PRIMARY or
~/.config/apimgr/countries/cli.yml.`)
}

func printVersion(w io.Writer) {
	built := BuildDate
	if epoch, err := strconv.ParseInt(BuildEpoch, 10, 64); err == nil && epoch > 0 {
		built = time.Unix(epoch, 0).UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(w, "%s-cli %s\nBuilt: %s\nGo: %s\nOS/Arch: %s/%s\n", projectName, Version, built, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// selectLanguage picks the first supported language from the flag, config, then the LANG environment, falling back to English.
func selectLanguage(flagValue, configValue string) string {
	envValue := strings.SplitN(os.Getenv("LANG"), ".", 2)[0]
	envValue = strings.SplitN(envValue, "_", 2)[0]
	for _, candidate := range []string{flagValue, configValue, envValue} {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate != "" && i18n.IsSupported(candidate) {
			return candidate
		}
	}
	return "en"
}
