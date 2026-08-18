#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "$0")/.." && pwd)"
base_database_url="${TEST_DATABASE_URL:-postgres://hcai:hcai@localhost:5432/hcai?sslmode=disable}"
api_port="${PROVIDER_DRILL_HTTP_PORT:-18084}"
fixture_port="${PROVIDER_DRILL_FIXTURE_PORT:-18085}"
schema="drill_provider_runtime_$(date +%s)_${RANDOM}"
root="$(mktemp -d "${TMPDIR:-/tmp}/hcai-provider-runtime.XXXXXX")"
api_pid=""
worker_pid=""
fixture_pid=""
api_key="sk-openai-provider-drill"
chat_model="gpt-5.6-terra"
image_model="gpt-image-2"
organization="org_provider_drill"
project="proj_provider_drill"
admin_cost_key="sk-openai-admin-cost-drill"

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

for command in psql jq curl lsof; do command -v "$command" >/dev/null || { echo "provider drill requires $command" >&2; exit 1; }; done
for port in "$api_port" "$fixture_port"; do
  if ! [[ "$port" =~ ^[0-9]+$ ]] || (( port < 1024 || port > 65535 )); then echo "Provider drill ports must be 1024-65535." >&2; exit 1; fi
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then echo "Provider drill port $port is already in use." >&2; exit 1; fi
done
if [[ "$api_port" == "$fixture_port" ]]; then echo "Provider drill ports must differ." >&2; exit 1; fi

cd "$project_root"
PGOPTIONS="-c search_path=public" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "CREATE SCHEMA $schema" >/dev/null
separator="?"; [[ "$base_database_url" == *"?"* ]] && separator="&"
export APP_ENV=test DATABASE_URL="${base_database_url}${separator}search_path=${schema}" HTTP_ADDR="127.0.0.1:${api_port}"
export WEB_ORIGIN="http://127.0.0.1:${api_port}" MEDIA_ROOT="$root/media" LOCAL_PROVIDER_SOURCE="$project_root/web/public/media/home-cinematic.jpg"
export LOCAL_PROVIDER_ENABLED=true DEMO_DATA_ENABLED=true EMAIL_DELIVERY_MODE=disabled
export OPENAI_ENABLED=true OPENAI_PAID_CALLS_APPROVED=true OPENAI_API_KEY="$api_key" OPENAI_BASE_URL="http://127.0.0.1:${fixture_port}/v1"
export OPENAI_CHAT_MODEL="$chat_model" OPENAI_IMAGE_MODEL="$image_model" OPENAI_CHAT_MAX_OUTPUT_TOKENS=2048 OPENAI_IMAGE_SIZE=1024x1024 OPENAI_IMAGE_QUALITY=medium
export OPENAI_ORGANIZATION="$organization" OPENAI_PROJECT="$project" OPENAI_RECONCILIATION_ENABLED=true OPENAI_RECONCILIATION_APPROVED=true OPENAI_ADMIN_API_KEY="$admin_cost_key" OPENAI_RECONCILIATION_OVERAGE_THRESHOLD_MICROS=10000

go run ./cmd/migrate >/dev/null
go run ./cmd/seed >/dev/null
go build -o "$root/hcai-api" ./cmd/api
go build -o "$root/hcai-worker" ./cmd/worker
go build -o "$root/openai-fixture" ./cmd/openaifixture

"$root/openai-fixture" -addr "127.0.0.1:${fixture_port}" -api-key "$api_key" -admin-api-key "$admin_cost_key" -chat-model "$chat_model" -image-model "$image_model" -organization "$organization" -project "$project" >"$root/fixture.log" 2>&1 & fixture_pid=$!
for _ in {1..100}; do curl -fsS "http://127.0.0.1:${fixture_port}/ready" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "http://127.0.0.1:${fixture_port}/ready" >/dev/null
"$root/hcai-api" >"$root/api.log" 2>&1 & api_pid=$!
for _ in {1..100}; do curl -fsS "http://127.0.0.1:${api_port}/ready" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "http://127.0.0.1:${api_port}/ready" >/dev/null
"$root/hcai-worker" >"$root/worker.log" 2>&1 & worker_pid=$!
for _ in {1..100}; do rg -q '"msg":"worker started"' "$root/worker.log" && break; sleep 0.1; done
rg -q '"msg":"worker started"' "$root/worker.log"

api_url="http://127.0.0.1:${api_port}/api/v1"
admin_jar="$root/admin.cookies"; creator_jar="$root/creator.cookies"
curl -fsS -c "$admin_jar" -b "$admin_jar" -H 'Content-Type: application/json' -d '{"actor":"admin"}' "$api_url/auth/demo" >/dev/null
curl -fsS -c "$creator_jar" -b "$creator_jar" -H 'Content-Type: application/json' -d '{"actor":"creator"}' "$api_url/auth/demo" >/dev/null

