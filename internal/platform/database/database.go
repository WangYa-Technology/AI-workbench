package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	// Declare the binary's complete bundle protocol on every connection, including
	// reconnects. Migration 0115 rejects older writers and worker claimers.
	poolConfig.ConnConfig.RuntimeParams["app.product_bundle_protocol"] = "zip-v1"
	poolConfig.ConnConfig.RuntimeParams["app.generation_output_protocol"] = "journal-v1"
	poolConfig.ConnConfig.RuntimeParams["app.generation_execution_protocol"] = "lease-v1"
	poolConfig.ConnConfig.RuntimeParams["app.asset_scan_execution_protocol"] = "lease-v1"
	poolConfig.ConnConfig.RuntimeParams["app.upload_write_protocol"] = "journal-v1"
	poolConfig.ConnConfig.RuntimeParams["app.upload_command_protocol"] = "command-v1"
	poolConfig.ConnConfig.RuntimeParams["app.seller_funding_read_protocol"] = "deadline-v1"
	poolConfig.ConnConfig.RuntimeParams["app.seller_funding_admission_protocol"] = "review-v1"
	poolConfig.ConnConfig.RuntimeParams["app.seller_bank_payout_protocol"] = "command-v1"
	poolConfig.ConnConfig.RuntimeParams["app.seller_bank_execution_protocol"] = "journal-v1"
	poolConfig.ConnConfig.RuntimeParams["app.seller_bank_resume_protocol"] = "resume-v1"
	poolConfig.ConnConfig.RuntimeParams["app.payment_transfer_execution_protocol"] = "job-v1"
	poolConfig.ConnConfig.RuntimeParams["app.payment_execution_lock_protocol"] = "lock-v1"
	poolConfig.ConnConfig.RuntimeParams["app.seller_reversal_protocol"] = "command-v1"
	poolConfig.ConnConfig.RuntimeParams["app.seller_reversal_execution_protocol"] = "journal-v1"
	poolConfig.ConnConfig.RuntimeParams["app.seller_reversal_closure_protocol"] = "closure-v1"
	poolConfig.MaxConns = 12
	poolConfig.MinConns = 1
	poolConfig.MaxConnLifetime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
