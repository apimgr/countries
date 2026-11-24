// Package mode manages the process application mode and mode-aware behavior.
package mode

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"
)

// Mode represents the application mode
type Mode string

const (
	// Production mode - optimized for production deployment
	Production Mode = "production"
	// Development mode - optimized for development
	Development Mode = "development"
	// Debug mode - maximum diagnostics with security checks retained.
	Debug Mode = "debug"
)

var (
	currentMode  Mode = Production // Default mode
	debugEnabled bool
	mu           sync.RWMutex
)

// Get returns the current application mode
func Get() Mode {
	mu.RLock()
	defer mu.RUnlock()
	return currentMode
}

// Set sets the application mode
func Set(mode string) error {
	parsed, err := ParseMode(mode)
	if err != nil {
		return err
	}

	mu.Lock()
	currentMode = parsed
	mu.Unlock()

	return nil
}

// ParseMode parses a mode string and returns the corresponding Mode
// Accepts production, development, debug, and their documented shortcuts.
func ParseMode(s string) (Mode, error) {
	switch s {
	case "dev", "devel", "development":
		return Development, nil
	case "prod", "production":
		return Production, nil
	case "debug":
		return Debug, nil
	default:
		return "", fmt.Errorf("invalid mode: %s (valid options: dev, development, prod, production, debug)", s)
	}
}

// IsDevelopment returns true if the current mode is development
func IsDevelopment() bool {
	return Get() == Development
}

// IsDebug returns true when the process is running in debug mode.
func IsDebug() bool {
	return Get() == Debug
}

// IsProduction returns true if the current mode is production
func IsProduction() bool {
	return Get() == Production
}

// Initialize initializes the mode from CLI flag or environment variable
// Priority: CLI flag > MODE env var > default (production)
func Initialize(cliMode string) error {
	var modeStr string

	// Priority 1: CLI flag (if provided)
	if cliMode != "" {
		modeStr = cliMode
	} else {
		// Priority 2: MODE environment variable
		envMode := os.Getenv("MODE")
		if envMode != "" {
			modeStr = envMode
		} else {
			// Priority 3: Default to production
			modeStr = "production"
		}
	}

	if err := Set(modeStr); err != nil {
		return err
	}
	defaultDebug := Get() == Debug
	mu.Lock()
	debugEnabled = defaultDebug
	mu.Unlock()
	return nil
}

// SetDebugEnabled explicitly controls debug endpoints and diagnostics.
func SetDebugEnabled(enabled bool) {
	mu.Lock()
	debugEnabled = enabled
	mu.Unlock()
}

// DebugEnabled reports whether debug endpoints and diagnostics are enabled.
func DebugEnabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return debugEnabled
}

// GetErrorDetail returns error details based on the current mode
// In development mode: returns full error details with stack trace
// In production mode: returns generic error message
func GetErrorDetail(err error) string {
	if err == nil {
		return ""
	}

	if IsDevelopment() || IsDebug() {
		// Return detailed error with stack trace
		stack := debug.Stack()
		return fmt.Sprintf("Error: %v\n\nStack trace:\n%s", err, string(stack))
	}

	// Return generic error message in production
	return "An internal error occurred. Please try again later."
}

// ShouldShowDebugEndpoints returns true if debug endpoints should be enabled
func ShouldShowDebugEndpoints() bool {
	return DebugEnabled()
}

// GetCacheHeaders returns appropriate cache headers based on the current mode
// Development: no-cache headers to ensure fresh content
// Production: cache headers for optimization
func GetCacheHeaders() map[string]string {
	if !IsProduction() {
		return map[string]string{
			"Cache-Control": "no-cache, no-store, must-revalidate",
			"Pragma":        "no-cache",
			"Expires":       "0",
		}
	}

	// Production: cache for 1 year for static assets
	return map[string]string{
		"Cache-Control": "public, max-age=31536000, immutable",
	}
}

// GetLogLevel returns the appropriate log level for the current mode
func GetLogLevel() string {
	if IsDevelopment() || IsDebug() || DebugEnabled() {
		return "debug"
	}
	return "info"
}

// ShouldCacheTemplates returns true if templates should be cached
func ShouldCacheTemplates() bool {
	return IsProduction() && !DebugEnabled()
}

// ShouldCacheStaticFiles returns true if static files should be cached
func ShouldCacheStaticFiles() bool {
	return IsProduction() && !DebugEnabled()
}

// ShouldEnableAutoReload returns true if auto-reload should be enabled
func ShouldEnableAutoReload() bool {
	return IsDevelopment() || IsDebug()
}

// ShouldEnableProfiling returns true if profiling should be enabled
func ShouldEnableProfiling() bool {
	return IsDevelopment() || IsDebug()
}

// GetPanicRecoveryDetail returns panic recovery details based on mode
// Development: full stack trace
// Production: generic error message
func GetPanicRecoveryDetail(recovered interface{}) string {
	if IsDevelopment() || IsDebug() {
		stack := debug.Stack()
		return fmt.Sprintf("Panic: %v\n\nStack trace:\n%s", recovered, string(stack))
	}

	return "Internal Server Error"
}

// String returns the string representation of the mode
func (m Mode) String() string {
	return string(m)
}
