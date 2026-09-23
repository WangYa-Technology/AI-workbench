#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "$0")/.." && pwd)"
base_database_url="${TEST_DATABASE_URL:-postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable}"
api_port="${PAYMENT_DRILL_HTTP_PORT:-18082}"
fixture_port="${PAYMENT_DRILL_FIXTURE_PORT:-18083}"
schema="drill_payment_provider_$(date +%s)_${RANDOM}"
root="$(mktemp -d "${TMPDIR:-/tmp}/hcai-payment-provider.XXXXXX")"
api_pid=""
worker_pid=""
fixture_pid=""
secret="whsec_payment_drill_secret"
key="sk_test_payment_drill"
version="2026-02-25.clover"
product_id="00000000-0000-4000-8000-000000000502"

cleanup() {
  for pid in "$worker_pid" "$api_pid" "$fixture_pid"; do
    if [[ -n "$pid" ]]; then kill -TERM "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
  done
  PGOPTIONS="-c search_path=public" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "DROP SCHEMA IF EXISTS $schema CASCADE" >/dev/null 2>&1 || true
  rm -rf "$root"
}
on_exit() {
  local status=$?
  if (( status != 0 )); then
    for log_file in "$root/api.log" "$root/worker.log" "$root/fixture.log"; do
      if [[ -f "$log_file" ]]; then echo "--- $(basename "$log_file") ---" >&2; tail -40 "$log_file" >&2; fi
    done
  fi
  cleanup
}
trap on_exit EXIT
trap 'exit 130' INT TERM

for command in psql jq curl lsof openssl; do command -v "$command" >/dev/null || { echo "payment drill requires $command" >&2; exit 1; }; done
for port in "$api_port" "$fixture_port"; do
  if ! [[ "$port" =~ ^[0-9]+$ ]] || (( port < 1024 || port > 65535 )); then echo "Payment drill ports must be 1024-65535." >&2; exit 1; fi
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then echo "Payment drill port $port is already in use." >&2; exit 1; fi
done
if [[ "$api_port" == "$fixture_port" ]]; then echo "Payment drill ports must differ." >&2; exit 1; fi

cd "$project_root"
PGOPTIONS="-c search_path=public" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "CREATE SCHEMA $schema" >/dev/null
separator="?"; [[ "$base_database_url" == *"?"* ]] && separator="&"
export APP_ENV=test DATABASE_URL="${base_database_url}${separator}search_path=${schema}" HTTP_ADDR="127.0.0.1:${api_port}"
export WEB_ORIGIN="http://127.0.0.1:${api_port}" MEDIA_ROOT="$root/media" LOCAL_PROVIDER_SOURCE="$project_root/web/public/media/home-cinematic.jpg"
export LOCAL_PROVIDER_ENABLED=true EMAIL_DELIVERY_MODE=disabled
export PAYMENT_PROVIDER=stripe WAFFO_ENABLED=false
export STRIPE_ENABLED=true STRIPE_LIVE_MODE=false STRIPE_LIVE_MODE_APPROVED=false STRIPE_SECRET_KEY="$key" STRIPE_WEBHOOK_SECRET="$secret"
export STRIPE_BASE_URL="http://127.0.0.1:${fixture_port}/v1" STRIPE_API_VERSION="$version" STRIPE_WEBHOOK_TOLERANCE_SECONDS=300

go run ./cmd/migrate >/dev/null
go run ./internal/testfixtures/cmd/seed >/dev/null
go build -o "$root/hcai-api" ./cmd/api
go build -o "$root/hcai-worker" ./cmd/worker
go build -o "$root/stripe-fixture" ./cmd/stripefixture

"$root/stripe-fixture" -addr "127.0.0.1:${fixture_port}" -secret-key "$key" -api-version "$version" >"$root/fixture.log" 2>&1 & fixture_pid=$!
for _ in {1..100}; do curl -fsS "http://127.0.0.1:${fixture_port}/ready" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "http://127.0.0.1:${fixture_port}/ready" >/dev/null
"$root/hcai-api" >"$root/api.log" 2>&1 & api_pid=$!
for _ in {1..100}; do curl -fsS "http://127.0.0.1:${api_port}/ready" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "http://127.0.0.1:${api_port}/ready" >/dev/null
"$root/hcai-worker" >"$root/worker.log" 2>&1 & worker_pid=$!
for _ in {1..100}; do rg -q '"msg":"worker started"' "$root/worker.log" && break; sleep 0.1; done
rg -q '"msg":"worker started"' "$root/worker.log"

