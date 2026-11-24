// Package notifications sends configured email notifications and validates templates.
package notifications

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/netip"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

type SMTPConfig struct {
	Host, Username, Password, TLS string
	Port                          int
	Timeout                       time.Duration
}

type Message struct {
	FromName, FromEmail, To, ReplyTo, Subject string
	Text, HTML, FQDN                          string
}

type Sender struct {
	Config SMTPConfig
}

func (s Sender) Check() error {
	client, err := s.connect()
	if err != nil {
		return err
	}
	return client.Quit()
}

func (s Sender) Send(message Message) (resultErr error) {
	if s.Config.Host == "" || s.Config.Port < 1 || s.Config.Port > 65535 {
		return fmt.Errorf("SMTP is not configured")
	}
	if hasNewline(message.FromName) || hasNewline(message.FromEmail) || hasNewline(message.To) || hasNewline(message.ReplyTo) || hasNewline(message.Subject) || hasNewline(message.FQDN) {
		return fmt.Errorf("email headers cannot contain CR or LF")
	}
	from, err := mail.ParseAddress(message.FromEmail)
	if err != nil || from.Address != message.FromEmail {
		return fmt.Errorf("invalid sender address")
	}
	to, err := mail.ParseAddress(message.To)
	if err != nil || to.Address != message.To {
		return fmt.Errorf("invalid recipient address")
	}
	if strings.TrimSpace(message.Subject) == "" || strings.TrimSpace(message.Text) == "" {
		return fmt.Errorf("email subject and plain text body are required")
	}
	if message.HTML == "" {
		return fmt.Errorf("HTML alternative is required")
	}
	client, err := s.connect()
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if closed {
			return
		}
		if closeErr := client.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close SMTP connection: %w", closeErr))
		}
	}()
	if s.Config.Username != "" {
		if ok, _ := client.Extension("AUTH"); !ok || s.Config.TLS == "none" {
			return fmt.Errorf("SMTP authentication requires an encrypted connection")
		}
		if err := client.Auth(smtp.PlainAuth("", s.Config.Username, s.Config.Password, s.Config.Host)); err != nil {
			return err
		}
	}
	raw, err := formatMessage(message, from.Address)
	if err != nil {
		return err
	}
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(to.Address); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	err = client.Quit()
	closed = true
	return err
}

func (s Sender) connect() (*smtp.Client, error) {
	if s.Config.Host == "" || s.Config.Port < 1 || s.Config.Port > 65535 {
		return nil, fmt.Errorf("SMTP is not configured")
	}
	host := strings.TrimSpace(s.Config.Host)
	mode := strings.ToLower(strings.TrimSpace(s.Config.TLS))
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto", "tls", "starttls", "none":
	default:
		return nil, fmt.Errorf("invalid SMTP TLS mode")
	}
	address := net.JoinHostPort(host, fmt.Sprint(s.Config.Port))
	timeout := s.Config.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	var err error
	if mode == "tls" || mode == "auto" && s.Config.Port == 465 {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, tlsOptions(host))
	} else {
		conn, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if mode == "starttls" || mode == "auto" && s.Config.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsOptions(host)); err != nil {
				_ = client.Close()
				return nil, err
			}
		} else if mode == "starttls" || s.Config.Username != "" {
			_ = client.Close()
			return nil, fmt.Errorf("SMTP server does not support required STARTTLS")
		}
	}
	return client, nil
}

func tlsOptions(host string) *tls.Config {
	options := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if ip, err := netip.ParseAddr(host); err == nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		options.InsecureSkipVerify = true
	}
	if ip, err := netip.ParseAddr(host); err == nil && ip.Is4() && netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
		options.InsecureSkipVerify = true
	}
	return options
}

