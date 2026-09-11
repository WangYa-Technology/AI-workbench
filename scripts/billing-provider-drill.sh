#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "$0")/.." && pwd)"
base_database_url="${TEST_DATABASE_URL:-postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable}"
api_port="${BILLING_DRILL_HTTP_PORT:-18086}"
fixture_port="${BILLING_DRILL_FIXTURE_PORT:-18087}"
schema="drill_billing_provider_$(date +%s)_${RANDOM}"
root="$(mktemp -d "${TMPDIR:-/tmp}/hcai-billing-provider.XXXXXX")"
api_pid=""
worker_pid=""
fixture_pid=""
secret="whsec_billing_provider_drill_secret"
key="sk_test_billing_provider_drill"
version="2026-02-25.clover"

cleanup() {
  for pid in "$worker_pid" "$api_pid" "$fixture_pid"; do
    if [[ -n "$pid" ]]; then
      kill -TERM "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  PGOPTIONS="-c search_path=public" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "DROP SCHEMA IF EXISTS $schema CASCADE" >/dev/null 2>&1 || true
  rm -rf "$root"
}

on_exit() {
  local status=$?
  if (( status != 0 )); then
    for log_file in "$root/api.log" "$root/worker.log" "$root/fixture.log"; do
      if [[ -f "$log_file" ]]; then
        echo "--- $(basename "$log_file") ---" >&2
        tail -40 "$log_file" >&2
      fi
    done
  fi
  cleanup
}

trap on_exit EXIT
trap 'exit 130' INT TERM

for command in psql jq curl lsof openssl; do
  command -v "$command" >/dev/null || { echo "billing provider drill requires $command" >&2; exit 1; }
done
for port in "$api_port" "$fixture_port"; do
  if ! [[ "$port" =~ ^[0-9]+$ ]] || (( port < 1024 || port > 65535 )); then
    echo "Billing drill ports must be 1024-65535." >&2
    exit 1
  fi
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "Billing drill port $port is already in use." >&2
    exit 1
  fi
done
if [[ "$api_port" == "$fixture_port" ]]; then
  echo "Billing drill ports must differ." >&2
  exit 1
fi

cd "$project_root"
PGOPTIONS="-c search_path=public" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "CREATE SCHEMA $schema" >/dev/null
separator="?"
[[ "$base_database_url" == *"?"* ]] && separator="&"
export APP_ENV=test DATABASE_URL="${base_database_url}${separator}search_path=${schema}" HTTP_ADDR="127.0.0.1:${api_port}"
export WEB_ORIGIN="http://127.0.0.1:${api_port}" MEDIA_ROOT="$root/media" LOCAL_PROVIDER_SOURCE="$project_root/web/public/media/home-cinematic.jpg"
export LOCAL_PROVIDER_ENABLED=true DEMO_DATA_ENABLED=true EMAIL_DELIVERY_MODE=disabled
export STRIPE_ENABLED=true STRIPE_LIVE_MODE=false STRIPE_LIVE_MODE_APPROVED=false STRIPE_SECRET_KEY="$key" STRIPE_WEBHOOK_SECRET="$secret"
export STRIPE_BASE_URL="http://127.0.0.1:${fixture_port}/v1" STRIPE_API_VERSION="$version" STRIPE_WEBHOOK_TOLERANCE_SECONDS=300

go run ./cmd/migrate >/dev/null
go run ./cmd/seed >/dev/null
go build -o "$root/hcai-api" ./cmd/api
go build -o "$root/hcai-worker" ./cmd/worker
go build -o "$root/stripe-fixture" ./cmd/stripefixture

"$root/stripe-fixture" -addr "127.0.0.1:${fixture_port}" -secret-key "$key" -api-version "$version" >"$root/fixture.log" 2>&1 & fixture_pid=$!
for _ in {1..100}; do
  curl -fsS "http://127.0.0.1:${fixture_port}/ready" >/dev/null 2>&1 && break
  sleep 0.1
done
curl -fsS "http://127.0.0.1:${fixture_port}/ready" >/dev/null

"$root/hcai-api" >"$root/api.log" 2>&1 & api_pid=$!
for _ in {1..100}; do
  curl -fsS "http://127.0.0.1:${api_port}/ready" >/dev/null 2>&1 && break
  sleep 0.1
done
curl -fsS "http://127.0.0.1:${api_port}/ready" >/dev/null

