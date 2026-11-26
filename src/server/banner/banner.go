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

// PrintServerStartupBanner selects a compact banner suitable for the current terminal.
func PrintServerStartupBanner(appName, version, appMode string, urls []string, forceColor *bool, lang string) {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		width = 80
	}
	emojis := os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	colors := terminalColor(forceColor)
	if !emojis {
		printPlain(appName, version, appMode, urls, lang)
		return
	}
	switch {
	case width >= 80:
		fmt.Printf("╭──────────────────────────────────────────────────────────────╮\n│  🚀 %s · 📦 %s\n", appName, version)
		fmt.Println("├──────────────────────────────────────────────────────────────┤")
		printMode(appMode, lang, true, colors)
		fmt.Println("├──────────────────────────────────────────────────────────────┤")
		printURLs(urls, true)
		fmt.Printf("│  ✅ Started: %s\n╰──────────────────────────────────────────────────────────────╯\n", startupTimestamp())
	case width >= 60:
		fmt.Printf("🚀 %s v%s\n", appName, version)
		printMode(appMode, lang, true, colors)
		printURLs(urls, true)
		fmt.Printf("✅ Started: %s\n", startupTimestamp())
	case width >= 40:
		fmt.Printf("%s %s\n%s", appName, version, strings.TrimSuffix(appMode, " [debugging]"))
		if len(urls) > 0 {
			fmt.Printf("\n%s", hostPort(urls[0]))
		}
		fmt.Println()
	default:
		fmt.Printf("%s", appName)
		if len(urls) > 0 {
			fmt.Printf(" :%s", portPart(urls[0]))
		}
		fmt.Println()
	}
}

func printPlain(appName, version, appMode string, urls []string, lang string) {
	fmt.Printf("%s v%s\n", appName, version)
	fmt.Printf("%s: %s\n", i18n.Translate(lang, "cli.running_in_mode_label"), appMode)
	for _, address := range urls {
		fmt.Printf("%s: %s\n", i18n.Translate(lang, "cli.listening_label"), address)
	}
	fmt.Printf("%s: %s\n", i18n.Translate(lang, "cli.started_label"), startupTimestamp())
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

func startupTimestamp() string {
	location, err := time.LoadLocation("America/New_York")
	if configured := os.Getenv("TZ"); configured != "" {
		if configuredLocation, loadErr := time.LoadLocation(configured); loadErr == nil {
			location, err = configuredLocation, nil
		}
	}
	if err != nil || location == nil {
		location = time.FixedZone("UTC", 0)
	}
	return time.Now().In(location).Format("January 02, 2006 at 15:04:05 MST")
}
