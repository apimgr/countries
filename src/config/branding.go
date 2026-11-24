package config

import (
	"fmt"
	"strings"
)

const (
	maxBrandingBodySize = 256 * 1024
)

func validateBrandingContent(name string, entry PageContentConfig) error {
	switch name {
	case "header", "about", "footer", "privacy", "terms":
	default:
		return fmt.Errorf("unknown page content key %q", name)
	}
	format := strings.ToLower(strings.TrimSpace(entry.Format))
	if format == "" {
		format = "md"
	}
	if format != "md" && format != "html" && format != "txt" {
		return fmt.Errorf("unsupported page content format %q", entry.Format)
	}
	if (name == "header" || name == "footer") && format != "html" {
		return fmt.Errorf("%s content must use html format", name)
	}
	if entry.Body == nil {
		return fmt.Errorf("body is required")
	}
	if brandingBodySize(entry.Body) > maxBrandingBodySize {
		return fmt.Errorf("page content exceeds %d bytes", maxBrandingBodySize)
	}
	return nil
}

func brandingBodySize(body any) int {
	switch value := body.(type) {
	case string:
		return len(value)
	case map[string]any:
		total := 0
		for _, localized := range value {
			if text, ok := localized.(string); ok {
				total += len(text)
			}
		}
		return total
	default:
		return maxBrandingBodySize + 1
	}
}
