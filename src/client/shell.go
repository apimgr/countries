package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/apimgr/countries/src/common/i18n"
)

var clientCommands = []string{"list", "get", "search", "nearest", "nearby", "stats", "request"}

func shellCommand(args []string, stdout io.Writer, selectedLanguage ...string) (bool, error) {
	if len(args) == 0 || args[0] != "--shell" {
		return false, nil
	}
	lang := "en"
	if len(selectedLanguage) > 0 && selectedLanguage[0] != "" {
		lang = selectedLanguage[0]
	}
	if len(args) < 2 || len(args) > 3 {
		return true, errors.New(clientText(lang, "shell.usage"))
	}
	command := args[1]
	shell := ""
	if len(args) == 3 {
		shell = args[2]
	} else {
		shell = detectShell()
	}
	binaryName := filepath.Base(os.Args[0])
	if command == "help" || command == "--help" || command == "-h" {
		_, err := io.WriteString(stdout, i18n.TranslateFormat(lang, "client.shell.help", map[string]string{"binary": binaryName})+"\n")
		return true, err
	}
	switch command {
	case "completions":
		return true, writeCompletions(stdout, shell, binaryName, lang)
	case "init":
		return true, writeShellInit(stdout, shell, binaryName, lang)
	default:
		return true, errors.New(clientText(lang, "shell.usage"))
	}
}

func detectShell() string {
	if shell := filepath.Base(strings.TrimSpace(os.Getenv("SHELL"))); shell != "." && shell != "/" && shell != "" {
		return shell
	}
	return "bash"
}

func writeCompletions(w io.Writer, shell, binaryName string, selectedLanguage ...string) error {
	lang := "en"
	if len(selectedLanguage) > 0 && selectedLanguage[0] != "" {
		lang = selectedLanguage[0]
	}
	commandList := strings.Join(clientCommands, " ")
	switch shell {
	case "bash":
		_, err := fmt.Fprintf(w, "_%s_complete() {\n  local cur=\"${COMP_WORDS[COMP_CWORD]}\"\n  COMPREPLY=( $(compgen -W %q -- \"$cur\") )\n}\ncomplete -F _%s_complete %s\n", completionFunctionName(binaryName), commandList, completionFunctionName(binaryName), shellQuote(binaryName))
		return err
	case "zsh":
		_, err := fmt.Fprintf(w, "#compdef %s\n_arguments '1:command:(%s)' '*:argument:'\n", binaryName, commandList)
		return err
	case "fish":
		_, err := fmt.Fprintf(w, "complete -c %s -f -n '__fish_use_subcommand' -a '%s'\n", binaryName, commandList)
		return err
	case "sh", "dash", "ksh":
		_, err := fmt.Fprintf(w, "_%s_complete() {\n  COMPREPLY=\"%s\"\n}\n", completionFunctionName(binaryName), commandList)
		return err
	case "powershell", "pwsh":
		_, err := fmt.Fprintf(w, "Register-ArgumentCompleter -Native -CommandName '%s' -ScriptBlock { param($wordToComplete) '%s'.Split(' ') | Where-Object { $_ -like \"$wordToComplete*\" } }\n", strings.ReplaceAll(binaryName, "'", "''"), commandList)
		return err
	default:
		return errors.New(i18n.TranslateFormat(lang, "client.shell.unsupported", map[string]string{"shell": shell}))
	}
}

func writeShellInit(w io.Writer, shell, binaryName string, selectedLanguage ...string) error {
	lang := "en"
	if len(selectedLanguage) > 0 && selectedLanguage[0] != "" {
		lang = selectedLanguage[0]
	}
	var line string
	switch shell {
	case "bash", "zsh":
		line = "source <(" + shellQuote(binaryName) + " --shell completions " + shell + ")\n"
	case "fish":
		line = shellQuote(binaryName) + " --shell completions fish | source\n"
	case "sh", "dash", "ksh":
		line = "eval \"$(" + shellQuote(binaryName) + " --shell completions " + shell + ")\"\n"
	case "powershell", "pwsh":
		line = "Invoke-Expression (& '" + strings.ReplaceAll(binaryName, "'", "''") + "' --shell completions " + shell + ")\n"
	default:
		return errors.New(i18n.TranslateFormat(lang, "client.shell.unsupported", map[string]string{"shell": shell}))
	}
	_, err := io.WriteString(w, line)
	return err
}

func completionFunctionName(binaryName string) string {
	var b strings.Builder
	for _, r := range binaryName {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
