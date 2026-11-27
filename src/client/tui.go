package main

import (
	"io"
	"strings"
	"unicode/utf8"

	"github.com/apimgr/countries/src/common/i18n"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tuiItem struct {
	command string
	title   string
	desc    string
}

func (item tuiItem) Title() string       { return item.title }
func (item tuiItem) Description() string { return item.desc }
func (item tuiItem) FilterValue() string { return item.title + " " + item.desc }

type clientTUI struct {
	menu        list.Model
	server      string
	token       string
	configPath  string
	config      *cliConfig
	screen      string
	command     string
	args        []string
	fields      []string
	fieldLabels []string
	fieldIndex  int
	input       string
	result      string
	errorText   string
	setupInfo   string
	width       int
	height      int
	showHelp    bool
	saveSetup   bool
	setDefault  bool
	theme       TUITheme
	stdout      io.Writer
	stderr      io.Writer
}

func newClientTUI(server string, cfg *cliConfig, configPath string, stdout, stderr io.Writer) clientTUI {
	items := make([]list.Item, 0, len(clientCommands))
	descriptions := map[string]string{
		"list":    clientText(cfg.Lang, "command.list"),
		"get":     clientText(cfg.Lang, "command.get"),
		"search":  clientText(cfg.Lang, "command.search"),
		"nearest": clientText(cfg.Lang, "command.nearest"),
		"nearby":  clientText(cfg.Lang, "command.nearby"),
		"stats":   clientText(cfg.Lang, "command.stats"),
		"request": clientText(cfg.Lang, "command.request"),
	}
	for _, command := range clientCommands {
		title := clientText(cfg.Lang, "command_title."+command)
		if command == "request" {
			title = clientText(cfg.Lang, "request.title")
		}
		items = append(items, tuiItem{command: command, title: title, desc: descriptions[command]})
	}
	menu := list.New(items, list.NewDefaultDelegate(), 80, 24)
	menu.Title = clientText(cfg.Lang, "tui.title")
	menu.SetShowStatusBar(false)
	menu.SetShowHelp(false)
	menu.SetFilteringEnabled(true)
	view := clientTUI{
		menu:       menu,
		server:     server,
		token:      cfg.Server.Token,
		configPath: configPath,
		config:     cfg,
		theme:      tuiTheme(cfg.TUI.Theme),
		screen:     "menu",
		stdout:     stdout,
		stderr:     stderr,
		width:      80,
		height:     24,
		saveSetup:  true,
	}
	if server == "" {
		view.screen = "setup-server"
	}
	return view
}

func runClientTUI(server string, cfg *cliConfig, configPath string, stdout, stderr io.Writer) error {
	program := tea.NewProgram(newClientTUI(server, cfg, configPath, stdout, stderr), tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func (model clientTUI) Init() tea.Cmd { return nil }

func (model clientTUI) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = message.Width, message.Height
		model.menu.SetSize(max(20, message.Width-8), max(5, message.Height-8))
		return model, nil
	case tea.KeyMsg:
		if message.Type == tea.KeyCtrlC {
			return model, tea.Quit
		}
		key := message.String()
		if key == "q" && (model.screen == "menu" || model.screen == "result") && !model.menu.SettingFilter() {
			return model, tea.Quit
		}
		if key == "?" && model.screen == "menu" {
			model.showHelp = !model.showHelp
			return model, nil
		}
		if model.showHelp {
			if key == "esc" || key == "enter" || key == "?" {
				model.showHelp = false
			}
			return model, nil
		}
		switch model.screen {
		case "setup-server", "setup-token", "arguments", "request":
			return model.updateInput(message)
		case "setup-options":
			switch key {
			case "s":
				model.saveSetup = !model.saveSetup
			case "d":
				model.setDefault = !model.setDefault
			case "enter":
				if err := persistWizardSettings(model.configPath, model.server, model.token, model.saveSetup, model.setDefault); err != nil {
					model.errorText = err.Error()
					return model, nil
				}
				model.config.Server.Primary = model.server
				model.config.Server.Token, model.config.Auth.Token = model.token, model.token
				model.screen = "menu"
				model.errorText = ""
				if !model.saveSetup {
					model.errorText = "Configuration was not saved; this connection is available for this run only."
				}
			}
			return model, nil
		case "menu":
			if key == "enter" {
				selected, ok := model.menu.SelectedItem().(tuiItem)
				if ok {
					return model.selectCommand(selected.command)
				}
			}
			updated, command := model.menu.Update(message)
			model.menu = updated
			return model, command
		case "result":
			if key == "esc" || key == "enter" || key == "backspace" {
				model.screen = "menu"
				model.errorText = ""
				return model, nil
			}
		}
	}
	return model, nil
}

func (model clientTUI) updateInput(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch message.Type {
	case tea.KeyEsc:
		if model.screen == "arguments" || model.screen == "request" {
			model.screen = "menu"
			model.errorText = ""
			return model, nil
		}
	case tea.KeyTab, tea.KeyShiftTab:
		if (model.screen == "arguments" || model.screen == "request") && len(model.fields) > 1 {
			if message.Type == tea.KeyShiftTab {
				model.fieldIndex = (model.fieldIndex + len(model.fields) - 1) % len(model.fields)
			} else {
				model.fieldIndex = (model.fieldIndex + 1) % len(model.fields)
			}
			model.input = model.fields[model.fieldIndex]
			return model, nil
		}
	case tea.KeyEnter:
		value := strings.TrimSpace(model.input)
		switch model.screen {
		case "setup-server":
			server, err := normalizeBaseURL(value, model.config.Lang)
			if err != nil {
				model.errorText = err.Error()
				return model, nil
			}
			model.server = server
			model.screen = "setup-token"
			model.input = ""
			model.errorText = ""
			return model, nil
		case "setup-token":
			model.token = value
			info, err := validateClientSetup(model.server, model.token, model.config.Lang)
			if err != nil {
				model.errorText = err.Error()
				return model, nil
			}
			model.setupInfo = clientSetupInfoText(info)
			model.screen = "setup-options"
			model.input = ""
			model.errorText = ""
			return model, nil
		case "arguments", "request":
			model.fields[model.fieldIndex] = value
			if model.fieldIndex+1 < len(model.fields) {
				model.fieldIndex++
				model.input = model.fields[model.fieldIndex]
				model.errorText = ""
				return model, nil
			}
			return model.executeSelected()
		}
	case tea.KeyBackspace, tea.KeyDelete:
		if len(model.input) > 0 {
			_, size := utf8.DecodeLastRuneInString(model.input)
			model.input = model.input[:len(model.input)-size]
		}
		return model, nil
	case tea.KeyRunes:
		model.input += string(message.Runes)
		return model, nil
	case tea.KeySpace:
		model.input += " "
		return model, nil
	}
	return model, nil
}

func (model clientTUI) selectCommand(command string) (tea.Model, tea.Cmd) {
	model.command = command
	model.args = nil
	model.fields = nil
	model.fieldLabels = nil
	model.fieldIndex = 0
	model.input = ""
	switch command {
	case "get":
		model.fieldLabels = []string{clientText(model.config.Lang, "tui.input_country_code")}
	case "search":
		model.fieldLabels = []string{clientText(model.config.Lang, "tui.input_search")}
	case "nearest":
		model.fieldLabels = []string{clientText(model.config.Lang, "tui.input_latitude"), clientText(model.config.Lang, "tui.input_longitude")}
	case "nearby":
		model.fieldLabels = []string{clientText(model.config.Lang, "tui.input_latitude"), clientText(model.config.Lang, "tui.input_longitude"), clientText(model.config.Lang, "tui.input_radius")}
	case "request":
		model.fieldLabels = []string{clientText(model.config.Lang, "request.method"), clientText(model.config.Lang, "request.path"), clientText(model.config.Lang, "request.body_optional")}
	default:
		return model.executeSelected()
	}
	model.fields = make([]string, len(model.fieldLabels))
	model.screen = clientCommandFormScreen(command)
	model.errorText = ""
	return model, nil
}

func (model clientTUI) executeSelected() (tea.Model, tea.Cmd) {
	model.args = []string{model.command}
	model.args = append(model.args, model.fields...)
	var output strings.Builder
	clientUICommand = true
	err := run(append([]string{"--server", model.server, "--token", model.token, "--lang", model.config.Lang, "--color=no"}, model.args...), &output, model.stderr)
	clientUICommand = false
	model.result = output.String()
	model.errorText = ""
	if err != nil {
		model.errorText = err.Error()
	}
	model.screen = "result"
	return model, nil
}

func (model clientTUI) View() string {
	width := max(20, model.width-4)
	base := lipgloss.NewStyle().Width(width).MaxWidth(width).Foreground(lipgloss.Color(model.theme.Foreground)).Background(lipgloss.Color(model.theme.Background))
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(model.theme.Primary))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(model.theme.Muted))
	errorStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(model.theme.Error))
	var content string
	switch {
	case model.showHelp:
		content = strings.Join([]string{clientText(model.config.Lang, "tui.keyboard_help"), "", clientText(model.config.Lang, "tui.move_help"), clientText(model.config.Lang, "tui.select_help"), clientText(model.config.Lang, "tui.tab_help"), clientText(model.config.Lang, "tui.filter_help"), clientText(model.config.Lang, "tui.toggle_help"), clientText(model.config.Lang, "tui.back_help"), clientText(model.config.Lang, "tui.quit_help")}, "\n")
	case model.screen == "setup-server":
		content = clientText(model.config.Lang, "tui.first_setup") + "\n\n" + clientText(model.config.Lang, "tui.connect_server") + "\n" + clientText(model.config.Lang, "tui.server_url") + ": " + model.input + "▏\n\n" + clientText(model.config.Lang, "tui.test_connection")
	case model.screen == "setup-token":
		content = clientText(model.config.Lang, "tui.first_setup") + "\n\n" + clientText(model.config.Lang, "tui.server_url") + ": " + model.server + "\n" + clientText(model.config.Lang, "tui.optional_token") + ": " + strings.Repeat("•", utf8.RuneCountInString(model.input)) + "▏\n\n" + clientText(model.config.Lang, "tui.token_prompt")
	case model.screen == "setup-options":
		saveMark, defaultMark := "[ ]", "[ ]"
		if model.saveSetup {
			saveMark = "[x]"
		}
		if model.setDefault {
			defaultMark = "[x]"
		}
		content = strings.Join([]string{clientText(model.config.Lang, "tui.first_setup"), "", clientTextFormat(model.config.Lang, "gui.connected", map[string]string{"server": model.setupInfo}), "s  " + saveMark + " " + clientText(model.config.Lang, "gui.save_config"), "d  " + defaultMark + " " + clientText(model.config.Lang, "gui.set_default"), "", clientText(model.config.Lang, "tui.select_help") + " · " + clientText(model.config.Lang, "tui.setup_toggle")}, "\n")
	case model.screen == "arguments":
		lines := []string{clientText(model.config.Lang, "tui.title") + " · " + clientText(model.config.Lang, "command_title."+model.command), ""}
		for i, label := range model.fieldLabels {
			value := model.fields[i]
			if i == model.fieldIndex {
				value = model.input + "▏"
			}
			lines = append(lines, label+": "+value)
		}
		lines = append(lines, "", clientText(model.config.Lang, "tui.fields_help"))
		content = strings.Join(lines, "\n")
	case model.screen == "request":
		lines := []string{"Countries CLI · " + clientText(model.config.Lang, "request.title"), ""}
		for i, label := range model.fieldLabels {
			value := model.fields[i]
			if i == model.fieldIndex {
				value = model.input + "▏"
			}
			lines = append(lines, label+": "+value)
		}
		lines = append(lines, "", clientText(model.config.Lang, "tui.fields_help"), clientText(model.config.Lang, "request.path_help"))
		content = strings.Join(lines, "\n")
	case model.screen == "result":
		commandTitle := clientText(model.config.Lang, "command_title."+model.command)
		if model.command == "request" {
			commandTitle = clientText(model.config.Lang, "request.title")
		}
		content = clientText(model.config.Lang, "tui.title") + " · " + commandTitle + "\n\n" + model.result + "\n" + clientText(model.config.Lang, "tui.result_back")
	default:
		content = model.menu.View()
	}
	if model.height > 0 && model.height < 10 {
		content = strings.Join(strings.Split(content, "\n")[:min(3, len(strings.Split(content, "\n")))], "\n")
	}
	content = fitTUIContent(content, width)
	footer := clientText(model.config.Lang, "tui.move_help") + " · " + clientText(model.config.Lang, "tui.select_help") + " · " + clientText(model.config.Lang, "tui.filter_help") + " · " + clientText(model.config.Lang, "tui.toggle_help") + " · " + clientText(model.config.Lang, "tui.quit_help")
	if model.width < 60 {
		footer = "j/k · " + clientText(model.config.Lang, "tui.select_help") + " · " + clientText(model.config.Lang, "tui.toggle_help") + " · " + clientText(model.config.Lang, "tui.quit_help")
	}
	if model.height < 10 {
		footer = clientText(model.config.Lang, "tui.toggle_help") + " · " + clientText(model.config.Lang, "tui.quit_help")
	}
	var lines []string
	lines = append(lines, title.Render(clientText(model.config.Lang, "tui.title")), base.Render(content))
	if model.errorText != "" {
		lines = append(lines, errorStyle.Render(i18n.TranslateFormat(model.config.Lang, "client.tui.error", map[string]string{"message": model.errorText})))
	}
	if model.height >= 10 {
		lines = append(lines, muted.Render(footer))
	}
	return strings.Join(lines, "\n")
}

func fitTUIContent(content string, width int) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if utf8.RuneCountInString(line) > width {
			runes := []rune(line)
			lines[i] = string(runes[:max(0, width-1)]) + "…"
		}
	}
	return strings.Join(lines, "\n")
}