api_url="http://127.0.0.1:${api_port}/api/v1"
sql() { PGOPTIONS="-c search_path=$schema" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "$1"; }
buyer_jar="$root/buyer.cookies"; admin_jar="$root/admin.cookies"
curl -fsS -c "$buyer_jar" -b "$buyer_jar" -H 'Content-Type: application/json' -d '{"email":"creator@fixture.hcai.test","password":"fixture-password-2026"}' "$api_url/auth/login" >/dev/null
curl -fsS -c "$admin_jar" -b "$admin_jar" -H 'Content-Type: application/json' -d '{"email":"operations@fixture.hcai.test","password":"fixture-password-2026"}' "$api_url/auth/login" >/dev/null

offer_version=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" "$api_url/products/$product_id" | jq -er '.offerVersion')
checkout_payload=$(jq -cn --arg version "$offer_version" '{licenseAccepted:true,offerVersion:$version}')
checkout_key="payment-provider-drill-checkout-$(date +%s)-${RANDOM}"
checkout=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" -H 'Content-Type: application/json' -H "Idempotency-Key: $checkout_key" -d "$checkout_payload" "$api_url/products/$product_id/checkout")
jq -e '.status == "checkout_open" and .paymentMode == "stripe" and .realCharge == false and .liveMode == false and (.checkoutUrl | startswith("https://checkout.stripe.com/"))' <<<"$checkout" >/dev/null
payment_id=$(jq -er '.paymentId' <<<"$checkout"); order_id=$(jq -er '.orderId' <<<"$checkout"); amount=$(jq -er '.amountCents' <<<"$checkout")
replay=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" -H 'Content-Type: application/json' -H "Idempotency-Key: $checkout_key" -d "$checkout_payload" "$api_url/products/$product_id/checkout")
jq -e --arg id "$payment_id" '.alreadyCreated == true and .paymentId == $id' <<<"$replay" >/dev/null
fixture=$(curl -fsS "http://127.0.0.1:${fixture_port}/__fixture/state")
jq -e --arg id "$payment_id" '.checkoutCalls == 1 and .lastCheckoutReference == $id and .lastCheckoutIdempotencyKey == ("checkout-" + $id)' <<<"$fixture" >/dev/null

# A checkout must already have a verified independent copy. This drill's
# media directory and schema are disposable; remove only its fixture original.
delivery_key=$(sql "SELECT storage_key FROM product_delivery_snapshots WHERE order_id='$order_id' AND state='ready' AND storage_backend='local_file'")
original_key=$(sql "SELECT source_key FROM product_delivery_snapshots WHERE order_id='$order_id' AND source_backend='local_file'")
[[ "$delivery_key" == delivery-* && "$delivery_key" != */* && "$original_key" != */* && -n "$original_key" && "$original_key" != . && "$original_key" != .. ]]
cmp "$MEDIA_ROOT/$original_key" "$MEDIA_ROOT/$delivery_key"
cp "$MEDIA_ROOT/$delivery_key" "$root/expected-delivery.bin"
rm "$MEDIA_ROOT/$original_key"

sign() { printf '%s.%s' "$1" "$2" | openssl dgst -sha256 -hmac "$secret" -hex | awk '{print $NF}'; }
submit_event() { local signature; signature=$(sign "$2" "$1"); curl -fsS -H 'Content-Type: application/json' -H "Stripe-Signature: t=$2,v1=$signature" --data-binary "$1" "$api_url/payments/webhooks/stripe"; }
wait_order() {
  local expected="$1" order=""
  for _ in {1..200}; do
    order=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" "$api_url/orders/$order_id")
    [[ "$(jq -r '.status' <<<"$order")" == "$expected" ]] && { printf '%s' "$order"; return 0; }
    sleep 0.1
  done
  echo "Order did not reach $expected" >&2; return 1
}

wait_payout() {
  local expected="$1" payout=""
  for _ in {1..200}; do
    payout=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" "$api_url/account/payouts")
    [[ "$(jq -r '.status' <<<"$payout")" == "$expected" ]] && { printf '%s' "$payout"; return 0; }
    sleep 0.1
  done
  echo "Payout account did not reach $expected" >&2; return 1
}

payout_user_id=$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" "$api_url/auth/session" | jq -er '.user.id')
jq -e '.providerAvailable == true and .liveMode == false and .status == "not_started" and .destinationId == null and .canStartOnboarding == true' \
  <<<"$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" "$api_url/account/payouts")" >/dev/null