providers=$(curl -fsS -c "$admin_jar" -b "$admin_jar" "$api_url/admin/providers")
jq -e --arg chat "$chat_model" --arg image "$image_model" '.items | any(.[]; .id == "openai-chat" and .runtimeAvailable == true and .adminEnabled == false and .modelName == $chat) and any(.[]; .id == "openai-image" and .runtimeAvailable == true and .adminEnabled == false and .modelName == $image)' <<<"$providers" >/dev/null
enable_provider() {
  local id="$1"
  curl -fsS -X PATCH -c "$admin_jar" -b "$admin_jar" -H 'Content-Type: application/json' -d '{"enabled":true,"reason":"Enable the configured loopback Provider runtime for acceptance evidence.","confirmed":true}' "$api_url/admin/providers/$id"
}
enable_provider openai-chat >/dev/null
enable_provider openai-image >/dev/null
routes=$(curl -fsS -c "$admin_jar" -b "$admin_jar" "$api_url/admin/models/routes")
chat_version=$(jq -er '.routes.chat.version' <<<"$routes")
image_version=$(jq -er '.routes.image.version' <<<"$routes")
activate_route() {
  local mode="$1" profile="$2" expected="$3" name="$4"
  curl -fsS -X POST -c "$admin_jar" -b "$admin_jar" -H 'Content-Type: application/json' \
    -d "$(jq -cn --arg profile "$profile" --arg name "$name" --argjson expected "$expected" '{providerProfileId:$profile,name:$name,timeoutSeconds:60,maxAttempts:2,reason:"Activate the verified loopback Provider runtime with bounded retry evidence.",expectedVersion:$expected,confirmed:true}')" \
    "$api_url/admin/models/routes/$mode"
}
chat_route=$(activate_route chat openai-chat "$chat_version" "Provider drill Chat route")
image_route=$(activate_route image openai-image "$image_version" "Provider drill Image route")
jq -e --arg model "$chat_model" '.routes.chat.provider == "openai" and .routes.chat.providerRuntimeReady == true and .routes.chat.modelName == $model' <<<"$chat_route" >/dev/null
jq -e --arg model "$image_model" '.routes.image.provider == "openai" and .routes.image.providerRuntimeReady == true and .routes.image.modelName == $model' <<<"$image_route" >/dev/null

submit_generation() {
  local mode="$1" prompt="$2" key="$3"
  curl -fsS -c "$creator_jar" -b "$creator_jar" -H 'Content-Type: application/json' -H "Idempotency-Key: $key" \
    -d "$(jq -cn --arg mode "$mode" --arg prompt "$prompt" '{mode:$mode,prompt:$prompt}')" "$api_url/generations"
}
wait_generation() {
  local id="$1" item=""
  for _ in {1..250}; do
    item=$(curl -fsS -c "$creator_jar" -b "$creator_jar" "$api_url/generations/$id")
    [[ "$(jq -r '.status' <<<"$item")" == "succeeded" ]] && { printf '%s' "$item"; return 0; }
    sleep 0.1
  done
  echo "Generation $id did not succeed" >&2; return 1
}

run_id="$(date +%s)-${RANDOM}"
chat_key="provider-runtime-chat-$run_id"; image_key="provider-runtime-image-$run_id"
chat=$(submit_generation chat "Provider drill Chat prompt $run_id" "$chat_key")
image=$(submit_generation image "Provider drill Image prompt $run_id" "$image_key")
jq -e '.status == "queued" and .provider == "openai" and .modelName == "gpt-5.6-terra"' <<<"$chat" >/dev/null
jq -e '.status == "queued" and .provider == "openai" and .modelName == "gpt-image-2"' <<<"$image" >/dev/null
chat_id=$(jq -er '.id' <<<"$chat"); image_id=$(jq -er '.id' <<<"$image")
chat_done=$(wait_generation "$chat_id"); image_done=$(wait_generation "$image_id")
jq -e '.status == "succeeded" and .outputAssetId != null and .provider == "openai" and .providerUsage.status == "reported" and .providerUsage.inputTokens == 19 and .providerUsage.cachedInputTokens == 4 and .providerUsage.outputTokens == 11 and .providerUsage.reasoningTokens == 3 and .providerUsage.totalTokens == 30' <<<"$chat_done" >/dev/null
jq -e '.status == "succeeded" and .outputAssetId != null and .provider == "openai" and .providerUsage.status == "reported" and .providerUsage.inputTokens == 37 and .providerUsage.cachedInputTokens == 5 and .providerUsage.outputTokens == 2048 and .providerUsage.reasoningTokens == 0 and .providerUsage.totalTokens == 2085' <<<"$image_done" >/dev/null
chat_asset=$(jq -er '.outputAssetId' <<<"$chat_done"); image_asset=$(jq -er '.outputAssetId' <<<"$image_done")
chat_asset_json=$(curl -fsS -c "$creator_jar" -b "$creator_jar" "$api_url/assets/$chat_asset")
image_asset_json=$(curl -fsS -c "$creator_jar" -b "$creator_jar" "$api_url/assets/$image_asset")
jq -e '.mimeType == "text/plain; charset=utf-8" and .sourceType == "generation"' <<<"$chat_asset_json" >/dev/null
jq -e '.mimeType == "image/png" and .width == 4 and .height == 3 and .sourceType == "generation"' <<<"$image_asset_json" >/dev/null
chat_replay=$(submit_generation chat "Provider drill Chat prompt $run_id" "$chat_key")
image_replay=$(submit_generation image "Provider drill Image prompt $run_id" "$image_key")
jq -e --arg id "$chat_id" '.id == $id and .status == "succeeded"' <<<"$chat_replay" >/dev/null
jq -e --arg id "$image_id" '.id == $id and .status == "succeeded"' <<<"$image_replay" >/dev/null

