#!/bin/sh
# RB-24: end-to-end, evidence-preserving PoC walkthrough.
# This script never creates scores or changes the persisted regression policy.
set -eu

API_URL=${API_URL:-http://localhost:8080}
WAIT_SECONDS=${WAIT_SECONDS:-600}
CONFIG_NAME=${CONFIG_NAME:-poc-baseline-$(date +%s)}
MODEL_PROFILE=${MODEL_PROFILE:-opencode-go-glm-5.3-flash}
EMBEDDING_PROFILE=${EMBEDDING_PROFILE:-huggingface-bge-small-en-v1.5}

command -v curl >/dev/null || { echo "curl is required" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 2; }

get() { curl --fail --silent --show-error "$API_URL$1"; }
post_json() { curl --fail --silent --show-error -H 'Content-Type: application/json' -d "$2" "$API_URL$1"; }

echo "Waiting for API readiness…"
deadline=$(( $(date +%s) + WAIT_SECONDS ))
while ! curl --silent --fail "$API_URL/readyz" >/dev/null; do
  [ "$(date +%s)" -lt "$deadline" ] || { echo "API did not become ready" >&2; exit 1; }
  sleep 5
done

config=$(post_json /api/v1/rag-configs "$(jq -n --arg n "$CONFIG_NAME" --arg model "$MODEL_PROFILE" --arg emb "$EMBEDDING_PROFILE" '{name:$n,chunk_size:800,chunk_overlap:120,retrieval_mode:"vector",top_k:5,rerank_enabled:false,prompt_version:"v1",model_profile:$model,embedding_profile:$emb}')")
config_id=$(printf '%s' "$config" | jq -r '.id')
[ -n "$config_id" ] && [ "$config_id" != null ] || { echo "configuration creation failed" >&2; exit 1; }

echo "Seeding reviewed corpus and golden dataset with real providers…"
API_URL="$API_URL" GOLDEN_CONFIG_NAME="$CONFIG_NAME" python3 scripts/seed-golden.py
datasets=$(get /api/v1/eval-datasets)
dataset_id=$(printf '%s' "$datasets" | jq -r '.datasets[] | select(.name == "golden-dataset-v1") | .id' | head -n 1)
version=$(printf '%s' "$datasets" | jq -r '.datasets[] | select(.name == "golden-dataset-v1") | .latest_version' | head -n 1)
[ -n "$dataset_id" ] && [ "$dataset_id" != null ] || { echo "golden dataset was not created" >&2; exit 1; }
[ -n "$version" ] && [ "$version" != null ] || { echo "golden dataset has no latest version" >&2; exit 1; }

echo "Launching baseline evaluation through Airflow…"
baseline=$(post_json /api/v1/eval-runs "$(jq -n --arg d "$dataset_id" --arg c "$config_id" --argjson v "$version" '{dataset_id:$d,dataset_version:$v,rag_config_id:$c,rubric_version:"rubric-v1",scoring_k:5}')")
baseline_id=$(printf '%s' "$baseline" | jq -r '.id')
[ -n "$baseline_id" ] && [ "$baseline_id" != null ] || { echo "baseline launch failed" >&2; exit 1; }

echo "Baseline run: $baseline_id"
echo "Use the same dataset and a separately saved degraded config for the intentional poor-retrieval demonstration in TEST_PLAN.md; this script reports observed values only and never adjusts thresholds."
echo "Inspect: $API_URL/api/v1/eval-runs/$baseline_id and $API_URL/api/v1/metrics/summary"
