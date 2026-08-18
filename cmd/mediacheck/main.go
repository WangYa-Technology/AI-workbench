package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

const acceptanceConfirmation = "I_APPROVE_MEDIA_ACCEPTANCE_CALLS"

func main() {
	if os.Getenv("MEDIA_ACCEPTANCE_CONFIRM") != acceptanceConfirmation {
		fmt.Fprintln(os.Stderr, "media acceptance disabled: MEDIA_ACCEPTANCE_CONFIRM must equal "+acceptanceConfirmation)
		os.Exit(1)
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "media acceptance configuration invalid:", err)
		os.Exit(1)
	}
	if cfg.MediaStorageAdapter != "s3" || cfg.MediaScannerAdapter != "http" {
		fmt.Fprintln(os.Stderr, "media acceptance requires MEDIA_STORAGE_ADAPTER=s3 and MEDIA_SCANNER_ADAPTER=http")
		os.Exit(1)
	}
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		fmt.Fprintln(os.Stderr, "media acceptance random identifier unavailable")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store := media.NewCatalogFromConfig(cfg).Primary()
	scanner := media.NewHTTPScanner(cfg.MediaScannerURL, cfg.MediaScannerToken, time.Duration(cfg.MediaScannerTimeoutSeconds)*time.Second)
	result, err := media.RunAcceptance(ctx, store, scanner, hex.EncodeToString(random))
	if err != nil {
		fmt.Fprintln(os.Stderr, "media acceptance failed:", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "encode media acceptance summary:", err)
		os.Exit(1)
	}
}
