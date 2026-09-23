#!/usr/bin/env bash
set -euo pipefail

metrics_url="${METRICS_URL:-http://127.0.0.1:8080/metrics}"
max_queued_age="${ALERT_MAX_QUEUED_AGE_SECONDS:-900}"
max_failed_attempts="${ALERT_MAX_FAILED_ATTEMPTS_24H:-0}"
max_recovery_failures="${ALERT_MAX_RECOVERY_FAILURES:-0}"
payment_mode="${ALERT_PAYMENT_MODE:-live}"
max_payment_problems="${ALERT_MAX_PAYMENT_PROBLEMS:-0}"
max_checkout_pending_age="${ALERT_MAX_CHECKOUT_PENDING_AGE_SECONDS:-900}"
max_checkout_expired_age="${ALERT_MAX_CHECKOUT_EXPIRED_AGE_SECONDS:-900}"
max_refund_age="${ALERT_MAX_REFUND_UNRESOLVED_AGE_SECONDS:-86400}"
max_refund_check_age="${ALERT_MAX_REFUND_CHECK_DUE_AGE_SECONDS:-900}"
max_settlement_due_age="${ALERT_MAX_SETTLEMENT_DUE_AGE_SECONDS:-900}"
max_settlement_unresolved_age="${ALERT_MAX_SETTLEMENT_UNRESOLVED_AGE_SECONDS:-900}"
max_seller_funding_age="${ALERT_MAX_SELLER_FUNDING_UNRESOLVED_AGE_SECONDS:-900}"
max_seller_funding_check_age="${ALERT_MAX_SELLER_FUNDING_CHECK_DUE_AGE_SECONDS:-900}"
max_seller_bank_age="${ALERT_MAX_SELLER_BANK_UNRESOLVED_AGE_SECONDS:-900}"
max_seller_bank_check_age="${ALERT_MAX_SELLER_BANK_CHECK_DUE_AGE_SECONDS:-900}"
max_seller_reversal_age="${ALERT_MAX_SELLER_REVERSAL_UNRESOLVED_AGE_SECONDS:-900}"
max_seller_reversal_closure_age="${ALERT_MAX_SELLER_REVERSAL_CLOSURE_DUE_AGE_SECONDS:-900}"
max_maintenance_age="${ALERT_MAX_MAINTENANCE_SUCCESS_AGE_SECONDS:-300}"
max_expired_lease_age="${ALERT_MAX_EXPIRED_LEASE_AGE_SECONDS:-60}"
max_upload_cleanup_age="${ALERT_MAX_UPLOAD_WRITE_CLEANUP_AGE_SECONDS:-300}"
max_output_cleanup_age="${ALERT_MAX_GENERATION_OUTPUT_CLEANUP_AGE_SECONDS:-300}"
max_generation_recovery_age="${ALERT_MAX_GENERATION_RECOVERY_AGE_SECONDS:-300}"
max_scan_pending_age="${ALERT_MAX_ASSET_SCAN_PENDING_AGE_SECONDS:-900}"
max_scan_recovery_age="${ALERT_MAX_ASSET_SCAN_RECOVERY_AGE_SECONDS:-300}"
max_media_write_pending_age="${ALERT_MAX_MEDIA_WRITE_PENDING_AGE_SECONDS:-1800}"
command -v curl >/dev/null || { echo "curl is required." >&2; exit 1; }

if ! [[ "$max_queued_age" =~ ^[0-9]+([.][0-9]+)?$ && "$max_failed_attempts" =~ ^[0-9]+$ && "$max_recovery_failures" =~ ^[0-9]+$ && "$max_payment_problems" =~ ^[0-9]+$ ]]; then
  echo "Alert thresholds must be non-negative numbers." >&2
  exit 1