"$root/hcai-worker" >"$root/worker.log" 2>&1 & worker_pid=$!
for _ in {1..100}; do
  rg -q '"msg":"worker started"' "$root/worker.log" && break
  sleep 0.1
done
rg -q '"msg":"worker started"' "$root/worker.log"

api_url="http://127.0.0.1:${api_port}/api/v1"
buyer_jar="$root/buyer.cookies"
curl -fsS -c "$buyer_jar" -b "$buyer_jar" -H 'Content-Type: application/json' -d '{"actor":"creator"}' "$api_url/auth/demo" >/dev/null

statement=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" "$api_url/billing/statement")
initial_balance=$(jq -er '.account.balanceCents' <<<"$statement")
points=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" "$api_url/billing/points")
initial_points=$(jq -er '.account.balancePoints' <<<"$points")
creator_plan_id=$(jq -er '.plans[] | select(.tierCode == "creator") | .id' <<<"$points")
creator_price=$(jq -er '.plans[] | select(.tierCode == "creator") | .priceCents' <<<"$points")
creator_granted_points=$(jq -er '.plans[] | select(.tierCode == "creator") | .includedPoints' <<<"$points")
user_id=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" "$api_url/auth/session" | jq -er '.user.id')

topup_key="billing-provider-drill-topup-$(date +%s)-${RANDOM}"
topup=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" -H 'Content-Type: application/json' -H "Idempotency-Key: $topup_key" -d '{"amountCents":4200}' "$api_url/billing/topups/checkout")
jq -e '.status == "checkout_open" and .purpose == "wallet_topup" and .amountCents == 4200 and .paymentMode == "stripe" and .realCharge == false and .liveMode == false and (.checkoutUrl | startswith("https://checkout.stripe.com/"))' <<<"$topup" >/dev/null
topup_payment_id=$(jq -er '.paymentId' <<<"$topup")
topup_replay=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" -H 'Content-Type: application/json' -H "Idempotency-Key: $topup_key" -d '{"amountCents":4200}' "$api_url/billing/topups/checkout")
jq -e --arg id "$topup_payment_id" '.alreadyCreated == true and .paymentId == $id' <<<"$topup_replay" >/dev/null
if curl -sS -o /dev/null -w '%{http_code}' -c "$buyer_jar" -b "$buyer_jar" -H 'Content-Type: application/json' -H "Idempotency-Key: $topup_key" -d '{"amountCents":4300}' "$api_url/billing/topups/checkout" | grep -qx '409'; then :; else
  echo "top-up idempotency conflict was not rejected" >&2
  exit 1
fi

sign() {
  printf '%s.%s' "$1" "$2" | openssl dgst -sha256 -hmac "$secret" -hex | awk '{print $NF}'
}
submit_event() {
  local body="$1" timestamp="$2" signature
  signature=$(sign "$timestamp" "$body")
  curl -fsS -H 'Content-Type: application/json' -H "Stripe-Signature: t=$timestamp,v1=$signature" --data-binary "$body" "$api_url/payments/webhooks/stripe"
}

created=$(date +%s)
topup_body=$(jq -cn --arg version "$version" --arg payment "$topup_payment_id" --arg user "$user_id" --argjson created "$created" '{id:"evt_billing_drill_topup",object:"event",api_version:$version,created:$created,livemode:false,type:"checkout.session.completed",data:{object:{id:"cs_test_paymentdrill001",object:"checkout.session",status:"complete",payment_status:"paid",amount_total:4200,currency:"usd",payment_intent:"pi_billing_drill_topup",metadata:{hcai_payment_id:$payment,hcai_resource_id:$user,hcai_purpose:"wallet_topup"}}}}')
topup_receipt=$(submit_event "$topup_body" "$created")
jq -e '.duplicate == false and .status == "received" and .eventType == "checkout.session.completed"' <<<"$topup_receipt" >/dev/null
topup_duplicate=$(submit_event "$topup_body" "$created")
jq -e '.duplicate == true' <<<"$topup_duplicate" >/dev/null

for _ in {1..200}; do
  statement=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" "$api_url/billing/statement")
  [[ "$(jq -r '.account.balanceCents' <<<"$statement")" == "$((initial_balance + 4200))" ]] && break
  sleep 0.1
done
[[ "$(jq -r '.account.balanceCents' <<<"$statement")" == "$((initial_balance + 4200))" ]]