period_start="$(date -u '+%Y-%m-%dT00:00:00Z')"
if period_end="$(date -u -d 'tomorrow 00:00' '+%Y-%m-%dT00:00:00Z' 2>/dev/null)"; then :; else
  period_end="$(date -u -j -v+1d -f '%Y-%m-%dT%H:%M:%SZ' "$period_start" '+%Y-%m-%dT00:00:00Z')"
fi
reconciliation=$(curl -fsS -c "$admin_jar" -b "$admin_jar" -H 'Content-Type: application/json' -X POST \
  -d "$(jq -cn --arg start "$period_start" --arg end "$period_end" '{provider:"openai",periodStart:$start,periodEnd:$end,reason:"Reconcile the approved loopback OpenAI daily aggregate cost period.",confirmed:true}')" \
  "$api_url/admin/provider-cost-reconciliations")
jq -e '.status == "queued" and .provider == "openai" and .jobId != null' <<<"$reconciliation" >/dev/null
reconciliation_id=$(jq -er '.id' <<<"$reconciliation")
for _ in {1..250}; do
  reconciliation=$(curl -fsS -c "$admin_jar" -b "$admin_jar" "$api_url/admin/provider-cost-reconciliations?status=matched")
  if jq -e --arg id "$reconciliation_id" '.items | any(.[]; .id == $id and .status == "matched")' <<<"$reconciliation" >/dev/null; then break; fi
  sleep 0.1
done
jq -e --arg id "$reconciliation_id" '.items | any(.[]; .id == $id and .providerCostMicros == 350000 and .localEstimatedCostMicros == 350000 and .varianceMicros == 0)' <<<"$reconciliation" >/dev/null

fixture=$(curl -fsS "http://127.0.0.1:${fixture_port}/__fixture/state")
jq -e --arg chat "$chat_model" --arg image "$image_model" '.responsesCalls == 1 and .imageCalls == 1 and .costsCalls == 1 and .lastChatModel == $chat and .lastImageModel == $image' <<<"$fixture" >/dev/null
sql() { PGOPTIONS="-c search_path=$schema" psql "$base_database_url" -v ON_ERROR_STOP=1 -qAtc "$1"; }
evidence=$(sql "SELECT (SELECT count(*) FROM generations WHERE id IN ('$chat_id','$image_id') AND status='succeeded')||'|'||(SELECT count(*) FROM assets WHERE id IN ('$chat_asset','$image_asset') AND source_type='generation')||'|'||(SELECT count(*) FROM billing_entries WHERE operation_id IN ('$chat_id','$image_id') AND entry_type='generation_charge')||'|'||(SELECT count(*) FROM jobs WHERE kind='generation.generate' AND payload->>'generationId' IN ('$chat_id','$image_id') AND status='succeeded')||'|'||(SELECT count(*) FROM job_attempts a JOIN jobs j ON j.id=a.job_id WHERE j.kind='generation.generate' AND j.payload->>'generationId' IN ('$chat_id','$image_id') AND a.status='succeeded')||'|'||(SELECT count(*) FROM generation_provider_usage WHERE generation_id IN ('$chat_id','$image_id') AND status='reported')||'|'||(SELECT sum(input_tokens) FROM generation_provider_usage WHERE generation_id IN ('$chat_id','$image_id'))||'|'||(SELECT sum(total_tokens) FROM generation_provider_usage WHERE generation_id IN ('$chat_id','$image_id'))")
[[ "$evidence" == "2|2|2|2|2|2|56|2115" ]] || { echo "Unexpected Provider drill evidence: $evidence" >&2; exit 1; }
reconciliation_evidence=$(sql "SELECT status||'|'||provider_cost_micros||'|'||local_estimated_cost_micros||'|'||variance_micros FROM provider_cost_reconciliations WHERE id='$reconciliation_id'")
[[ "$reconciliation_evidence" == "matched|350000|350000|0" ]] || { echo "Unexpected reconciliation evidence: $reconciliation_evidence" >&2; exit 1; }
jq -n --arg chatId "$chat_id" --arg imageId "$image_id" --arg chatAsset "$chat_asset" --arg imageAsset "$image_asset" --arg evidence "$evidence" --arg reconciliationId "$reconciliation_id" --arg reconciliationEvidence "$reconciliation_evidence" --argjson fixture "$fixture" '{status:"passed",chatGenerationId:$chatId,imageGenerationId:$imageId,chatAssetId:$chatAsset,imageAssetId:$imageAsset,databaseEvidence:$evidence,reconciliationId:$reconciliationId,reconciliationEvidence:$reconciliationEvidence,routeActivation:true,generationReplay:true,fixture:$fixture}'