fi
for threshold in "$max_checkout_pending_age" "$max_checkout_expired_age" "$max_refund_age" "$max_refund_check_age" "$max_settlement_due_age" "$max_settlement_unresolved_age" "$max_seller_funding_age" "$max_seller_funding_check_age" "$max_seller_bank_age" "$max_seller_bank_check_age" "$max_seller_reversal_age" "$max_seller_reversal_closure_age" "$max_maintenance_age" "$max_expired_lease_age" "$max_output_cleanup_age" "$max_upload_cleanup_age" "$max_generation_recovery_age" "$max_scan_pending_age" "$max_scan_recovery_age" "$max_media_write_pending_age"; do
  if ! [[ "$threshold" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    echo "Age thresholds must be non-negative numbers." >&2
    exit 1
  fi
done
case "$payment_mode" in
  live|test) payment_modes=("$payment_mode") ;;
  all) payment_modes=(live test) ;;
  *) echo "ALERT_PAYMENT_MODE must be live, test or all." >&2; exit 1 ;;
esac
if ! body="$(curl --fail --silent --show-error --max-time 5 "$metrics_url")"; then
  echo "ALERT metrics endpoint unavailable: $metrics_url" >&2
  exit 1
fi

metric() {
  local name="$1"
  local value
  if ! value="$(awk -v name="$name" -v fallback="${2:-}" -v integer="${3:-}" '
    $1 == name {count++; value=$2; if (NF!=2 || $2 !~ /^[0-9]+([.][0-9]+)?$/ || (integer!="" && $2 !~ /^[0-9]+$/)) invalid=1}
    END {
      if (count==0 && fallback!="") {print fallback; exit}
      if (count!=1 || invalid) exit 1
      print value
    }' <<<"$body")"; then
    echo "ALERT missing_or_invalid_metric: $name" >&2
    return 1
  fi
  printf '%s\n' "$value"
}
database_ready="$(metric hcai_database_ready)"
audit_valid="$(metric hcai_audit_chain_valid)"
queued_age="$(metric hcai_jobs_oldest_queued_age_seconds)"
expired_leases="$(metric hcai_jobs_expired_leases '' integer)"
expired_lease_age="$(metric hcai_jobs_oldest_expired_lease_age_seconds)"
invalid_leases="$(metric hcai_jobs_invalid_leases '' integer)"
failed_attempts="$(metric 'hcai_job_attempts_total{status="failed",window="24h"}' 0 integer)"
recovery_failures=0
for kind in account_deletion export_expiry export_export media_account media_product; do
  value="$(metric "hcai_recovery_jobs_failed{kind=\"$kind\"}" '' integer)"
  recovery_failures="$(awk -v total="$recovery_failures" -v value="$value" 'BEGIN {printf "%.0f", total+value}')"
done

output_failures="$(metric hcai_generation_output_cleanup_failed '' integer)"
output_due="$(metric hcai_generation_output_cleanup_due '' integer)"
output_age="$(metric hcai_generation_output_cleanup_oldest_due_age_seconds)"
alerts=()
for kind in generation_output upload_write; do
  pending="$(metric "hcai_${kind}_pending" '' integer)"
  pending_invalid="$(metric "hcai_${kind}_pending_invalid_timestamps" '' integer)"
  pending_age="$(metric "hcai_${kind}_oldest_pending_age_seconds")"
  if ! awk -v count="$pending" -v invalid="$pending_invalid" -v age="$pending_age" 'BEGIN {exit !(invalid<=count && (count>0 || age==0))}'; then
    echo "ALERT inconsistent_media_write_metrics: $kind" >&2
    exit 1
  fi
  [[ "$pending_invalid" == 0 ]] || alerts+=("${kind}_pending_clock_invalid")
  if ! awk -v age="$pending_age" -v limit="$max_media_write_pending_age" 'BEGIN {exit !(age<=limit)}'; then
    alerts+=("${kind}_pending_overdue")
  fi
done
[[ "$output_failures" == 0 ]] || alerts+=("generation_output_cleanup_failed")
if ! awk -v count="$output_due" -v age="$output_age" 'BEGIN {exit !(count>0 || age==0)}'; then
 echo "ALERT inconsistent_generation_output_metrics" >&2
 exit 1
fi
if ! awk -v age="$output_age" -v limit="$max_output_cleanup_age" 'BEGIN {exit !(age<=limit)}'; then
 alerts+=("generation_output_cleanup_overdue")