func formatMessage(message Message, envelopeFrom string) ([]byte, error) {
	if hasNewline(message.FQDN) {
		return nil, fmt.Errorf("email headers cannot contain CR or LF")
	}
	from, err := mail.ParseAddress((&mail.Address{Name: message.FromName, Address: envelopeFrom}).String())
	if err != nil {
		return nil, err
	}
	if message.ReplyTo != "" {
		if parsed, err := mail.ParseAddress(message.ReplyTo); err != nil || parsed.Address != message.ReplyTo {
			return nil, fmt.Errorf("invalid reply-to address")
		}
	}
	if message.FQDN == "" {
		message.FQDN = "localhost"
	}
	messageIDDomain, err := mail.ParseAddress("notification@" + message.FQDN)
	if err != nil || messageIDDomain.Address != "notification@"+message.FQDN {
		return nil, fmt.Errorf("invalid message-id domain")
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	boundary := "countries-" + base64.RawURLEncoding.EncodeToString(random)
	var output bytes.Buffer
	writeHeader := func(key, value string) error {
		name := textproto.CanonicalMIMEHeaderKey(key)
		prefix := name + ": "
		line := prefix
		for len(value) > 0 {
			available := 78 - len(line)
			if len(value) <= available {
				line += value
				value = ""
				break
			}
			cut := strings.LastIndex(value[:min(len(value), available+1)], " ")
			if cut <= 0 {
				if len(prefix)+len(value) > 998 {
					return fmt.Errorf("email header exceeds 998 octets")
				}
				line += value
				value = ""
				break
			}
			line += value[:cut]
			output.WriteString(line + "\r\n")
			value = strings.TrimLeft(value[cut+1:], " ")
			line = " "
		}
		if len(line) > 998 {
			return fmt.Errorf("email header exceeds 998 octets")
		}
		output.WriteString(line + "\r\n")
		return nil
	}
	if err := writeHeader("From", from.String()); err != nil {
		return nil, err
	}
	if err := writeHeader("To", message.To); err != nil {
		return nil, err
	}
	if message.ReplyTo != "" {
		if err := writeHeader("Reply-To", message.ReplyTo); err != nil {
			return nil, err
		}
	}
	for _, header := range [][2]string{
		{"Subject", mime.QEncoding.Encode("utf-8", message.Subject)},
		{"Date", time.Now().Format(time.RFC1123Z)},
		{"Message-ID", fmt.Sprintf("<%s@%s>", base64.RawURLEncoding.EncodeToString(random), message.FQDN)},
		{"MIME-Version", "1.0"},
		{"Auto-Submitted", "auto-generated"},
		{"Content-Type", "multipart/alternative; boundary=\"" + boundary + "\""},
	} {
		if err := writeHeader(header[0], header[1]); err != nil {
			return nil, err
		}
	}
	output.WriteString("\r\n--" + boundary + "\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	output.WriteString(wrapBase64([]byte(message.Text)) + "\r\n--" + boundary + "\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	output.WriteString(wrapBase64([]byte(message.HTML)) + "\r\n--" + boundary + "--\r\n")
	return output.Bytes(), nil
}

func wrapBase64(value []byte) string {
	encoded := base64.StdEncoding.EncodeToString(value)
	var wrapped strings.Builder
	for len(encoded) > 76 {
		wrapped.WriteString(encoded[:76])
		wrapped.WriteString("\r\n")
		encoded = encoded[76:]
	}
	wrapped.WriteString(encoded)
	return wrapped.String()
}

func hasNewline(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}

func RenderTemplate(template string, values map[string]string) (string, string, error) {
	separator := strings.Index(template, "\n---\n")
	if separator < 0 {
		return "", "", fmt.Errorf("email template requires Subject and --- separator")
	}
	header := strings.TrimSuffix(template[:separator], "\r")
	if !strings.HasPrefix(header, "Subject: ") {
		return "", "", fmt.Errorf("email template must begin with a Subject header")
	}
	subject := header[len("Subject: "):]
	body := template[separator+5:]
	for name, value := range values {
		subject = strings.ReplaceAll(subject, "{"+name+"}", value)
		body = strings.ReplaceAll(body, "{"+name+"}", value)
	}
	return subject, strings.TrimSpace(body), nil
}
