//go:build gui && !freebsd && !netbsd && !openbsd

package main

import (
	"log"
	"strings"

	"github.com/apimgr/countries/src/common/display"
	"github.com/gogpu/gg"
	_ "github.com/gogpu/gg/gpu"
	"github.com/gogpu/gg/integration/ggcanvas"
	"github.com/gogpu/gogpu"
	"github.com/gogpu/ui/app"
	"github.com/gogpu/ui/core/button"
	"github.com/gogpu/ui/core/dialog"
	"github.com/gogpu/ui/core/textfield"
	"github.com/gogpu/ui/desktop"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/render"
	"github.com/gogpu/ui/theme"
	"github.com/gogpu/ui/widget"
)

type clientGUI struct {
	app        *app.App
	server     string
	token      string
	configPath string
	config     *cliConfig
	screen     string
	command    string
	fields     []string
	values     []string
	result     string
	message    string
}

func clientGUIAvailable() bool { return true }

func launchClientGUI(server string, cfg *cliConfig, configPath string) error {
	title := clientText(cfg.Lang, "tui.title")
	window := gogpu.NewApp(gogpu.DefaultConfig().WithTitle(title).WithSize(900, 700))
	uiTheme := theme.DefaultDark()
	if display.DetectSystemTheme() == display.ThemeLight {
		uiTheme = theme.DefaultLight()
	}
	ui := app.New(app.WithWindowProvider(window), app.WithPlatformProvider(window), app.WithEventSource(window.EventSource()), app.WithTheme(uiTheme))
	state := &clientGUI{app: ui, server: server, token: cfg.Server.Token, configPath: configPath, config: cfg, screen: "menu"}
	if strings.TrimSpace(server) == "" {
		state.screen = "setup"
	}
	ui.SetRoot(state.view())
	var canvas *ggcanvas.Canvas
	window.OnDraw(func(dc *gogpu.Context) {
		w, h := dc.Width(), dc.Height()
		if w <= 0 || h <= 0 {
			return
		}
		if canvas == nil {
			provider := window.GPUContextProvider()
			if provider == nil {
				return
			}
			var err error
			canvas, err = ggcanvas.New(provider, w, h)
			if err != nil {
				log.Printf("GUI canvas initialization failed: %v", err)
				return
			}
		}
		ui.Frame()
		cw, ch := canvas.Size()
		if cw != w || ch != h {
			if err := canvas.Resize(w, h); err != nil {
				log.Printf("GUI canvas resize failed: %v", err)
				return
			}
			cw, ch = w, h
		}
		canvas.Draw(func(cc *gg.Context) {
			r, g, b, a := ui.Theme().Colors.Background.RGBA8()
			cc.SetRGBA(float64(r)/255, float64(g)/255, float64(b)/255, float64(a)/255)
			cc.DrawRectangle(0, 0, float64(cw), float64(ch))
			cc.Fill()
			ui.Window().DrawTo(render.NewCanvas(cc, cw, ch))
		})
		if err := canvas.Render(dc.RenderTarget()); err != nil {
			log.Printf("GUI canvas render failed: %v", err)
		}
	})
	return desktop.Run(window, ui)
}

func (state *clientGUI) view() *primitives.BoxWidget {
	children := []widget.Widget{primitives.Text(clientText(state.config.Lang, "tui.title")).FontSize(28).Bold(), primitives.Text(state.message)}
	switch state.screen {
	case "setup":
		children = append(children, primitives.Text(clientText(state.config.Lang, "gui.setup")))
		serverLabel := clientText(state.config.Lang, "tui.server_url")
		children = append(children, textfield.New(textfield.Placeholder(serverLabel), textfield.InitialValue(state.server), textfield.A11yLabel(serverLabel), textfield.OnChange(func(value string) { state.server = value })))
		tokenLabel := clientText(state.config.Lang, "gui.optional_token")
		children = append(children, textfield.New(textfield.Placeholder(tokenLabel), textfield.InitialValue(state.token), textfield.A11yLabel(tokenLabel), textfield.InputTypeOpt(textfield.TypePassword), textfield.OnChange(func(value string) { state.token = value })))
		children = append(children, button.New(button.Text(clientText(state.config.Lang, "gui.test_save")), button.OnClick(state.saveSetup)))
	case "arguments":
		children = append(children, primitives.Text(clientText(state.config.Lang, "command."+state.command)))
		for i, label := range state.fields {
			index := i
			children = append(children, primitives.Text(label), textfield.New(textfield.Placeholder(label), textfield.InitialValue(state.values[i]), textfield.A11yLabel(label), textfield.OnChange(func(value string) { state.values[index] = value })))
		}
		children = append(children, button.New(button.Text(clientText(state.config.Lang, "gui.run")), button.OnClick(state.execute)), button.New(button.Text(clientText(state.config.Lang, "gui.back")), button.OnClick(state.showMenu)))
	case "request":
		children = append(children, primitives.Text(clientText(state.config.Lang, "request.title")))
		for i, label := range state.fields {
			index := i
			children = append(children, primitives.Text(label), textfield.New(textfield.Placeholder(label), textfield.InitialValue(state.values[i]), textfield.A11yLabel(label), textfield.OnChange(func(value string) { state.values[index] = value })))
		}
		children = append(children, primitives.Text(clientText(state.config.Lang, "request.path_help")), button.New(button.Text(clientText(state.config.Lang, "gui.run")), button.OnClick(state.execute)), button.New(button.Text(clientText(state.config.Lang, "gui.back")), button.OnClick(state.showMenu)))
	case "result":
		children = append(children, primitives.Text(state.result), button.New(button.Text(clientText(state.config.Lang, "gui.back_actions")), button.OnClick(state.showMenu)))
	default:
		children = append(children, primitives.Text(clientText(state.config.Lang, "gui.choose")))
		for _, name := range clientCommands {
			command := name
			children = append(children, button.New(button.Text(clientText(state.config.Lang, "command."+command)), button.OnClick(func() { state.choose(command) })))
		}
		children = append(children, button.New(button.Text(clientText(state.config.Lang, "gui.help")), button.OnClick(func() {
			state.screen = "result"
			state.result = clientText(state.config.Lang, "gui.actions_help")
			state.refresh()
		})))
	}
	return primitives.Box(children...).Padding(28).Gap(12)
}