onboarding=$(curl -fsS -X POST -c "$buyer_jar" -b "$buyer_jar" "$api_url/account/payouts/onboarding")
jq -e '.status.status == "pending_onboarding" and .status.destinationId == "acct_test_paymentdrill001" and (.url | startswith("https://connect.stripe.com/")) and .expiresAt != null' <<<"$onboarding" >/dev/null
onboarding_replay=$(curl -fsS -X POST -c "$buyer_jar" -b "$buyer_jar" "$api_url/account/payouts/onboarding")
jq -e --arg first "$(jq -er '.url' <<<"$onboarding")" '.status.status == "pending_onboarding" and .url != $first and (.url | startswith("https://connect.stripe.com/"))' <<<"$onboarding_replay" >/dev/null
fixture=$(curl -fsS "http://127.0.0.1:${fixture_port}/__fixture/state")
jq -e --arg user "$payout_user_id" '.connectAccountCalls == 1 and .accountLinkCalls == 2 and .lastConnectUserId == $user and .lastAccountLinkId == "acct_test_paymentdrill001" and (.lastConnectIdempotencyKey | startswith("connect-account-")) and (.lastAccountLinkIdempotencyKey | startswith("connect-link-"))' <<<"$fixture" >/dev/null

created=$(date +%s); payout_event="evt_paymentdrillaccount${RANDOM}"
payout_body=$(jq -cn --arg event "$payout_event" --arg user "$payout_user_id" --argjson created "$created" '{id:$event,object:"event",api_version:"2026-02-25.clover",created:$created,livemode:false,type:"account.updated",data:{object:{id:"acct_test_paymentdrill001",object:"account",charges_enabled:true,payouts_enabled:true,details_submitted:true,metadata:{hcai_user_id:$user},requirements:{currently_due:[],past_due:[],pending_verification:[]}}}}')
payout_receipt=$(submit_event "$payout_body" "$created")
jq -e --arg event "$payout_event" '.duplicate == false and .providerEventId == $event and .eventType == "account.updated"' <<<"$payout_receipt" >/dev/null
jq -e '.duplicate == true' <<<"$(submit_event "$payout_body" "$created")" >/dev/null
jq -e '.status == "verified" and .chargesEnabled == true and .payoutsEnabled == true and .detailsSubmitted == true and .requirementsDue == false and .destinationId == "acct_test_paymentdrill001"' <<<"$(wait_payout verified)" >/dev/null

created=$(date +%s); paid_event="evt_paymentdrillpaid${RANDOM}"
paid_body=$(jq -cn --arg event "$paid_event" --arg payment "$payment_id" --arg product "$product_id" --argjson created "$created" --argjson amount "$amount" '{id:$event,object:"event",api_version:"2026-02-25.clover",created:$created,livemode:false,type:"checkout.session.completed",data:{object:{id:"cs_test_paymentdrill001",object:"checkout.session",status:"complete",payment_status:"paid",amount_total:$amount,currency:"usd",payment_intent:"pi_test_paymentdrill001",metadata:{hcai_payment_id:$payment,hcai_resource_id:$product,hcai_purpose:"product"}}}}')
receipt=$(submit_event "$paid_body" "$created")
jq -e --arg event "$paid_event" '.duplicate == false and .providerEventId == $event' <<<"$receipt" >/dev/null
jq -e '.duplicate == true' <<<"$(submit_event "$paid_body" "$created")" >/dev/null
fulfilled=$(wait_order fulfilled)
jq -e '.status == "fulfilled" and .paymentMode == "stripe" and .assetId != null' <<<"$fulfilled" >/dev/null
asset_id=$(jq -er '.assetId' <<<"$fulfilled")
curl -fsS -c "$buyer_jar" -b "$buyer_jar" "$api_url/assets/$asset_id/content" -o "$root/download.bin"
cmp "$root/expected-delivery.bin" "$root/download.bin"
curl -fsS -c "$buyer_jar" -b "$buyer_jar" -H 'Range: bytes=0-15' "$api_url/assets/$asset_id/content" -o "$root/range.bin"
head -c 16 "$root/expected-delivery.bin" > "$root/expected-range.bin"
cmp "$root/expected-range.bin" "$root/range.bin"

refund_key="payment-provider-drill-refund-$(date +%s)-${RANDOM}"
refund_payload='{"reason":"The controlled payment-provider drill validates the durable refund workflow."}'
jq -e '.status == "refund_requested" and .paymentMode == "stripe"' <<<"$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" -H 'Content-Type: application/json' -H "Idempotency-Key: $refund_key" -d "$refund_payload" "$api_url/orders/$order_id/refund")" >/dev/null
jq -e '.status == "refund_requested"' <<<"$(curl -fsS -c "$buyer_jar" -b "$buyer_jar" -H 'Content-Type: application/json' -H "Idempotency-Key: $refund_key" -d "$refund_payload" "$api_url/orders/$order_id/refund")" >/dev/null
# Refund submission commits a durable job; allow the worker to dispatch it.
for _ in {1..200}; do
  fixture=$(curl -fsS "http://127.0.0.1:${fixture_port}/__fixture/state")
  [[ "$(jq -r '.refundCalls' <<<"$fixture")" == "1" ]] && break
  sleep 0.1
