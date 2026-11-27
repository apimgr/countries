// Package banner prints the server's responsive startup banner.
package banner

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/apimgr/countries/src/common/i18n"
	"golang.org/x/term"
)

type ServerBannerInfo struct {
	AppName         string
	Version         string
	AppMode         string
	StartupDatetime string
	URLs            []string
	OnionAddress    string
	I2PAddress      string
	SMTPAddress     string
	SetupToken      string
	ForceColor      *bool
	Lang            string
}

// PrintServerStartupBanner selects a compact banner suitable for the current terminal.
func PrintServerStartupBanner(info ServerBannerInfo) {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		width = 80
	}
	emojis := os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	colors := terminalColor(info.ForceColor)
	appMode := info.AppMode
	started := info.StartupDatetime
	if started == "" {
		started = formatStartupTimestamp(time.Now())
	}
	if !emojis {
		printPlain(info.AppName, info.Version, appMode, info.URLs, info.Lang, started, info.SetupToken, info.OnionAddress, info.I2PAddress, info.SMTPAddress)
		return
	}
	switch {
	case width >= 80:
		fmt.Printf("  ____  ____  _   _ _   _ _____ ____  _   _ _____ ____\n")
		fmt.Printf(" / ___||  _ \\| | | | | | |_   _/ ___|| | | | ____/ ___|\n")
		fmt.Printf(" \\___ \\| |_) | |_| | | | | | || |    | |_| |  _| \\___ \\\n")
		fmt.Printf("  ___) |  __/|  _  | |_| | | || |___ |  _  | |___ ___) |\n")
		fmt.Printf(" |____/|_|   |_| |_|\\___/  |_| \\____||_| |_|_____|____/\n\n")
		fmt.Printf("╭──────────────────────────────────────────────────────────────╮\n│  🚀 %s · 📦 %s\n", info.AppName, info.Version)
		fmt.Println("├──────────────────────────────────────────────────────────────┤")
		printMode(appMode, info.Lang, true, colors)
		fmt.Println("├──────────────────────────────────────────────────────────────┤")
		printURLs(info.URLs, true)
		printOptional(info)
		fmt.Printf("│  ✅ Started: %s\n╰──────────────────────────────────────────────────────────────╯\n", started)
	case width >= 60:
		fmt.Printf("🚀 %s v%s\n", info.AppName, info.Version)
		printMode(appMode, info.Lang, true, colors)
		printURLs(info.URLs, true)
		printOptional(info)
		fmt.Printf("✅ Started: %s\n", started)
	case width >= 40:
		fmt.Printf("%s %s\n%s", info.AppName, info.Version, strings.TrimSuffix(appMode, " [debugging]"))
		if len(info.URLs) > 0 {
			fmt.Printf("\n%s", hostPort(info.URLs[0]))
		}
		fmt.Println()
	default:
		fmt.Printf("%s", info.AppName)
		if len(info.URLs) > 0 {
			fmt.Printf(" :%s", portPart(info.URLs[0]))
		}
		if info.SetupToken != "" {
			fmt.Print(" [SETUP]")
		}
		fmt.Println()
	}
}

func printPlain(appName, version, appMode string, urls []string, lang, started, setupToken, onion, i2p, smtp string) {
	fmt.Printf("%s v%s\n", appName, version)
	fmt.Printf("%s: %s\n", i18n.Translate(lang, "cli.running_in_mode_label"), appMode)
	for _, address := range urls {
		fmt.Printf("%s: %s\n", i18n.Translate(lang, "cli.listening_label"), address)
	}
	fmt.Printf("%s: %s\n", i18n.Translate(lang, "cli.started_label"), started)
	if onion != "" {
		fmt.Printf("Tor: http://%s\n", onion)
	}
	if i2p != "" {
		fmt.Printf("I2P: http://%s\n", i2p)
	}
	if smtp != "" {
		fmt.Printf("SMTP: %s\n", smtp)
	}
	if setupToken != "" {
		fmt.Printf("Setup token: %s\n", setupToken)
	}
	fmt.Println()
}

func printMode(appMode, lang string, emoji, color bool) {
	icon := ""
	if emoji {
		switch strings.ToLower(appMode) {
		case "development":
			icon = "🔧 "
		case "debug":
			icon = "🐛 "
		default:
			icon = "🔒 "
		}
	}
	label := i18n.Translate(lang, "cli.running_in_mode_label")
	debugging := ""
	if strings.HasSuffix(appMode, " [debugging]") {
		debugging = " [debugging]"
		appMode = strings.TrimSuffix(appMode, debugging)
	}
	line := fmt.Sprintf("%s%s: %s%s", icon, label, appMode, debugging)
	if color {
		line = "\x1b[36m" + line + "\x1b[0m"
	}
	fmt.Println(line)
}

func printURLs(urls []string, emoji bool) {
	for _, address := range urls {
		if emoji {
			icon := "🌐"
			lower := strings.ToLower(address)
			switch {
			case strings.Contains(lower, ".onion"):
				icon = "🧅"
			case strings.Contains(lower, ".i2p"):
				icon = "🔗"
			case strings.HasPrefix(lower, "https://"):
				icon = "🔐"
			case strings.Contains(hostPort(address), ":") && !strings.Contains(hostPort(address), "]:"):
				icon = "🌍"
			}
			fmt.Printf("%s %s\n", icon, address)
		} else {
			fmt.Printf("%s\n", address)
		}
	}
}

func terminalColor(forceColor *bool) bool {
	if forceColor != nil {
		return *forceColor
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func hostPort(address string) string {
	address = strings.TrimPrefix(strings.TrimPrefix(address, "https://"), "http://")
	if i := strings.IndexAny(address, "/?#"); i >= 0 {
		address = address[:i]
	}
	return address
}

func portPart(address string) string {
	address = hostPort(address)
	if index := strings.LastIndex(address, ":"); index >= 0 {
		return address[index+1:]
	}
	return ""
}

func formatStartupTimestamp(timestamp time.Time) string {
	location, err := time.LoadLocation("America/New_York")
	if configured := os.Getenv("TZ"); configured != "" {
		if configuredLocation, loadErr := time.LoadLocation(configured); loadErr == nil {
			location, err = configuredLocation, nil
		}
	}
	if err != nil || location == nil {
		location = time.FixedZone("UTC", 0)
	}
	return timestamp.In(location).Format("January 02, 2006 at 15:04:05 MST")
}

func printOptional(info ServerBannerInfo) {
	if info.OnionAddress != "" {
		fmt.Printf("🧅 Tor: http://%s\n", info.OnionAddress)
	}
	if info.I2PAddress != "" {
		fmt.Printf("🔗 I2P: http://%s\n", info.I2PAddress)
	}
	if info.SMTPAddress != "" {
		fmt.Printf("📧 SMTP: %s\n", info.SMTPAddress)
	}
	if info.SetupToken != "" {
		fmt.Printf("🔑 Setup token: %s\n", info.SetupToken)
	}
}
