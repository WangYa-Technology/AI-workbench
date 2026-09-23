package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/mailer"
)

func main() {
	send := flag.Bool("send-test", false, "send one diagnostic email to the configured sender mailbox")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := mailer.Check(ctx, cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Println("SMTP TLS and authentication verified")
	if *send {
		message := []byte("Subject: HCAI CHAT SMTP delivery check\r\n\r\nThis message verifies the configured HCAI CHAT email sender. No account has been created or changed.\r\n")
		if err := mailer.New(cfg).Send(ctx, uuid.NewString(), cfg.SMTPFrom, message); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Diagnostic message accepted by SMTP server")
	}
}
