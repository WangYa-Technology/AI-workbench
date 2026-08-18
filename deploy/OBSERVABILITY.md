# Production observability baseline

The API exposes `GET /metrics` for an internal Prometheus-compatible scraper. The endpoint is intentionally not proxied by the public Web container. Place the scraper on the private Compose/network side or expose it only through an authenticated operations proxy.

The series are deliberately low-cardinality and contain no request IDs, URL parameters, user IDs, prompts, email addresses, Provider response data, or credentials:

- `hcai_database_ready`
- `hcai_audit_chain_valid`
- `hcai_http_requests_total` by status class only
- `hcai_http_request_duration_seconds_*` and response bytes
- `hcai_jobs_total` by durable status
- `hcai_jobs_oldest_queued_age_seconds`, counting only work whose `available_at` has arrived
- `hcai_job_attempts_total` by status for the last 24 hours

Run the bounded smoke check from an operations host:

```bash
METRICS_URL=http://api:8080/metrics make metrics-check
```

The check exits non-zero when PostgreSQL is unavailable, the audit chain is invalid, runnable queued work exceeds `ALERT_MAX_QUEUED_AGE_SECONDS` (default 900 seconds), or failed attempts exceed `ALERT_MAX_FAILED_ATTEMPTS_24H` (default 0). Future scheduled work is excluded until its `available_at` time arrives. Configure an external scheduler or Prometheus alert rule to page an operator; this repository does not claim to provide paging or centralized retention.
