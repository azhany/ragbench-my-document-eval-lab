#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
# Synthetic sources only. Omit --expect-missing-key to require real embeddings.
# Records remain in the local Library for inspection; the TXT fixture is deleted
# through the API to verify cancellation/retention.
docker compose run --rm --no-deps \
  -e SMOKE_MODEL_PROFILE="${SMOKE_MODEL_PROFILE:-openai-gpt-4o-mini}" \
  -e SMOKE_EMBEDDING_PROFILE="${SMOKE_EMBEDDING_PROFILE:-openai-text-embedding-3-small}" \
  -v "$PWD/airflow/tests:/opt/airflow/tests:ro" \
  airflow-scheduler python /opt/airflow/tests/smoke_documents.py "$@"
