package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"golang.org/x/term"
)

type clientSetupInfo struct {
	name      string
	version   string
	tokenKind string
}

func clientSetupInfoText(info clientSetupInfo) string {
	return strings.TrimSpace(info.name + " " + info.version)
}

func clientSetupConfigGuidance(lang string) string {
	return clientText(lang, "error.server_required")
}

func clientInteractiveIO(input io.Reader, output io.Writer) bool {
	in, ok := input.(*os.File)
	if !ok || !term.IsTerminal(int(in.Fd())) {
		return false
	}
	out, ok := output.(*os.File)
	return ok && term.IsTerminal(int(out.Fd()))
}

func persistWizardSettings(configPath, server, token string, save, setDefault bool) error {
	if !save {
		return nil
	}
	if err := persistClientSettings(configPath, &server, &token); err != nil {
		return err
	}
	defaultPath, err := resolveConfigPath("")
	if err != nil {
		return err
	}
	if setDefault && defaultPath != configPath {
		return persistClientSettings(defaultPath, &server, &token)
	}
	return nil
}

func runInlineSetup(serverURL string, cfg *cliConfig, configPath string, input io.Reader, output io.Writer) error {
	if !clientInteractiveIO(input, output) {
		return errors.New(clientSetupConfigGuidance(cfg.Lang))
	}
	reader := bufio.NewReader(input)
	if strings.TrimSpace(serverURL) == "" {
		if _, err := fmt.Fprint(output, clientText(cfg.Lang, "tui.server_url")+": "); err != nil {
			return err
		}
		value, err := reader.ReadString('\n')
		if err != nil && len(value) == 0 {
			return err
		}
		serverURL = strings.TrimSpace(value)
	}
	server, err := normalizeBaseURL(serverURL, cfg.Lang)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprint(output, clientText(cfg.Lang, "gui.optional_token")+": "); err != nil {
		return err
	}
	inputFile, ok := input.(*os.File)
	if !ok {
		return errors.New(clientSetupConfigGuidance(cfg.Lang))
	}
	secret, err := term.ReadPassword(int(inputFile.Fd()))
	if _, writeErr := fmt.Fprintln(output); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(secret))
	info, err := validateClientSetup(server, token, cfg.Lang)
	if err != nil {
		return err
	}
	save, err := askInlineYesNo(reader, output, clientText(cfg.Lang, "gui.save_config")+" [Y/n] ", true)
	if err != nil {
		return err
	}
	setDefault, err := askInlineYesNo(reader, output, clientText(cfg.Lang, "gui.set_default")+" [y/N] ", false)
	if err != nil {
		return err
	}
	if err := persistWizardSettings(configPath, server, token, save, setDefault); err != nil {
		return err
	}
	cfg.Server.Primary, cfg.Server.Token, cfg.Auth.Token = server, token, token
	if _, err := fmt.Fprintln(output, clientTextFormat(cfg.Lang, "gui.connected", map[string]string{"server": clientSetupInfoText(info)})); err != nil {
		return err
	}
	return nil
}

func validateClientSetup(serverURL, token, lang string) (clientSetupInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := checkClientEndpoint(ctx, serverURL+api.APIPath("server/healthz"), token, lang); err != nil {
		return clientSetupInfo{}, err
	}
	info := clientSetupInfo{}
	if token != "" {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+api.APIPath("server/auth/check"), nil)
		if err != nil {
			return info, err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Accept-Language", lang)
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			return info, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		closeErr := response.Body.Close()
		if readErr != nil {
			return info, readErr
		}
		if closeErr != nil {
			return info, closeErr
		}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return info, errors.New(clientText(lang, "tokens.error_invalid"))
		}
		if response.StatusCode != http.StatusOK {
			return info, fmt.Errorf("%s", clientTextFormat(lang, "error.server_http", map[string]string{"status": fmt.Sprint(response.StatusCode)}))
		}
		var envelope struct {
			Data struct {
				Kind string `json:"kind"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return info, err
		}
		info.tokenKind = strings.TrimSpace(envelope.Data.Kind)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/api/autodiscover", nil)
	if err != nil {
		return info, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Language", lang)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return info, err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	closeErr := response.Body.Close()
	if readErr != nil {
		return info, readErr
	}
	if closeErr != nil {
		return info, closeErr
	}
	if response.StatusCode != http.StatusOK {
		return info, fmt.Errorf("%s", clientTextFormat(lang, "error.server_http", map[string]string{"status": fmt.Sprint(response.StatusCode)}))
	}
	var envelope struct {
		Data struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return info, err
	}
	info.name, info.version = strings.TrimSpace(envelope.Data.Name), strings.TrimSpace(envelope.Data.Version)
	if info.name == "" || info.version == "" {
		return info, errors.New(clientText(lang, "gui.request_failed"))
	}
	return info, nil
}

func askInlineYesNo(reader *bufio.Reader, output io.Writer, prompt string, defaultYes bool) (bool, error) {
	if _, err := fmt.Fprint(output, prompt); err != nil {
		return false, err
	}
	value, err := reader.ReadString('\n')
	if err != nil && len(value) == 0 {
		return false, err
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return defaultYes, nil
	}
	return value == "y" || value == "yes", nil
}
