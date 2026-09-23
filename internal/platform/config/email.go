package config

import (
	"fmt"
	"net/mail"
	"os"
	"strconv"
	"strings"
)

func validateEmail(cfg *Config) error {
	if cfg.EmailDeliveryMode != "smtp" && cfg.EmailDeliveryMode != "local_file" && cfg.EmailDeliveryMode != "disabled" {
		return fmt.Errorf("EMAIL_DELIVERY_MODE must be smtp, local_file or disabled")
	}
	if cfg.EmailDeliveryMode != "smtp" {
		return nil
	}
	if cfg.SMTPHost == "" || strings.ContainsAny(cfg.SMTPHost, "\r\n /:") {
		return fmt.Errorf("SMTP_HOST must be a hostname")
	}
	port, err := strconv.Atoi(value("SMTP_PORT", "465"))
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("SMTP_PORT must be between 1 and 65535")
	}
	cfg.SMTPPort = port
	if cfg.SMTPUsername == "" || cfg.SMTPPassword == "" {
		return fmt.Errorf("SMTP_USERNAME and SMTP_PASSWORD are required")
	}
	address, err := mail.ParseAddress(cfg.SMTPFrom)
	if err != nil || address.Address != cfg.SMTPFrom || strings.ContainsAny(cfg.SMTPFrom, "\r\n") {
		return fmt.Errorf("SMTP_FROM must be a bare email address")
	}
	if os.Getenv("EMAIL_ACTION_ENCRYPTION_KEY_B64") == "" {
		return fmt.Errorf("EMAIL_ACTION_ENCRYPTION_KEY_B64 is required for SMTP delivery")
	}
	return nil
}