fi
upload_failures="$(metric hcai_upload_write_cleanup_failed '' integer)"
upload_due="$(metric hcai_upload_write_cleanup_due '' integer)"
upload_age="$(metric hcai_upload_write_cleanup_oldest_due_age_seconds)"
[[ "$upload_failures" == 0 ]] || alerts+=("upload_write_cleanup_failed")
if ! awk -v count="$upload_due" -v age="$upload_age" 'BEGIN {exit !(count>0 || age==0)}'; then
 echo "ALERT inconsistent_upload_write_metrics" >&2
 exit 1
fi
if ! awk -v age="$upload_age" -v limit="$max_upload_cleanup_age" 'BEGIN {exit !(age<=limit)}'; then
 alerts+=("upload_write_cleanup_overdue")
fi
generation_failed="$(metric hcai_generation_recovery_failed '' integer)"
generation_due="$(metric hcai_generation_recovery_due '' integer)"
generation_unresolved="$(metric hcai_generation_recovery_unresolved '' integer)"
generation_age="$(metric hcai_generation_recovery_oldest_due_age_seconds)"
[[ "$generation_failed" == 0 ]] || alerts+=("generation_recovery_failed")
[[ "$generation_unresolved" == 0 ]] || alerts+=("generation_recovery_unresolved")
if ! awk -v count="$generation_due" -v age="$generation_age" 'BEGIN {exit !(count>0 || age==0)}'; then
 echo "ALERT inconsistent_generation_recovery_metrics" >&2
 exit 1
fi
if ! awk -v age="$generation_age" -v limit="$max_generation_recovery_age" 'BEGIN {exit !(age<=limit)}'; then
 alerts+=("generation_recovery_overdue")
fi
scan_pending="$(metric hcai_asset_scan_pending '' integer)"
scan_pending_age="$(metric hcai_asset_scan_oldest_pending_age_seconds)"
scan_failed="$(metric hcai_asset_scan_recovery_failed '' integer)"
scan_due="$(metric hcai_asset_scan_recovery_due '' integer)"
scan_unresolved="$(metric hcai_asset_scan_recovery_unresolved '' integer)"
scan_due_age="$(metric hcai_asset_scan_recovery_oldest_due_age_seconds)"
if ! awk -v pending="$scan_pending" -v age="$scan_pending_age" -v failed="$scan_failed" -v due="$scan_due" -v unresolved="$scan_unresolved" -v due_age="$scan_due_age" 'BEGIN {exit !((pending>0 || age==0) && (due>0 || due_age==0) && failed<=pending && due<=pending && unresolved<=pending)}'; then
 echo "ALERT inconsistent_asset_scan_metrics" >&2
 exit 1
fi
[[ "$scan_failed" == 0 ]] || alerts+=("asset_scan_recovery_failed")
[[ "$scan_unresolved" == 0 ]] || alerts+=("asset_scan_recovery_unresolved")
if ! awk -v age="$scan_pending_age" -v limit="$max_scan_pending_age" 'BEGIN {exit !(age<=limit)}'; then
 alerts+=("asset_scan_pending_overdue")
fi
if ! awk -v age="$scan_due_age" -v limit="$max_scan_recovery_age" 'BEGIN {exit !(age<=limit)}'; then
 alerts+=("asset_scan_recovery_overdue")
fi
[[ "$invalid_leases" == 0 ]] || alerts+=("invalid_job_leases")
if ! awk -v count="$expired_leases" -v age="$expired_lease_age" 'BEGIN {exit !(count>0 || age==0)}'; then
  echo "ALERT inconsistent_lease_metrics" >&2
  exit 1
fi
if ! awk -v age="$expired_lease_age" -v limit="$max_expired_lease_age" 'BEGIN {exit !(age<=limit)}'; then
  alerts+=("expired_job_lease_age_exceeded")
