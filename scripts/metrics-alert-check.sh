#!/usr/bin/env bash
set -euo pipefail

metrics_url="${METRICS_URL:-http://127.0.0.1:8080/metrics}"
max_queued_age="${ALERT_MAX_QUEUED_AGE_SECONDS:-900}"
max_failed_attempts="${ALERT_MAX_FAILED_ATTEMPTS_24H:-0}"
command -v curl >/dev/null || { echo "curl is required." >&2; exit 1; }

if ! [[ "$max_queued_age" =~ ^[0-9]+([.][0-9]+)?$ && "$max_failed_attempts" =~ ^[0-9]+$ ]]; then
  echo "Alert thresholds must be non-negative numbers." >&2
  exit 1
fi
if ! body="$(curl --fail --silent --show-error --max-time 5 "$metrics_url")"; then
  echo "ALERT metrics endpoint unavailable: $metrics_url" >&2
  exit 1
fi

metric() {
  local name="$1"
  awk -v name="$name" '$0 ~ "^" name "(\\{| )" {print $NF; found=1; exit} END {if (!found) exit 1}' <<<"$body"
}
database_ready="$(metric hcai_database_ready)"
audit_valid="$(metric hcai_audit_chain_valid)"
queued_age="$(metric hcai_jobs_oldest_queued_age_seconds)"
failed_attempts="$(awk '$0 ~ /^hcai_job_attempts_total\{status="failed",window="24h"\}/ {print $NF; found=1; exit} END {if (!found) print 0}' <<<"$body")"

alerts=()
[[ "$database_ready" == "1" ]] || alerts+=("database_not_ready")
[[ "$audit_valid" == "1" ]] || alerts+=("audit_chain_invalid")
if ! awk -v value="$queued_age" -v limit="$max_queued_age" 'BEGIN {exit !(value <= limit)}'; then
  alerts+=("queued_job_age_exceeded")
fi
if ! awk -v value="$failed_attempts" -v limit="$max_failed_attempts" 'BEGIN {exit !(value <= limit)}'; then
  alerts+=("failed_job_attempts_exceeded")
fi

if ((${#alerts[@]} > 0)); then
  printf 'ALERT %s\n' "${alerts[*]}" >&2
  printf 'database_ready=%s audit_valid=%s queued_age_seconds=%s failed_attempts_24h=%s\n' "$database_ready" "$audit_valid" "$queued_age" "$failed_attempts" >&2
  exit 1
fi
printf 'ok database_ready=%s audit_valid=%s queued_age_seconds=%s failed_attempts_24h=%s\n' "$database_ready" "$audit_valid" "$queued_age" "$failed_attempts"
