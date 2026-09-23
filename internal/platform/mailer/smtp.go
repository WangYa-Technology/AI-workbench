// Package mailer delivers identity messages without exposing transport errors or credentials.
package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/mail"
	"strings"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	gomail "github.com/wneessen/go-mail"
)

type Sender interface {
	Send(context.Context, string, string, []byte) error
}

type SMTP struct {
	cfg config.Config
}

func New(cfg config.Config) Sender {
	if cfg.EmailDeliveryMode != "smtp" {
		return nil
	}
	return &SMTP{cfg: cfg}
}

func (s *SMTP) client() (*gomail.Client, error) {
	return gomail.NewClient(s.cfg.SMTPHost,
		gomail.WithPort(s.cfg.SMTPPort), gomail.WithSSL(),
		gomail.WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: s.cfg.SMTPHost}),
		gomail.WithSMTPAuth(gomail.SMTPAuthLogin),
		gomail.WithUsername(s.cfg.SMTPUsername), gomail.WithPassword(s.cfg.SMTPPassword),
		gomail.WithTimeout(15*time.Second))
}

func (s *SMTP) message(id, recipient string, raw []byte) (*gomail.Msg, error) {
	if id == "" || strings.ContainsAny(id, "\r\n<>@ \t") {
		return nil, errors.New("invalid email message ID")
	}
	address, err := mail.ParseAddress(recipient)
	if err != nil || address.Address != recipient || strings.ContainsAny(recipient, "\r\n") {
		return nil, errors.New("invalid email recipient")
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("invalid email message")
	}
	body, err := io.ReadAll(parsed.Body)
	if err != nil {
		return nil, errors.New("invalid email body")
	}
	m := gomail.NewMsg(gomail.WithNoDefaultUserAgent())
	if err := m.FromFormat("HCAI CHAT", s.cfg.SMTPFrom); err != nil {
		return nil, errors.New("invalid sender configuration")
	}
	if err := m.To(recipient); err != nil {
		return nil, errors.New("invalid email recipient")
	}
	m.Subject(parsed.Header.Get("Subject"))
	m.SetBodyString(gomail.TypeTextPlain, string(body))
	// Stable IDs help recipients deduplicate retries after an interrupted acknowledgement.
	m.SetMessageIDWithValue(id + "@" + strings.SplitN(s.cfg.SMTPFrom, "@", 2)[1])
	m.SetDate()
	return m, nil
}

// Check verifies TLS and authentication without sending a message.
func Check(ctx context.Context, cfg config.Config) error {
	if cfg.EmailDeliveryMode != "smtp" {
		return errors.New("smtp_not_enabled")
	}
	client, err := (&SMTP{cfg: cfg}).client()
	if err != nil {
		return errors.New("smtp_configuration_failed")
	}
	if err := client.DialWithContext(ctx); err != nil {
		return errors.New("smtp_connection_or_authentication_failed")
	}
	if err := client.Close(); err != nil {
		return errors.New("smtp_close_failed")
	}
	return nil
}

func (s *SMTP) Send(ctx context.Context, id, recipient string, raw []byte) error {
	m, err := s.message(id, recipient, raw)
	if err != nil {
		return err
	}
	client, err := s.client()
	if err != nil {
		return errors.New("smtp_configuration_failed")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := client.DialAndSendWithContext(ctx, m); err != nil {
		return errors.New("smtp_delivery_failed")
	}
	return nil
}
