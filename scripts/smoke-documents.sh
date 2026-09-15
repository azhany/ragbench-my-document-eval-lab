#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
# Synthetic sources only. Omit --expect-missing-key to require real embeddings.
# Records remain in the local Library for inspection; the TXT fixture is deleted
# through the API to verify cancellation/retention.
docker compose run --rm --no-deps \
  -v "$PWD/airflow/tests:/opt/airflow/tests:ro" \
  airflow-scheduler python /opt/airflow/tests/smoke_documents.py "$@"
