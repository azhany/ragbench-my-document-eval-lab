#!/usr/bin/env sh
# RB-27--RB-32: reviewer-facing upload/analysis smoke.
# SOURCE_FILE may be a synthetic TXT, PDF, JPG, or PNG. The optional
# FAILURE_FILE exercises a corrupt/unsupported extraction path and remains
# visible in PostgreSQL; this script never fabricates a successful result.
set -eu

API_URL=${API_URL:-http://localhost:8080}
SOURCE_FILE=${SOURCE_FILE:-db/fixtures/financial/invoice_valid.txt}
FAILURE_FILE=${FAILURE_FILE:-}
MODEL_PROFILE=${MODEL_PROFILE:-opencode-go-glm-5.3-flash}
EMBEDDING_PROFILE=${EMBEDDING_PROFILE:-openai-text-embedding-3-small}
WAIT_SECONDS=${WAIT_SECONDS:-180}

command -v curl >/dev/null || { echo "curl is required" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
[ -f "$SOURCE_FILE" ] || { echo "source file does not exist: $SOURCE_FILE" >&2; exit 2; }

echo "Waiting for API readiness…"
deadline=$(( $(date +%s) + WAIT_SECONDS ))
while ! curl --silent --fail "$API_URL/readyz" >/dev/null; do
  [ "$(date +%s)" -lt "$deadline" ] || { echo "API did not become ready" >&2; exit 1; }
  sleep 3
done

config_name="financial-smoke-$(date +%s)"
config=$(curl --silent --show-error -X POST "$API_URL/api/v1/rag-configs" \
  -H 'Content-Type: application/json' \
  -d "$(jq -n --arg name "$config_name" --arg model "$MODEL_PROFILE" --arg embedding "$EMBEDDING_PROFILE" \
    '{name:$name,chunk_size:500,chunk_overlap:50,retrieval_mode:"vector",top_k:5,prompt_version:"v1",model_profile:$model,embedding_profile:$embedding}')")
config_id=$(printf '%s' "$config" | jq -r '.id // empty')
[ -n "$config_id" ] || { echo "configuration creation failed: $config" >&2; exit 1; }

wait_for_analysis() {
  analysis_id=$1
  deadline=$(( $(date +%s) + WAIT_SECONDS ))
  while :; do
    result=$(curl --silent --show-error "$API_URL/api/v1/document-analyses/$analysis_id")
    status=$(printf '%s' "$result" | jq -r '.status // "unknown"')
    stage=$(printf '%s' "$result" | jq -r '.stage // "unknown"')
    echo "analysis=$analysis_id status=$status stage=$stage"
    case "$status" in
      completed|partial|failed|cancelled)
        printf '%s\n' "$result" | jq
        return 0
        ;;
    esac
    [ "$(date +%s)" -lt "$deadline" ] || { echo "analysis polling timed out" >&2; return 1; }
    sleep 3
  done
}

run_case() {
  file=$1
  echo "Uploading $file"
  document=$(curl --silent --show-error -X POST "$API_URL/api/v1/documents" \
    -F "config_id=$config_id" -F "file=@$file")
  document_id=$(printf '%s' "$document" | jq -r '.id // empty')
  [ -n "$document_id" ] || { echo "document upload failed: $document" >&2; return 1; }
  echo "document=$document_id"
  analysis=$(curl --silent --show-error -X POST "$API_URL/api/v1/documents/$document_id/analyses" \
    -H 'Content-Type: application/json' -d '{}')
  analysis_id=$(printf '%s' "$analysis" | jq -r '.id // .analysis.id // empty')
  [ -n "$analysis_id" ] || { echo "analysis start failed: $analysis" >&2; return 1; }
  wait_for_analysis "$analysis_id"
}

run_case "$SOURCE_FILE"
if [ -n "$FAILURE_FILE" ]; then
  [ -f "$FAILURE_FILE" ] || { echo "failure file does not exist: $FAILURE_FILE" >&2; exit 2; }
  run_case "$FAILURE_FILE"
fi