func (state *clientGUI) refresh() { state.app.SetRoot(state.view()) }

func (state *clientGUI) showMenu() {
	state.screen, state.message = "menu", ""
	state.refresh()
}

func (state *clientGUI) choose(command string) {
	state.command, state.message = command, ""
	switch command {
	case "get":
		state.fields = []string{clientText(state.config.Lang, "tui.input_country_code")}
	case "search":
		state.fields = []string{clientText(state.config.Lang, "tui.input_search")}
	case "nearest":
		state.fields = []string{clientText(state.config.Lang, "tui.input_latitude"), clientText(state.config.Lang, "tui.input_longitude")}
	case "nearby":
		state.fields = []string{clientText(state.config.Lang, "tui.input_latitude"), clientText(state.config.Lang, "tui.input_longitude"), clientText(state.config.Lang, "tui.input_radius")}
	case "request":
		state.fields = []string{clientText(state.config.Lang, "request.method"), clientText(state.config.Lang, "request.path"), clientText(state.config.Lang, "request.body_optional")}
	default:
		state.fields = nil
	}
	state.values = make([]string, len(state.fields))
	if len(state.fields) == 0 {
		state.execute()
		return
	}
	state.screen = clientCommandFormScreen(command)
	state.refresh()
}

func (state *clientGUI) saveSetup() {
	server, err := normalizeBaseURL(state.server, state.config.Lang)
	if err == nil {
		err = checkServer(server, state.token, state.config.Lang)
	}
	if err != nil {
		state.message = ""
		state.refresh()
		state.showError(err.Error())
		return
	}
	state.server = server
	state.config.Server.Primary = server
	state.config.Server.Token = state.token
	if err := saveConfigAt(*state.config, state.configPath); err != nil {
		state.message = ""
		state.refresh()
		state.showError(err.Error())
		return
	}
	state.screen, state.message = "menu", clientText(state.config.Lang, "gui.saved")
	state.refresh()
}

func (state *clientGUI) execute() {
	args := []string{"--server", state.server, "--token", state.token, "--lang", state.config.Lang, "--color=no", state.command}
	args = append(args, state.values...)
	var output strings.Builder
	clientUICommand = true
	err := run(args, &output, &output)
	clientUICommand = false
	state.result = output.String()
	if err != nil {
		state.message = ""
		if state.result == "" {
			state.result = err.Error()
		}
		state.screen = "result"
		state.refresh()
		state.showError(clientGUIErrorMessage(err))
		return
	} else {
		state.message = ""
	}
	state.screen = "result"
	state.refresh()
}

func (state *clientGUI) showError(message string) {
	alert := dialog.New(
		dialog.Title(clientText(state.config.Lang, "gui.request_failed")),
		dialog.Content(primitives.Text(message)),
		dialog.Actions(dialog.Action{Label: clientText(state.config.Lang, "gui.ok"), Variant: dialog.VariantFilled, Default: true}),
	)
	alert.Show(state.app.Window().Context())
}

func clientGUIErrorMessage(err error) string {
	message := err.Error()
	if strings.HasPrefix(message, "server returned HTTP ") {
		parts := strings.SplitN(message, ": ", 2)
		if len(parts) == 2 {
			return parts[1]
		}
	}
	return message
}
