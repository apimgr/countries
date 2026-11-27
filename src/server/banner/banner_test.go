package banner

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPrintServerStartupBannerPlainFirstRun(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = writer
	PrintServerStartupBanner(ServerBannerInfo{
		AppName: "countries", Version: "1.2.3", AppMode: "development",
		StartupDatetime: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC).Format("January 02, 2006 at 15:04:05 MST"),
		URLs:            []string{"https://example.test"},
		OnionAddress:    "example.onion", I2PAddress: "example.b32.i2p", SMTPAddress: "mail.example.test:587",
		SetupToken: "setup-token",
	})
	os.Stdout = previous
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	for _, expected := range []string{"countries v1.2.3", "Listening: https://example.test", "Started:", "Tor: http://example.onion", "I2P: http://example.b32.i2p", "SMTP: mail.example.test:587", "Setup token: setup-token"} {
		if !strings.Contains(string(output), expected) {
			t.Errorf("banner output %q does not contain %q", output, expected)
		}
	}
}
