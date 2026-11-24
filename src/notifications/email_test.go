package notifications

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFormatMessageRFC5322AndMIME(t *testing.T) {
	message := Message{
		FromName: "Café Countries", FromEmail: "reports@example.test", To: "ops@example.test",
		ReplyTo: "help@example.test", Subject: "Résumé: été", Text: "Plain text body",
		HTML: "<p>HTML body</p>", FQDN: "api.example.test",
	}
	raw, err := formatMessage(message, message.FromEmail)
	if err != nil {
		t.Fatalf("formatMessage() error = %v", err)
	}
	for scanner := bufio.NewScanner(bytes.NewReader(raw)); scanner.Scan(); {
		if len(scanner.Bytes()) > 998 {
			t.Fatalf("message line has %d octets", len(scanner.Bytes()))
		}
	}
	if err := scannerError(raw); err != nil {
		t.Fatal(err)
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("mail.ReadMessage() error = %v", err)
	}
	if parsed.Header.Get("MIME-Version") != "1.0" || parsed.Header.Get("Auto-Submitted") != "auto-generated" {
		t.Fatalf("required headers missing: %#v", parsed.Header)
	}
	if _, err := mail.ParseDate(parsed.Header.Get("Date")); err != nil {
		t.Fatalf("invalid Date header: %v", err)
	}
	if !strings.Contains(parsed.Header.Get("Message-ID"), "@api.example.test>") {
		t.Fatalf("Message-ID = %q", parsed.Header.Get("Message-ID"))
	}
	from, err := mail.ParseAddress(parsed.Header.Get("From"))
	if err != nil || from.Name != message.FromName || from.Address != message.FromEmail {
		t.Fatalf("From = %+v, error = %v", from, err)
	}
	wordDecoder := new(mime.WordDecoder)
	decodedSubject, err := wordDecoder.DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || decodedSubject != message.Subject {
		t.Fatalf("decoded Subject = %q, error = %v", decodedSubject, err)
	}
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q, error = %v", mediaType, err)
	}
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	plain, err := reader.NextPart()
	if err != nil {
		t.Fatalf("first MIME part: %v", err)
	}
	if plain.Header.Get("Content-Type") != "text/plain; charset=UTF-8" {
		t.Fatalf("first part Content-Type = %q", plain.Header.Get("Content-Type"))
	}
	decodedPlain, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, plain))
	if err != nil || string(decodedPlain) != message.Text {
		t.Fatalf("plain part = %q, error = %v", decodedPlain, err)
	}
	if len(strings.TrimSpace(string(decodedPlain))) == 0 {
		t.Fatal("plain-text MIME part is empty")
	}
	htmlPart, err := reader.NextPart()
	if err != nil {
		t.Fatalf("second MIME part: %v", err)
	}
	if htmlPart.Header.Get("Content-Type") != "text/html; charset=UTF-8" {
		t.Fatalf("second part Content-Type = %q", htmlPart.Header.Get("Content-Type"))
	}
}

func TestSendRejectsHeaderNewlinesBeforeConnecting(t *testing.T) {
	base := Message{FromName: "Countries", FromEmail: "from@example.test", To: "to@example.test", Subject: "Subject", Text: "Body", HTML: "<p>Body</p>", FQDN: "example.test"}
	for _, field := range []string{"name", "recipient", "subject", "fqdn"} {
		t.Run(field, func(t *testing.T) {
			message := base
			switch field {
			case "name":
				message.FromName = "Bad\r\nBcc: victim@example.test"
			case "recipient":
				message.To = "bad@example.test\nBcc: victim@example.test"
			case "subject":
				message.Subject = "Bad\r\nBcc: victim@example.test"
			case "fqdn":
				message.FQDN = "example.test\r\nBcc: victim@example.test"
			}
			sender := Sender{Config: SMTPConfig{Host: "127.0.0.1", Port: 1, TLS: "none"}}
			if err := sender.Send(message); err == nil || !strings.Contains(err.Error(), "CR or LF") {
				t.Fatalf("Send() error = %v, want CR/LF rejection before network access", err)
			}
		})
	}
}

func TestSenderChecksAndSendsThroughSMTP(t *testing.T) {
	for _, test := range []struct {
		name string
		send bool
	}{
		{name: "connection check"},
		{name: "message delivery", send: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := listener.Close(); err != nil {
					t.Errorf("close SMTP test listener: %v", err)
				}
			})
			port, err := strconv.Atoi(strings.TrimPrefix(listener.Addr().String(), "127.0.0.1:"))
			if err != nil {
				t.Fatal(err)
			}
			serverResult := make(chan string, 1)
			go serveTestSMTP(listener, serverResult)
			sender := Sender{Config: SMTPConfig{Host: "127.0.0.1", Port: port, TLS: "none", Timeout: time.Second}}
			if test.send {
				err = sender.Send(Message{
					FromName: "Countries", FromEmail: "from@example.test", To: "to@example.test",
					Subject: "SMTP test", Text: "Plain body", HTML: "<p>HTML body</p>", FQDN: "api.example.test",
				})
			} else {
				err = sender.Check()
			}
			if err != nil {
				t.Fatalf("SMTP operation failed: %v", err)
			}
			if got := <-serverResult; test.send && !strings.Contains(got, "Subject: SMTP test") {
				t.Fatalf("SMTP server received message without expected subject: %q", got)
			}
		})
	}
}

