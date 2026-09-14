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


# ---- provider keys (leave uncommitted; required only by later stories) ----
# OPENAI_API_KEY=
# EMBEDDING_PROVIDER_API_KEY=
```

Notes:

- If you override the application database credentials, set `DATABASE_URL` to match.
- Provider API keys belong only in `.env`, which is git-ignored; they are not needed for this foundation stack.

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
