package mailer

import (
	"bytes"
	"context"
	"io"
	"log"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
)

func TestSMTPMessageUsesConfiguredSenderAndPreservesUnicode(t *testing.T) {
	s := &SMTP{cfg: config.Config{SMTPFrom: "support@example.com"}}
	raw := []byte("From: no-reply@local.invalid\r\nSubject: 登录验证码\r\n\r\n你的验证码：123456\r\n")
	m, err := s.message("message-123", "recipient@example.com", raw)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := m.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	parsed, err := mail.ReadMessage(&buf)
	if err != nil {
		t.Fatal(err)
	}
	from, err := mail.ParseAddress(parsed.Header.Get("From"))
	if err != nil || from.Address != s.cfg.SMTPFrom {
		t.Fatal("incorrect From")
	}
	subject, err := (&mime.WordDecoder{}).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || subject != "登录验证码" {
		t.Fatal("incorrect Unicode subject")
	}
	body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	if err != nil || !strings.Contains(string(body), "你的验证码：123456") {
		t.Fatal("incorrect Unicode body")
	}
	if parsed.Header.Get("Message-Id") != "<message-123@example.com>" {
		t.Fatal("unstable message ID")
	}
	if _, err := s.message("bad\r\nID", "recipient@example.com", raw); err == nil {
		t.Fatal("unsafe message ID accepted")
	}
	if _, err := s.message("valid", "recipient@example.com\r\nBcc: x@example.com", raw); err == nil {
		t.Fatal("unsafe recipient accepted")
	}
}

func TestSMTPRejectsUntrustedTLSAndSanitizesErrors(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	host, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(port)
	cfg := config.Config{EmailDeliveryMode: "smtp", SMTPHost: host, SMTPPort: n, SMTPUsername: "secret-user", SMTPPassword: "secret-password", SMTPFrom: "support@example.com"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = New(cfg).Send(ctx, "message-123", "recipient@example.com", []byte("Subject: test\r\n\r\nbody"))
	if err == nil || err.Error() != "smtp_delivery_failed" {
		t.Fatalf("unsafe TLS error: %v", err)
	}
}