func TestSenderRejectsInvalidConfigurationAndMessage(t *testing.T) {
	if err := (Sender{}).Check(); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Check() with no SMTP configuration = %v", err)
	}
	if _, err := (Sender{Config: SMTPConfig{Host: "localhost", Port: 25, TLS: "invalid"}}).connect(); err == nil || !strings.Contains(err.Error(), "TLS mode") {
		t.Fatalf("connect() with invalid TLS mode = %v", err)
	}
	sender := Sender{Config: SMTPConfig{Host: "127.0.0.1", Port: 25, TLS: "none"}}
	for _, test := range []struct {
		name    string
		message Message
		want    string
	}{
		{name: "invalid sender", message: Message{FromEmail: "bad address", To: "to@example.test"}, want: "sender address"},
		{name: "invalid recipient", message: Message{FromEmail: "from@example.test", To: "bad address"}, want: "recipient address"},
		{name: "missing subject", message: Message{FromEmail: "from@example.test", To: "to@example.test", Text: "Body", HTML: "<p>Body</p>"}, want: "subject and plain text"},
		{name: "missing text", message: Message{FromEmail: "from@example.test", To: "to@example.test", Subject: "Subject", HTML: "<p>Body</p>"}, want: "subject and plain text"},
		{name: "missing HTML", message: Message{FromEmail: "from@example.test", To: "to@example.test", Subject: "Subject", Text: "Body"}, want: "HTML alternative"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := sender.Send(test.message); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Send() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestTLSOptionsOnlyRelaxPrivateIPVerification(t *testing.T) {
	for _, test := range []struct {
		host    string
		skipped bool
	}{
		{host: "127.0.0.1", skipped: true},
		{host: "10.0.0.1", skipped: true},
		{host: "172.16.0.1", skipped: true},
		{host: "192.168.1.1", skipped: true},
		{host: "100.64.0.1", skipped: true},
		{host: "169.254.0.1", skipped: true},
		{host: "::1", skipped: true},
		{host: "fd00::1", skipped: true},
		{host: "fe80::1", skipped: true},
		{host: "203.0.113.1"},
		{host: "mail.example.test"},
	} {
		tlsConfig := tlsOptions(test.host)
		if tlsConfig.InsecureSkipVerify != test.skipped {
			t.Errorf("tlsOptions(%q).InsecureSkipVerify = %t, want %t", test.host, tlsConfig.InsecureSkipVerify, test.skipped)
		}
		if tlsConfig.MinVersion != tls.VersionTLS12 || tlsConfig.ServerName != test.host {
			t.Errorf("tlsOptions(%q) = %+v", test.host, tlsConfig)
		}
	}
}

func serveTestSMTP(listener net.Listener, result chan<- string) {
	connection, err := listener.Accept()
	if err != nil {
		result <- "accept failed: " + err.Error()
		return
	}
	var message strings.Builder
	defer func() {
		if err := connection.Close(); err != nil {
			message.WriteString("\nclose failed: ")
			message.WriteString(err.Error())
		}
		result <- message.String()
	}()
	reader := bufio.NewReader(connection)
	_, _ = fmt.Fprint(connection, "220 smtp.example.test ESMTP\r\n")
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			message.WriteString("read failed: ")
			message.WriteString(readErr.Error())
			return
		}
		command := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(command, "EHLO "):
			_, _ = fmt.Fprint(connection, "250-smtp.example.test\r\n250 OK\r\n")
		case strings.HasPrefix(command, "MAIL FROM:") || strings.HasPrefix(command, "RCPT TO:"):
			_, _ = fmt.Fprint(connection, "250 OK\r\n")
		case command == "DATA":
			_, _ = fmt.Fprint(connection, "354 send message\r\n")
			for {
				dataLine, dataErr := reader.ReadString('\n')
				if dataErr != nil {
					message.WriteString("read message failed: ")
					message.WriteString(dataErr.Error())
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				message.WriteString(dataLine)
			}
			_, _ = fmt.Fprint(connection, "250 queued\r\n")
		case command == "QUIT":
			_, _ = fmt.Fprint(connection, "221 bye\r\n")
			return
		default:
			_, _ = fmt.Fprint(connection, "500 unsupported command\r\n")
		}
	}
}

func scannerError(raw []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		if len(scanner.Bytes()) > 998 {
			return io.ErrShortBuffer
		}
	}
	return scanner.Err()
}