subscription_key="billing-provider-drill-subscription-$(date +%s)-${RANDOM}"
subscription=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" -H 'Content-Type: application/json' -H "Idempotency-Key: $subscription_key" -d "{\"planId\":\"$creator_plan_id\"}" "$api_url/billing/subscriptions/checkout")
jq -e --arg plan "$creator_plan_id" --argjson amount "$creator_price" '.status == "checkout_open" and .purpose == "subscription" and .resourceId == $plan and .amountCents == $amount and .paymentMode == "stripe" and .realCharge == false and .liveMode == false' <<<"$subscription" >/dev/null
subscription_payment_id=$(jq -er '.paymentId' <<<"$subscription")
subscription_replay=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" -H 'Content-Type: application/json' -H "Idempotency-Key: $subscription_key" -d "{\"planId\":\"$creator_plan_id\"}" "$api_url/billing/subscriptions/checkout")
jq -e --arg id "$subscription_payment_id" '.alreadyCreated == true and .paymentId == $id' <<<"$subscription_replay" >/dev/null

subscription_body=$(jq -cn --arg version "$version" --arg payment "$subscription_payment_id" --arg plan "$creator_plan_id" --argjson created "$((created + 1))" --argjson amount "$creator_price" '{id:"evt_billing_drill_subscription",object:"event",api_version:$version,created:$created,livemode:false,type:"checkout.session.completed",data:{object:{id:"cs_test_paymentdrill002",object:"checkout.session",status:"complete",payment_status:"paid",amount_total:$amount,currency:"usd",payment_intent:"pi_billing_drill_subscription",metadata:{hcai_payment_id:$payment,hcai_resource_id:$plan,hcai_purpose:"subscription"}}}}')
subscription_receipt=$(submit_event "$subscription_body" "$((created + 1))")
jq -e '.duplicate == false and .status == "received" and .eventType == "checkout.session.completed"' <<<"$subscription_receipt" >/dev/null
subscription_duplicate=$(submit_event "$subscription_body" "$((created + 1))")
jq -e '.duplicate == true' <<<"$subscription_duplicate" >/dev/null

for _ in {1..200}; do
  points=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" "$api_url/billing/points")
  [[ "$(jq -r '.account.balancePoints' <<<"$points")" == "$((initial_points + creator_granted_points))" ]] && break
  sleep 0.1
done
jq -e --argjson expected "$((initial_points + creator_granted_points))" '.account.balancePoints == $expected and .currentSubscription.tierCode == "creator"' <<<"$points" >/dev/null

sql() {
  PGOPTIONS="-c search_path=$schema" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "$1"
}
topup_evidence=$(sql "SELECT pi.status||'|'||(SELECT count(*) FROM billing_entries WHERE user_id='$user_id' AND operation_id='$topup_payment_id' AND entry_type='wallet_topup') FROM payment_intents pi WHERE pi.id='$topup_payment_id'")
subscription_evidence=$(sql "SELECT pi.status||'|'||(SELECT count(*) FROM point_entries WHERE user_id='$user_id' AND operation_id='$subscription_payment_id' AND entry_type='subscription_credit')||'|'||(SELECT count(*) FROM user_subscriptions WHERE user_id='$user_id' AND purchase_operation_id='$subscription_payment_id') FROM payment_intents pi WHERE pi.id='$subscription_payment_id'")
[[ "$topup_evidence" == "paid|1" ]] || { echo "Unexpected top-up evidence: $topup_evidence" >&2; exit 1; }
[[ "$subscription_evidence" == "paid|1|1" ]] || { echo "Unexpected subscription evidence: $subscription_evidence" >&2; exit 1; }

fixture=$(curl -fsS "http://127.0.0.1:${fixture_port}/__fixture/state")
jq -e '.checkoutCalls == 2' <<<"$fixture" >/dev/null
jq -n --arg topupPaymentId "$topup_payment_id" --arg subscriptionPaymentId "$subscription_payment_id" --arg topupEvidence "$topup_evidence" --arg subscriptionEvidence "$subscription_evidence" --argjson fixture "$fixture" '{status:"passed",topupPaymentId:$topupPaymentId,subscriptionPaymentId:$subscriptionPaymentId,topupEvidence:$topupEvidence,subscriptionEvidence:$subscriptionEvidence,checkoutReplay:true,webhookReplay:true,workerFulfillment:true,fixture:$fixture}'
