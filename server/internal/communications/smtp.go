package communications

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

var ErrMailNotConfigured = errors.New("host SMTP is not configured")
var ErrMailDeliveryUnknown = errors.New("mail delivery could not be confirmed; do not automatically resend")

type SMTPConfig struct {
	Host, Port, Username, Password, From string
	ImplicitTLS                          bool
}

func SMTPConfigFromEnvValues(values map[string]string) SMTPConfig {
	port := values["SMTP_PORT"]
	if port == "" {
		port = "465"
	}
	return SMTPConfig{Host: values["SMTP_HOST"], Port: port, Username: values["SMTP_USERNAME"], Password: values["SMTP_PASSWORD"], From: values["SMTP_FROM_EMAIL"], ImplicitTLS: port == "465" || values["SMTP_TLS_MODE"] == "implicit"}
}

type Mail struct{ Recipient, Subject, Body string }
type SMTPSender struct {
	cfg       SMTPConfig
	tlsConfig *tls.Config
}

func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	port, err := strconv.Atoi(cfg.Port)
	if err != nil || port < 1 || port > 65535 || cfg.Host == "" || cfg.Username == "" || cfg.Password == "" || !plainAddress(cfg.From) {
		return nil, ErrMailNotConfigured
	}
	return &SMTPSender{cfg: cfg, tlsConfig: &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}}, nil
}
func plainAddress(value string) bool {
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value && !strings.ContainsAny(value, "\r\n") && len(value) <= 320
}
func (s *SMTPSender) Send(ctx context.Context, message Mail) error {
	if !plainAddress(message.Recipient) || message.Subject == "" || len(message.Subject) > 256 || len(message.Body) > 64<<10 || strings.ContainsAny(message.Subject, "\r\n\x00") || strings.ContainsRune(message.Body, 0) {
		return ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(s.cfg.Host, s.cfg.Port))
	if err != nil {
		return errors.New("SMTP connection failed")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return err
		}
	}
	var stream net.Conn = conn
	if s.cfg.ImplicitTLS {
		secure := tls.Client(conn, s.tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return errors.New("SMTP TLS handshake failed")
		}
		stream = secure
	}
	client, err := smtp.NewClient(stream, s.cfg.Host)
	if err != nil {
		return errors.New("SMTP greeting failed")
	}
	defer client.Close()
	if !s.cfg.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP requires STARTTLS")
		}
		if err := client.StartTLS(s.tlsConfig); err != nil {
			return errors.New("SMTP STARTTLS failed")
		}
	}
	if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
		return errors.New("SMTP authentication failed")
	}
	if err := client.Mail(s.cfg.From); err != nil {
		return errors.New("SMTP sender rejected")
	}
	if err := client.Rcpt(message.Recipient); err != nil {
		return errors.New("SMTP recipient rejected")
	}
	writer, err := client.Data()
	if err != nil {
		return errors.New("SMTP DATA rejected")
	}
	body := strings.ReplaceAll(strings.ReplaceAll(message.Body, "\r\n", "\n"), "\n", "\r\n")
	raw := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", s.cfg.From, message.Recipient, mime.QEncoding.Encode("utf-8", message.Subject), body)
	if _, err := writer.Write([]byte(raw)); err != nil {
		return ErrMailDeliveryUnknown
	}
	if err := writer.Close(); err != nil {
		return ErrMailDeliveryUnknown
	}
	// DATA acknowledgement is the delivery boundary; QUIT failure must not
	// encourage duplicate delivery after the server accepted the message.
	return nil
}
