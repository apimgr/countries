package logbuffer

import (
	"strings"
	"testing"
	"time"
)

func TestBufferRedactsCredentialsAndHonorsRecentLimits(t *testing.T) {
	buffer := New()
	buffer.Add("ERROR", `request failed password="private phrase" Authorization: Bearer very-secret mtr_0123456789abcdefghijklmnopqrstuv`)
	buffer.Add("info", "older event")
	entries := buffer.Recent(1, time.Hour)
	if len(entries) != 1 || entries[0].Message != "older event" {
		t.Fatalf("Recent(1) = %+v, want only newest event", entries)
	}
	entries = buffer.Recent(10, time.Hour)
	if len(entries) != 2 || entries[0].Level != "error" {
		t.Fatalf("Recent() = %+v, want chronological events with normalized level", entries)
	}
	for _, secret := range []string{"private phrase", "very-secret", "mtr_0123456789abcdefghijklmnopqrstuv"} {
		if strings.Contains(entries[0].Message, secret) {
			t.Errorf("stored message contains credential %q: %s", secret, entries[0].Message)
		}
	}
}
