package mode

import (
	"errors"
	"testing"
)

// Restore the default mode after every test so package-level state does
// not leak between tests (currentMode is a shared package var).
func resetMode(t *testing.T) {
	t.Helper()
	mu.Lock()
	currentMode = Production
	debugEnabled = false
	mu.Unlock()
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{"dev", Development, false},
		{"development", Development, false},
		{"prod", Production, false},
		{"production", Production, false},
		{"debug", Debug, false},
		{"", "", true},
		{"bogus", "", true},
		// Parsing must be case-sensitive and must not match uppercase input.
		{"PRODUCTION", "", true},
	}

	for _, c := range cases {
		got, err := ParseMode(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseMode(%q) expected error, got nil", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMode(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseMode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSet_ValidAndInvalid(t *testing.T) {
	defer resetMode(t)

	if err := Set("development"); err != nil {
		t.Fatalf("Set(development) unexpected error: %v", err)
	}
	if Get() != Development {
		t.Errorf("Get() = %q, want %q", Get(), Development)
	}

	// An invalid Set must leave the mode unchanged, not corrupt it.
	if err := Set("nonsense"); err == nil {
		t.Error("Set(nonsense) expected error, got nil")
	}
	if Get() != Development {
		t.Errorf("Get() after failed Set = %q, want unchanged %q", Get(), Development)
	}
}

func TestIsDevelopmentIsProduction(t *testing.T) {
	defer resetMode(t)

	if err := Set("production"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if !IsProduction() || IsDevelopment() {
		t.Error("expected IsProduction=true, IsDevelopment=false after Set(production)")
	}

	if err := Set("development"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if IsProduction() || !IsDevelopment() {
		t.Error("expected IsProduction=false, IsDevelopment=true after Set(development)")
	}
	if err := Set("debug"); err != nil {
		t.Fatalf("Set(debug): %v", err)
	}
	if !IsDebug() || IsProduction() {
		t.Error("expected IsDebug=true and IsProduction=false after Set(debug)")
	}
}

func TestInitialize_Priority(t *testing.T) {
	defer resetMode(t)

	// Priority 1: CLI flag wins over env var.
	t.Setenv("MODE", "production")
	if err := Initialize("development"); err != nil {
		t.Fatalf("Initialize() error: %v", err)
	}
	if Get() != Development {
		t.Errorf("Initialize() with CLI flag = %q, want %q (CLI should win)", Get(), Development)
	}

	// Priority 2: env var used when CLI flag empty.
	t.Setenv("MODE", "development")
	if err := Initialize(""); err != nil {
		t.Fatalf("Initialize() error: %v", err)
	}
	if Get() != Development {
		t.Errorf("Initialize() with env var = %q, want %q", Get(), Development)
	}

	// Priority 3: default to production when neither is set.
	t.Setenv("MODE", "")
	if err := Initialize(""); err != nil {
		t.Fatalf("Initialize() error: %v", err)
	}
	if Get() != Production {
		t.Errorf("Initialize() with no input = %q, want default %q", Get(), Production)
	}
	if err := Initialize("debug"); err != nil {
		t.Fatalf("Initialize(debug) error: %v", err)
	}
	if Get() != Debug || !DebugEnabled() {
		t.Errorf("Initialize(debug) = mode %q, debug %v; want debug mode with diagnostics enabled", Get(), DebugEnabled())
	}
	SetDebugEnabled(false)
	if DebugEnabled() {
		t.Error("explicit debug disable should override debug mode default")
	}
}

func TestInitialize_InvalidPropagatesError(t *testing.T) {
	defer resetMode(t)
	if err := Initialize("garbage"); err == nil {
		t.Error("Initialize(garbage) expected error, got nil")
	}
}

func TestGetErrorDetail(t *testing.T) {
	defer resetMode(t)

	if got := GetErrorDetail(nil); got != "" {
		t.Errorf("GetErrorDetail(nil) = %q, want empty string", got)
	}

	testErr := errors.New("boom")

	if err := Set("production"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if got := GetErrorDetail(testErr); got != "An internal error occurred. Please try again later." {
		t.Errorf("GetErrorDetail() in production = %q, want generic message", got)
	}

	if err := Set("development"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	got := GetErrorDetail(testErr)
	if got == "" {
		t.Error("GetErrorDetail() in development returned empty string")
	}
	if got == "An internal error occurred. Please try again later." {
		t.Error("GetErrorDetail() in development should not return the generic production message")
	}
}

func TestShouldShowDebugEndpoints(t *testing.T) {
	defer resetMode(t)

	if err := Set("production"); err != nil {
		t.Fatalf("Set(production): %v", err)
	}
	if ShouldShowDebugEndpoints() {
		t.Error("ShouldShowDebugEndpoints() should be false in production")
	}
	if err := Set("development"); err != nil {
		t.Fatalf("Set(development): %v", err)
	}
	if ShouldShowDebugEndpoints() {
		t.Error("ShouldShowDebugEndpoints() should be false in development without debug enabled")
	}
	SetDebugEnabled(true)
	if !ShouldShowDebugEndpoints() {
		t.Error("ShouldShowDebugEndpoints() should be true when debug is enabled")
	}
}

func TestGetCacheHeaders(t *testing.T) {
	defer resetMode(t)

	if err := Set("development"); err != nil {
		t.Fatalf("Set(development): %v", err)
	}
	devHeaders := GetCacheHeaders()
	if devHeaders["Cache-Control"] != "no-cache, no-store, must-revalidate" {
		t.Errorf("dev Cache-Control = %q, want no-cache directive", devHeaders["Cache-Control"])
	}

	if err := Set("production"); err != nil {
		t.Fatalf("Set(production): %v", err)
	}
	prodHeaders := GetCacheHeaders()
	if prodHeaders["Cache-Control"] != "public, max-age=31536000, immutable" {
		t.Errorf("prod Cache-Control = %q, want long-lived cache directive", prodHeaders["Cache-Control"])
	}
}

func TestGetLogLevel(t *testing.T) {
	defer resetMode(t)

	if err := Set("development"); err != nil {
		t.Fatalf("Set(development): %v", err)
	}
	if GetLogLevel() != "debug" {
		t.Errorf("GetLogLevel() in dev = %q, want debug", GetLogLevel())
	}
	if err := Set("production"); err != nil {
		t.Fatalf("Set(production): %v", err)
	}
	if GetLogLevel() != "info" {
		t.Errorf("GetLogLevel() in prod = %q, want info", GetLogLevel())
	}
}

func TestShouldCacheTemplatesAndStaticFiles(t *testing.T) {
	defer resetMode(t)

	if err := Set("production"); err != nil {
		t.Fatalf("Set(production): %v", err)
	}
	if !ShouldCacheTemplates() || !ShouldCacheStaticFiles() {
		t.Error("expected caching enabled in production")
	}

	if err := Set("development"); err != nil {
		t.Fatalf("Set(development): %v", err)
	}
	if ShouldCacheTemplates() || ShouldCacheStaticFiles() {
		t.Error("expected caching disabled in development")
	}
}

func TestShouldEnableAutoReloadAndProfiling(t *testing.T) {
	defer resetMode(t)

	if err := Set("development"); err != nil {
		t.Fatalf("Set(development): %v", err)
	}
	if !ShouldEnableAutoReload() || !ShouldEnableProfiling() {
		t.Error("expected auto-reload and profiling enabled in development")
	}

	if err := Set("production"); err != nil {
		t.Fatalf("Set(production): %v", err)
	}
	if ShouldEnableAutoReload() || ShouldEnableProfiling() {
		t.Error("expected auto-reload and profiling disabled in production")
	}
}

func TestGetPanicRecoveryDetail(t *testing.T) {
	defer resetMode(t)

	if err := Set("production"); err != nil {
		t.Fatalf("Set(production): %v", err)
	}
	if got := GetPanicRecoveryDetail("oops"); got != "Internal Server Error" {
		t.Errorf("GetPanicRecoveryDetail() in prod = %q, want generic message", got)
	}

	if err := Set("development"); err != nil {
		t.Fatalf("Set(development): %v", err)
	}
	got := GetPanicRecoveryDetail("oops")
	if got == "Internal Server Error" || got == "" {
		t.Errorf("GetPanicRecoveryDetail() in dev = %q, want detailed panic output", got)
	}
}

func TestModeString(t *testing.T) {
	if Development.String() != "development" {
		t.Errorf("Development.String() = %q, want development", Development.String())
	}
	if Production.String() != "production" {
		t.Errorf("Production.String() = %q, want production", Production.String())
	}
}

// Concurrent Get/Set should never race or panic; run with -race to verify.
func TestConcurrentAccess(t *testing.T) {
	defer resetMode(t)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			if err := Set("development"); err != nil {
				t.Errorf("Set(development): %v", err)
			}
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 100; i++ {
			if err := Set("production"); err != nil {
				t.Errorf("Set(production): %v", err)
			}
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 100; i++ {
			_ = Get()
		}
		done <- struct{}{}
	}()

	<-done
	<-done
	<-done
}