fi
# A reversal reconciliation worker is only applicable once a source-return
# command exists. Keep its health checks strict when work exists, while
# avoiding a false "never succeeded" alert on installations with no seller
# reversals yet.
reversal_work=0
settlement_work=0
seller_funding_work=0
seller_bank_work=0
for mode in "${payment_modes[@]}"; do
  reversal_unresolved="$(metric "hcai_product_payment_backlog{kind=\"seller_reversal_unresolved\",mode=\"$mode\"}" '' integer)"
  reversal_due="$(metric "hcai_product_payment_backlog{kind=\"seller_reversal_closure_due\",mode=\"$mode\"}" '' integer)"
  reversal_work=$((reversal_work + reversal_unresolved + reversal_due))
  settlement_due="$(metric "hcai_product_payment_backlog{kind=\"settlement_due\",mode=\"$mode\"}" '' integer)"
  settlement_unresolved="$(metric "hcai_product_payment_backlog{kind=\"settlement_unresolved\",mode=\"$mode\"}" '' integer)"
  settlement_work=$((settlement_work + settlement_due + settlement_unresolved))
  funding_unresolved="$(metric "hcai_product_payment_backlog{kind=\"seller_funding_unresolved\",mode=\"$mode\"}" '' integer)"
  funding_due="$(metric "hcai_product_payment_backlog{kind=\"seller_funding_check_due\",mode=\"$mode\"}" '' integer)"
  seller_funding_work=$((seller_funding_work + funding_unresolved + funding_due))
  bank_unresolved="$(metric "hcai_product_payment_backlog{kind=\"seller_bank_unresolved\",mode=\"$mode\"}" '' integer)"
  bank_due="$(metric "hcai_product_payment_backlog{kind=\"seller_bank_check_due\",mode=\"$mode\"}" '' integer)"
  seller_bank_work=$((seller_bank_work + bank_unresolved + bank_due))
done
for kind in legal_hold_expiry legal_hold_cleanup product_cleanup_reconciliation account_deletion_reconciliation original_media_cleanup_reconciliation product_refund_reconciliation product_checkout_reconciliation product_settlement_reconciliation seller_funding_reconciliation seller_bank_reconciliation seller_reversal_reconciliation generation_output_cleanup generation_execution_recovery asset_scan_execution_recovery upload_write_cleanup; do
  labels="{kind=\"$kind\"}"
  required=1
  case "$kind" in
    product_settlement_reconciliation) [[ "$settlement_work" -gt 0 ]] || required=0 ;;
    seller_funding_reconciliation) [[ "$seller_funding_work" -gt 0 ]] || required=0 ;;
    seller_bank_reconciliation) [[ "$seller_bank_work" -gt 0 ]] || required=0 ;;
    seller_reversal_reconciliation) [[ "$reversal_work" -gt 0 ]] || required=0 ;;
  esac
  if [[ "$required" == 1 ]]; then
    seen="$(metric "hcai_maintenance_success_seen$labels" '' integer)"
    failed="$(metric "hcai_maintenance_last_pass_failed$labels" '' integer)"
    invalid="$(metric "hcai_maintenance_invalid_timestamps$labels" '' integer)"
    age="$(metric "hcai_maintenance_last_success_age_seconds$labels")"
    [[ "$seen" == 1 ]] || alerts+=("${kind}_never_succeeded")
    [[ "$failed" == 0 ]] || alerts+=("${kind}_pass_failed")
    [[ "$invalid" == 0 ]] || alerts+=("${kind}_clock_invalid")
    if ! awk -v age="$age" -v limit="$max_maintenance_age" 'BEGIN {exit !(age<=limit)}'; then
      alerts+=("${kind}_success_stale")
    fi
  fi
