package main

import (
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTUIResponsiveNavigationAndForms(t *testing.T) {
	cfg := &cliConfig{Lang: "es"}
	model := newClientTUI("https://countries.example", cfg, "", io.Discard, io.Discard)
	if model.screen != "menu" || len(model.menu.Items()) != len(clientCommands) {
		t.Fatalf("initial TUI state = screen %q, items %d", model.screen, len(model.menu.Items()))
	}
	if got := model.menu.Items()[0].(tuiItem).Title(); got != "Listar" {
		t.Fatalf("Spanish TUI command title = %q, want Listar", got)
	}
	item := tuiItem{title: "Search", desc: "Find countries"}
	if item.Title() != "Search" || item.Description() != "Find countries" || item.FilterValue() != "Search Find countries" {
		t.Fatalf("TUI menu item methods returned unexpected values: %+v", item)
	}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 34, Height: 8})
	model = updated.(clientTUI)
	if model.width != 34 || model.height != 8 {
		t.Fatalf("window dimensions = %dx%d", model.width, model.height)
	}
	if view := model.View(); view == "" {
		t.Fatal("compact TUI view is empty")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	model = updated.(clientTUI)
	if !model.showHelp || !strings.Contains(model.View(), clientText("es", "tui.keyboard_help")) {
		t.Fatalf("help screen not shown: %q", model.View())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(clientTUI)
	if model.showHelp {
		t.Fatal("Escape did not dismiss TUI help")
	}

	updated, _ = model.selectCommand("nearby")
	model = updated.(clientTUI)
	if model.screen != "arguments" || len(model.fields) != 3 {
		t.Fatalf("nearby form state = screen %q, fields %v", model.screen, model.fieldLabels)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(clientTUI)
	if model.fieldIndex != 1 {
		t.Fatalf("Tab selected field %d, want 1", model.fieldIndex)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = updated.(clientTUI)
	if model.fieldIndex != 0 {
		t.Fatalf("Shift+Tab selected field %d, want 0", model.fieldIndex)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("12.5")})
	model = updated.(clientTUI)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(clientTUI)
	if model.fieldIndex != 1 || model.fields[0] != "12.5" {
		t.Fatalf("field submission state = index %d, values %v", model.fieldIndex, model.fields)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(clientTUI)
	if model.screen != "menu" || model.errorText != "" {
		t.Fatalf("Escape did not return to menu: screen=%q error=%q", model.screen, model.errorText)
	}

	updated, _ = model.selectCommand("request")
	model = updated.(clientTUI)
	if model.screen != "request" || len(model.fields) != 3 {
		t.Fatalf("request form state = screen %q, fields %v", model.screen, model.fieldLabels)
	}
	if got := fitTUIContent("long界", 4); got != "lon…" {
		t.Fatalf("fitTUIContent() = %q", got)
	}
}

func TestTUISetupAndResultTransitions(t *testing.T) {
	cfg := &cliConfig{Lang: "en"}
	model := newClientTUI("", cfg, "", io.Discard, io.Discard)
	if model.screen != "setup-server" || !strings.Contains(model.View(), "server") {
		t.Fatalf("first-run TUI state = %q", model.screen)
	}
	model.input = "not a URL"
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(clientTUI)
	if model.screen != "setup-server" || model.errorText == "" {
		t.Fatalf("invalid server URL state = screen %q error %q", model.screen, model.errorText)
	}

	model.screen = "result"
	model.errorText = "request failed"
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(clientTUI)
	if model.screen != "menu" || model.errorText != "" {
		t.Fatalf("result dismissal state = screen %q error %q", model.screen, model.errorText)
	}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if command == nil || updated.(clientTUI).screen != "menu" {
		t.Fatal("Ctrl+C did not request TUI quit")
	}
}
