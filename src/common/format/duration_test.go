package format

import (
	"fmt"
	"testing"
	"time"
)

func TestDurationUsesLargestUnitsAndAtMostTwoParts(t *testing.T) {
	for _, test := range []struct {
		name  string
		value time.Duration
		want  string
	}{
		{"zero", 0, "0 seconds"},
		{"under minute", 45 * time.Second, "45 seconds"},
		{"single minute", 3 * time.Minute, "3 minutes"},
		{"minutes and seconds", 2*time.Minute + 5*time.Second, "2 minutes 5 seconds"},
		{"single hour", 2 * time.Hour, "2 hours"},
		{"hours and minutes", time.Hour + 30*time.Minute, "1 hour 30 minutes"},
		{"days and hours", 3*24*time.Hour + 4*time.Hour + 59*time.Minute, "3 days 4 hours"},
		{"negative duration", -time.Second, "0 seconds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Duration(test.value, "en", nil); got != test.want {
				t.Errorf("Duration(%s) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestDurationUsesCLDRPluralizerMessages(t *testing.T) {
	pluralizer := func(lang, key string, count int) string {
		if lang != "ar" {
			t.Errorf("pluralizer language = %q, want ar", lang)
		}
		category := "other"
		if count == 1 {
			category = "one"
		}
		return fmt.Sprintf("%s[%s]={count}", key, category)
	}
	if got := Duration(2*time.Hour+time.Minute, "ar", pluralizer); got != "format_hours[other]=2 format_minutes[one]=1" {
		t.Fatalf("Duration() = %q, want pluralized unit messages", got)
	}
}

func TestCountUsesLocaleGrouping(t *testing.T) {
	for _, test := range []struct {
		lang string
		want string
	}{
		{"en", "1,234,567"},
		{"de", "1.234.567"},
	} {
		if got := Count(1234567, test.lang); got != test.want {
			t.Errorf("Count(1234567, %q) = %q, want %q", test.lang, got, test.want)
		}
	}
}
