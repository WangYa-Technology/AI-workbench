package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hcai-chat/hcai-chat/internal/mediainventory"
	"github.com/hcai-chat/hcai-chat/internal/platform/config"
	"github.com/hcai-chat/hcai-chat/internal/platform/database"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(parent context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mediainventory", flag.ContinueOnError)
	flags.SetOutput(stderr)
	backend := flags.String("backend", "", "required: local_file or s3")
	output := flags.String("output", "", "new private JSONL report path (never overwritten)")
	limit := flags.Int("max-entries", 100000, "maximum entries, 1–1000000; exceeding it fails the scan")
	duration := flags.Duration("timeout", 10*time.Minute, "scan deadline, 1s–30m")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || (*backend != "local_file" && *backend != "s3") || *output == "" || *limit < 1 || *limit > 1_000_000 || *duration < time.Second || *duration > 30*time.Minute {
		fmt.Fprintln(stderr, "invalid inventory options; use -help")
		return 2
	}
	fail := func(stage string) int {
		// Provider and database errors may include private locators/credentials.
		fmt.Fprintln(stderr, "media inventory failed at "+stage+"; no new complete report published")
		return 1
	}
	cfg, err := config.Load()
	if err != nil {
		return fail("configuration")
	}
	store, err := media.NewCatalogFromConfig(cfg).Get(*backend)
	if err != nil {
		return fail("backend selection")
	}
	inventory, ok := store.(media.Inventory)
	if !ok {
		return fail("inventory capability")
	}
	ctx, cancel := context.WithTimeout(parent, *duration)
	defer cancel()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fail("database connection")
	}
	defer pool.Close()
	// Intentionally no database.Migrate, workers, scanner, payment or cleanup calls.
	summary, err := mediainventory.Write(ctx, pool, inventory, *output, *limit)
	if err != nil {
		return fail("read-only scan or report publication")
	}
	if err := json.NewEncoder(stdout).Encode(summary); err != nil {
		fmt.Fprintln(stderr, "report published, but summary output failed")
		return 1
	}
	return 0
}