done
refund_operation=$(jq -er '.lastRefundOperationId | select(length > 0)' <<<"$fixture")
jq -e '.checkoutCalls == 1 and .refundCalls == 1 and .lastRefundPaymentIntent == "pi_test_paymentdrill001" and (.lastRefundIdempotencyKey | startswith("refund-"))' <<<"$fixture" >/dev/null

refund_event="evt_paymentdrillrefund${RANDOM}"
refund_body=$(jq -cn --arg event "$refund_event" --arg operation "$refund_operation" --arg payment "$payment_id" --arg product "$product_id" --argjson created "$created" --argjson amount "$amount" '{id:$event,object:"event",api_version:"2026-02-25.clover",created:$created,livemode:false,type:"refund.updated",data:{object:{id:"re_test_paymentdrill001",object:"refund",status:"succeeded",amount:$amount,currency:"usd",payment_intent:"pi_test_paymentdrill001",metadata:{hcai_payment_id:$payment,hcai_resource_id:$product,hcai_purpose:"product",hcai_refund_operation_id:$operation}}}}')
jq -e --arg event "$refund_event" '.duplicate == false and .providerEventId == $event' <<<"$(submit_event "$refund_body" "$created")" >/dev/null
jq -e '.status == "refunded" and .refundedAt != null' <<<"$(wait_order refunded)" >/dev/null
jq -e '.duplicate == true' <<<"$(submit_event "$refund_body" "$created")" >/dev/null

# The signed refund revokes the media endpoint and queues physical copy cleanup.
[[ "$(curl -sS -o /dev/null -w '%{http_code}' -b "$buyer_jar" "$api_url/assets/$asset_id/content")" == 403 ]]
for _ in {1..200}; do
  [[ "$(sql "SELECT state FROM product_delivery_snapshots WHERE order_id='$order_id'")" == removed ]] && break
  sleep 0.1
done
[[ "$(sql "SELECT state FROM product_delivery_snapshots WHERE order_id='$order_id'")" == removed && ! -e "$MEDIA_ROOT/$delivery_key" ]]

admin=$(curl -fsS -c "$admin_jar" -b "$admin_jar" "$api_url/admin/payments?q=$payment_id&attention=healthy")
jq -e --arg id "$payment_id" '.items | length == 1 and .[0].id == $id and .[0].status == "refunded" and .[0].attentionCode == "none" and .[0].providerEvent.processingState == "processed"' <<<"$admin" >/dev/null
evidence=$(sql "SELECT o.status||'|'||pi.status||'|'||(SELECT count(*) FROM entitlements e WHERE e.order_id=o.id AND e.status='active')||'|'||(SELECT count(*) FROM payment_provider_events e WHERE e.payment_id=pi.id)||'|'||(SELECT count(*) FROM payment_provider_event_processing p WHERE p.status='processed')||'|'||(SELECT count(*) FROM jobs j WHERE j.kind='payment.process_event' AND j.status='succeeded') FROM orders o JOIN payment_intents pi ON pi.order_id=o.id WHERE o.id='$order_id'")
[[ "$evidence" == "refunded|refunded|0|2|3|3" ]] || { echo "Unexpected payment drill evidence: $evidence" >&2; exit 1; }
payout_evidence=$(sql "SELECT status||'|'||charges_enabled||'|'||payouts_enabled||'|'||details_submitted||'|'||requirements_due||'|'||(SELECT count(*) FROM payment_provider_events WHERE provider_event_id='$payout_event' AND destination_id='acct_test_paymentdrill001') FROM payment_destinations WHERE provider='stripe' AND user_id='$payout_user_id'")
[[ "$payout_evidence" == "verified|true|true|true|false|1" ]] || { echo "Unexpected payout drill evidence: $payout_evidence" >&2; exit 1; }
fixture=$(curl -fsS "http://127.0.0.1:${fixture_port}/__fixture/state")
jq -n --arg paymentId "$payment_id" --arg orderId "$order_id" --arg evidence "$evidence" --arg payoutEvidence "$payout_evidence" --argjson fixture "$fixture" '{status:"passed",paymentId:$paymentId,orderId:$orderId,databaseEvidence:$evidence,payoutEvidence:$payoutEvidence,checkoutReplay:true,webhookReplay:true,refundReplay:true,independentDelivery:true,rangeVerified:true,deliveryCleanup:true,payoutAccountReplay:true,payoutLinkRenewal:true,fixture:$fixture}'
