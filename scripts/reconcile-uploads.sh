#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
# Pause new uploads before running. Reports only; never removes source bytes.
docker compose exec -T -e PYTHONPATH=/opt/airflow/dags airflow-scheduler python -m ragbench.reconcile