done
payment_problems=0
for mode in "${payment_modes[@]}"; do
  quarantine_count="$(metric "hcai_product_webhook_quarantines{mode=\"$mode\"}" '' integer)"
  quarantine_age="$(metric "hcai_product_webhook_quarantine_oldest_age_seconds{mode=\"$mode\"}")"
  [[ "$quarantine_count" == 0 ]] || alerts+=("product_webhook_evidence_pending_${mode}")
  if ! awk -v count="$quarantine_count" -v age="$quarantine_age" 'BEGIN {exit !(count>0 || age==0)}'; then
    echo "ALERT inconsistent_product_webhook_metrics" >&2
    exit 1
  fi
  for kind in checkout_pending checkout_expired refund_unresolved refund_check_due settlement_due settlement_unresolved seller_funding_unresolved seller_funding_check_due seller_bank_unresolved seller_bank_check_due seller_reversal_unresolved seller_reversal_closure_due; do
    labels="{kind=\"$kind\",mode=\"$mode\"}"
    count="$(metric "hcai_product_payment_backlog$labels" '' integer)"
    age="$(metric "hcai_product_payment_oldest_age_seconds$labels")"
    case "$kind" in
      checkout_pending) limit="$max_checkout_pending_age" ;;
      checkout_expired) limit="$max_checkout_expired_age" ;;
      refund_unresolved) limit="$max_refund_age" ;;
      refund_check_due) limit="$max_refund_check_age" ;;
      settlement_due) limit="$max_settlement_due_age" ;;
      settlement_unresolved) limit="$max_settlement_unresolved_age" ;;
      seller_funding_unresolved) limit="$max_seller_funding_age" ;;
      seller_bank_unresolved) limit="$max_seller_bank_age" ;;
      seller_funding_check_due) limit="$max_seller_funding_check_age" ;;
      seller_bank_check_due) limit="$max_seller_bank_check_age" ;;
      seller_reversal_unresolved) limit="$max_seller_reversal_age" ;;
      seller_reversal_closure_due) limit="$max_seller_reversal_closure_age" ;;
    esac
    if ! awk -v count="$count" -v age="$age" 'BEGIN {exit !(count>0 || age==0)}'; then
      echo "ALERT inconsistent_payment_metrics: $kind $mode" >&2
      exit 1
    fi
    if ! awk -v age="$age" -v limit="$limit" 'BEGIN {exit !(age<=limit)}'; then
      alerts+=("${mode}_${kind}_age_exceeded")
    fi
  done
  for kind in event_failed checkout_check_missing checkout_check_stopped refund_check_stopped refund_observation_unresolved refund_read_unrecorded refund_evidence_missing checkout_evidence_missing checkout_evidence_conflict closed_checkout_paid settlement_missing settlement_check_stopped seller_funding_dispatch_missing seller_funding_admission_missing seller_funding_stopped seller_funding_review seller_funding_read_unrecorded seller_bank_failed seller_bank_review seller_bank_read_unrecorded seller_bank_dispatch_stopped seller_reversal_review seller_reversal_read_unrecorded seller_reversal_stopped invalid_timestamp; do
    value="$(metric "hcai_product_payment_problems{kind=\"$kind\",mode=\"$mode\"}" '' integer)"
    payment_problems="$(awk -v total="$payment_problems" -v value="$value" 'BEGIN {printf "%.0f", total+value}')"
  done
done
if ! awk -v value="$payment_problems" -v limit="$max_payment_problems" 'BEGIN {exit !(value<=limit)}'; then
  alerts+=("payment_problems_exceeded")
fi
[[ "$database_ready" == "1" ]] || alerts+=("database_not_ready")
[[ "$audit_valid" == "1" ]] || alerts+=("audit_chain_invalid")
if ! awk -v value="$queued_age" -v limit="$max_queued_age" 'BEGIN {exit !(value <= limit)}'; then
  alerts+=("queued_job_age_exceeded")
fi
if ! awk -v value="$failed_attempts" -v limit="$max_failed_attempts" 'BEGIN {exit !(value <= limit)}'; then
  alerts+=("failed_job_attempts_exceeded")
fi
if ! awk -v value="$recovery_failures" -v limit="$max_recovery_failures" 'BEGIN {exit !(value <= limit)}'; then
  alerts+=("recovery_job_failures_exceeded")
fi

if ((${#alerts[@]} > 0)); then
  printf 'ALERT %s\n' "${alerts[*]}" >&2
  printf 'database_ready=%s audit_valid=%s queued_age_seconds=%s failed_attempts_24h=%s recovery_failures=%s payment_mode=%s payment_problems=%s\n' "$database_ready" "$audit_valid" "$queued_age" "$failed_attempts" "$recovery_failures" "$payment_mode" "$payment_problems" >&2
  exit 1
fi
printf 'ok database_ready=%s audit_valid=%s queued_age_seconds=%s failed_attempts_24h=%s recovery_failures=%s payment_mode=%s payment_problems=%s\n' "$database_ready" "$audit_valid" "$queued_age" "$failed_attempts" "$recovery_failures" "$payment_mode" "$payment_problems"
