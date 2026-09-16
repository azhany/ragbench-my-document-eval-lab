# RAGbench-MY — Document Library Eval Lab

A small portfolio PoC based on a document-library/RAG workload, focused on **evaluation, tuning, cost, tracing, and monitoring** rather than building another large AI application.

## Purpose

Demonstrate a production-minded RAG workflow using the preferred stack:

- Go API/backend
- Vue.js frontend
- Apache Airflow for ingestion and evaluation jobs
- PostgreSQL + pgvector
- Docker Compose
- Python only where Airflow/evaluation tooling benefits from the ecosystem

The project should feel like a simplified internal Document Library system: upload documents, process/chunk/embed them, ask grounded questions, inspect citations, run repeatable evaluation suites, compare configurations, and monitor quality/cost/latency.

## Portfolio story

> I took a familiar enterprise Document Library / RAG workflow and added an evaluation and observability control layer so retrieval and generation changes can be measured instead of guessed.

## MVP flows

1. Upload a document.
2. Airflow processes it: extract → normalize → chunk → embed → index.
3. User asks a question in the chat UI.
4. Go retrieves context and calls the configured LLM.
5. The response returns citations plus trace/cost metadata.
6. User runs a fixed evaluation dataset.
7. Evaluation results are stored and compared against a baseline.
8. Monitor page shows latency, error rate, token usage, cost, and quality regressions.

## AI/ML Take-Home Assessment — Document Intelligence Agent

This repository also contains a bounded financial-document intelligence workflow built as Sprint 7 on top of the existing RAGbench-MY platform.

### Assessment flow

```text
PDF/JPG/PNG
  -> text extraction / OCR
  -> LLM structured financial extraction
  -> schema validation
  -> deterministic financial validation
  -> concise summary
  -> persisted trace / latency / usage / validation evidence
```

The implementation intentionally reuses the repository's production-minded foundations instead of creating a separate demo service: durable PostgreSQL state, explicit migrations, Go domain logic/provider interfaces, Airflow batch orchestration, structured errors, trace/cost conventions, Docker Compose, and repeatable tests.

Sprint 7 is an extension of the six-sprint RAG evaluation lab, not a second application. Retrieval over historical invoices or receipts is optional; mandatory extraction and validation operate on the uploaded document evidence.

### Reviewer guide

