-- Bounded operational telemetry, not financial or deletion-completion evidence.
CREATE TABLE maintenance_health (
 kind text PRIMARY KEY CHECK(kind IN (
  'legal_hold_expiry','legal_hold_cleanup','product_cleanup_reconciliation',
  'account_deletion_reconciliation','original_media_cleanup_reconciliation','product_refund_reconciliation')),
 passes bigint NOT NULL CHECK(passes>0),
 failures bigint NOT NULL CHECK(failures>=0 AND failures<=passes),
 last_failed boolean NOT NULL,
 completed_at timestamptz NOT NULL CHECK(isfinite(completed_at)),
 last_success_at timestamptz CHECK(last_success_at IS NULL OR isfinite(last_success_at)),
 last_failure_at timestamptz CHECK(last_failure_at IS NULL OR isfinite(last_failure_at)),
 CHECK((last_success_at IS NULL)=(passes=failures)),
 CHECK((last_failure_at IS NULL)=(failures=0)),
 CHECK(last_success_at IS NULL OR last_success_at<=completed_at),
 CHECK(last_failure_at IS NULL OR last_failure_at<=completed_at)
);
