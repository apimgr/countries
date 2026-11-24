// Package format provides shared presentation formatting for user-facing values.
package format

import (
	"fmt"
	"strings"
	"time"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// Pluralizer selects a localized plural message and returns it with {count} intact.
type Pluralizer func(lang, key string, count int) string

// Duration returns a localized duration using the largest unit and at most one smaller unit.
// The pluralizer receives keys such as format_hours and the numeric count for CLDR selection.
func Duration(value time.Duration, lang string, pluralize Pluralizer) string {
	if value < 0 {
		value = 0
	}
	seconds := int64(value / time.Second)
	units := make([]durationUnit, 0, 2)
	switch {
	case seconds >= 24*60*60:
		days := seconds / (24 * 60 * 60)
		seconds %= 24 * 60 * 60
		units = append(units, durationUnit{name: "days", count: days})
		if hours := seconds / 3600; hours > 0 {
			units = append(units, durationUnit{name: "hours", count: hours})
		}
	case seconds >= 60*60:
		hours := seconds / 3600
		seconds %= 3600
		units = append(units, durationUnit{name: "hours", count: hours})
		if minutes := seconds / 60; minutes > 0 {
			units = append(units, durationUnit{name: "minutes", count: minutes})
		}
	case seconds >= 60:
		minutes := seconds / 60
		seconds %= 60
		units = append(units, durationUnit{name: "minutes", count: minutes})
		if seconds > 0 {
			units = append(units, durationUnit{name: "seconds", count: seconds})
		}
	default:
		units = append(units, durationUnit{name: "seconds", count: seconds})
	}

	parts := make([]string, 0, len(units))
	for _, unit := range units {
		key := "format_" + unit.name
		message := ""
		if pluralize != nil {
			message = pluralize(lang, key, int(unit.count))
		}
		if message == "" || message == key {
			message = englishDurationUnit(unit.name, unit.count)
		}
		message = strings.ReplaceAll(message, "{count}", fmt.Sprint(unit.count))
		parts = append(parts, message)
	}
	return strings.Join(parts, " ")
}

// Count formats an integer with locale-appropriate grouping separators.
func Count(value int64, lang string) string {
	tag, err := language.Parse(lang)
	if err != nil {
		tag = language.English
	}
	return message.NewPrinter(tag).Sprintf("%d", value)
}

type durationUnit struct {
	name  string
	count int64
}

func englishDurationUnit(unit string, count int64) string {
	if count == 1 {
		return "1 " + strings.TrimSuffix(unit, "s")
	}
	return fmt.Sprintf("%d %s", count, unit)
}