1. Start the stack using the normal [local-development instructions](#local-development).
2. Apply pending database migrations.
3. Configure the documented model/OCR provider credentials in the uncommitted `.env` file.
4. Create a saved RAG configuration, upload a synthetic PDF/JPG/PNG, and start an analysis using the documented assessment endpoint.
5. Poll/read the analysis until the workflow completes or exposes a classified failure.
6. Inspect structured data, validation findings, summary, and trace/usage metadata.
7. See [`docs/ASSESSMENT_MAPPING.md`](docs/ASSESSMENT_MAPPING.md) for requirement-to-evidence coverage.
8. See [`docs/SPRINT_7_VERIFICATION.md`](docs/SPRINT_7_VERIFICATION.md) for the verified runtime/test evidence.
9. See [`ENGINEERING_NOTES.md`](ENGINEERING_NOTES.md) for architecture decisions, prompt strategy, limitations, guardrails, and production improvements.

The assessment path intentionally uses synthetic fixture content only. A minimal
reviewer flow is:

```sh
CONFIG_ID=$(curl -sS -X POST http://localhost:8080/api/v1/rag-configs \
  -H 'Content-Type: application/json' \
  -d '{"name":"financial-review-v1","chunk_size":500,"chunk_overlap":50,"retrieval_mode":"vector","top_k":5,"prompt_version":"v1","model_profile":"opencode-go-glm-5.3-flash","embedding_profile":"openai-text-embedding-3-small"}' | jq -r .id)
DOCUMENT=$(curl -sS -X POST http://localhost:8080/api/v1/documents \
  -F "config_id=$CONFIG_ID" -F 'file=@./path/to/synthetic-invoice.pdf')
DOCUMENT_ID=$(printf '%s' "$DOCUMENT" | jq -r .id)
curl -sS -X POST "http://localhost:8080/api/v1/documents/$DOCUMENT_ID/analyses" \
  -H 'Content-Type: application/json' -d '{}'
# Poll the returned id:
curl -sS "http://localhost:8080/api/v1/document-analyses/<analysis-id>" | jq
```

The analysis response exposes structured data, validation findings, ordered
tool events, spans, and truthful usage/cost state. `docs/SPRINT_7_VERIFICATION.md`
records which provider-backed happy and failure paths have actually been run.

For a repeatable command-line smoke using the checked-in synthetic fixture:

```sh
sh scripts/smoke-document-intelligence.sh
```

Set `SOURCE_FILE` to a PDF/JPG/PNG path to exercise that upload path and set
`FAILURE_FILE=db/fixtures/financial/corrupt.pdf` to inspect a classified
failure alongside the successful case. A provider credential is required for
the final structured extraction/summary; without one, the analysis still
persists extraction evidence and exposes the provider failure instead of
claiming completion.

## Keep it small

Do **not** build:
- a full multi-tenant SaaS
- complex agents
- Kubernetes
- a large auth/permissions system
- dozens of evaluation metrics

The PoC should prove one thing well: **RAG changes are measurable, comparable, and observable.**

## Local development

### Prerequisites

- Docker with Compose v2 (`docker compose version`)
- Docker Desktop allocated at least 4 GiB of memory. Airflow's official Compose setup requires this minimum; check Docker Desktop **Settings → Resources** before starting the stack.

### Start the stack

```sh
sh scripts/dev.sh
```

`scripts/dev.sh` is the single shell launcher: it builds the images and starts the whole stack in the foreground (`docker compose up --build`). Press `Ctrl-C` to stop; the API drains in-flight requests for up to 10 seconds on `SIGINT`/`SIGTERM`.

### Services and URLs (once the stack is up)

| Service | URL / address | Notes |
|---|---|---|
| Vue frontend | http://localhost:5173 | nginx serves the built app and proxies `/healthz`, `/readyz`, `/api/` to the API |
| Go API | http://localhost:8080 | health: `GET /healthz`, readiness: `GET /readyz` (see `docs/API.md`) |
| Airflow UI / REST API | http://localhost:9090 | served by the Airflow 3 API server; DAGs load from `./airflow/dags` |
| Application PostgreSQL | `localhost:5432` | pgvector image `pgvector/pgvector:pg16` |
| Airflow metadata PostgreSQL | internal (`airflow-metadata-db`) | separate `postgres:16` service; not published to the host |

Airflow runs with `LocalExecutor` (no Redis/Celery). `airflow-init` migrates the Airflow metadata database and creates the admin user before the API server, DAG processor, and scheduler start.

### Environment configuration

All services run with safe local defaults; no `.env` file is required. For overrides, create a `.env` file in the repository root (git-ignored — never commit it). Example:

```sh
# ---- optional local overrides (defaults shown) ----
POSTGRES_USER=ragbench
POSTGRES_PASSWORD=ragbench
POSTGRES_DB=ragbench
POSTGRES_PORT=5432
DATABASE_URL=postgres://ragbench:ragbench@postgres:5432/ragbench?sslmode=disable
RAGBENCH_API_PORT=8080
FRONTEND_PORT=5173
AIRFLOW_UI_PORT=9090

# ---- Airflow local admin (metadata database) ----
_AIRFLOW_WWW_USER_USERNAME=airflow
_AIRFLOW_WWW_USER_PASSWORD=airflow
# ---- Airflow intra-service signing (must match every Airflow component) ----
AIRFLOW__API__SECRET_KEY=ragbench-airflow-local-api-secret-change-me
AIRFLOW__API_AUTH__JWT_SECRET=ragbench-airflow-local-jwt-secret-change-me


# ---- provider credentials (leave uncommitted) ----
# OPENAI_API_KEY=
# EMBEDDING_PROVIDER_API_KEY=
# HF_TOKEN=
# OPENCODE_API_KEY=
# OPENAI_BASE_URL=https://api.openai.com/v1
# HUGGINGFACE_EMBEDDING_BASE_URL=https://router.huggingface.co/hf-inference/models
# OPENAI_COMPATIBLE_BASE_URL=https://opencode.ai/zen/go/v1
```

Notes:

- If you override the application database credentials, set `DATABASE_URL` to match.
- Provider API keys belong only in `.env`, which is git-ignored. Upload/extraction
  run without a key; embedding reports `embedding_failed` until one is configured.

### Local development credentials

| Credential | Value | Used by |
|---|---|---|
| Application DB user / password / database | `ragbench` / `ragbench` / `ragbench` | Go API |
| Airflow metadata DB user / password / database | `airflow` / `airflow` / `airflow` | Airflow components (internal) |
| Airflow UI login | `airflow` / `airflow` | http://localhost:9090 |

These are local development credentials only. Change them via `.env` before exposing anything beyond localhost.

### Startup and shutdown behavior

- Every database, backend, frontend, and Airflow component declares a healthcheck; dependent services wait on `service_healthy` / `service_completed_successfully` conditions.
- Airflow components only start after `airflow-init` completes; a failed migration or user creation stops the init container and blocks the Airflow services with a visible error.
- If the application database is unavailable, the API still starts and serves `200` on `/healthz`, while `/readyz` returns `503` with the failing check (see `docs/API.md`).
- On failure, inspect the named service first: `docker compose logs <service>`. Startup errors in the API log identify the failed dependency (for example, the `postgres` dependency in the startup warning).
### Database migrations

Application schema changes are numbered SQL migrations under `db/migrations/`,
applied with the shell entry point (PostgreSQL is the source of truth; the API
never creates schema on its own):

```sh
sh scripts/migrate.sh          # apply pending migrations
sh scripts/migrate.sh status   # list applied/pending migrations
```

The runner is idempotent — applied migrations recorded in `schema_migrations`
are skipped — and each migration commits atomically with its bookkeeping
record, so a failed migration leaves no partial state and exits non-zero with
the failing file named. Conventions live in `db/migrations/README.md`.

The API verifies the schema version at startup and on `/readyz` (the `schema`
check): against an unmigrated or mismatched database, `/readyz` returns `503`
until `scripts/migrate.sh` has been run. See `docs/API.md` for the readiness
response shape.

### Observable PoC walkthrough (Sprint 6)

After the stack is ready and provider credentials are authorized, run:

```sh
sh scripts/smoke-poc.sh
```

This seeds the reviewed corpus/dataset with real provider calls, launches a
normal Airflow-backed evaluation, and prints durable IDs for inspection. It
does not fabricate scores or alter comparison thresholds. Use the
poor-retrieval procedure in `docs/TEST_PLAN.md` for the intentionally degraded
candidate and retain the observed result, including `not_evaluable` when a
provider or corpus prerequisite fails.

Scheduled regression checks are disabled by default. Create a persisted check
through `POST /api/v1/regression-checks`, then set
`RAGBENCH_SCHEDULE_ENABLED=true` and an explicit `RAGBENCH_SCHEDULE_CRON` in
`.env`, plus `RAGBENCH_SCHEDULE_CHECK_ID=<check-id>` for cron-triggered runs,
before recreating Airflow. A manual DAG trigger can provide
`dag_run.conf.check_id` instead. Disable the schedule after the authorized
run.

### Document Library (Sprint 2)

Run `sh scripts/migrate.sh` after starting PostgreSQL, then rebuild with
`docker compose up -d --build`. Open http://localhost:5173/library. Save a RAG
configuration using the documented `POST /api/v1/rag-configs` contract first.
Select it in Library and upload a text-bearing PDF, DOCX, or UTF-8 TXT.
Library refreshes every five seconds while documents are listed and exposes the actual error, published chunk count,
revision history, reprocessing, dispatch recovery, and deletion.

Choose a registered provider profile and set its credential in `.env`, then
recreate the backend and Airflow services. OpenAI uses `OPENAI_API_KEY` (or the
embedding-only `EMBEDDING_PROVIDER_API_KEY`); native Hugging Face feature
extraction uses `HF_TOKEN`/`HUGGINGFACE_API_KEY`; OpenCode Go generation uses
`OPENCODE_API_KEY`/`GENERATION_PROVIDER_API_KEY`. The existing generic
`EMBEDDING_PROVIDER_API_KEY` and `OPENAI_API_KEY` names remain compatible
fallbacks. Go dispatches through Airflow 3's
JWT/v2 API using the existing local admin credentials; keys never reach Vue.

| Setting | Default | Purpose |
|---|---|---|
| `MAX_UPLOAD_BYTES` | 20971520 | Maximum source file bytes, 1–1073741824 |
| `MAX_EXTRACTED_CHARS` | 2000000 | Extraction output guard |
| `MAX_DOCX_EXPANDED_BYTES` | 104857600 | Expanded DOCX archive guard |
| `MAX_IMAGE_PIXELS` | 25000000 | Maximum decoded JPG/PNG pixels |
| `OCR_TIMEOUT_SECONDS` | 30 | Maximum Tesseract OCR time per image |
| `EMBEDDING_BATCH_SIZE` | 16 | Embedding request batch, 1–128 chunks |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | OpenAI or an explicitly selected OpenAI deployment base |
| `HUGGINGFACE_EMBEDDING_BASE_URL` | `https://router.huggingface.co/hf-inference/models` | Native Hugging Face feature-extraction model base |
| `OPENAI_COMPATIBLE_BASE_URL` | `https://opencode.ai/zen/go/v1` | OpenAI-compatible Chat Completions base (OpenCode Go by default) |
| `UPLOAD_DIR` | `/data/uploads` | Same shared path in Go and Airflow |

Executable provider profiles are `openai-text-embedding-3-small` (1536d),
`huggingface-bge-small-en-v1.5` (384d), `openai-gpt-4o-mini`, and
`opencode-go-glm-5.3-flash`. For a complete non-OpenAI provider smoke:

```sh
SMOKE_EMBEDDING_PROFILE=huggingface-bge-small-en-v1.5 \
SMOKE_MODEL_PROFILE=opencode-go-glm-5.3-flash \
  sh scripts/smoke-documents.sh --chat
```

The upload volume uses shared group 0 and setgid directory permissions; Go
remains UID 10001 and Airflow UID 50000. Backend startup waits for volume setup.
Deletion retains historical evidence and excludes the document from new
searches; it does not erase bytes. Failure and recovery contracts are in
`docs/API.md`, and verification commands/remaining gates are in `docs/TEST_PLAN.md`.
