# Architecture

## Style

Modern modular monolith for the Go application, with Airflow as the asynchronous data/evaluation pipeline.

PostgreSQL is authoritative for durable state.

## High-level architecture

```text
                         ┌────────────────────┐
                         │      Vue.js UI     │
                         │ Library / Chat /   │
                         │ Eval / Monitor     │
                         └─────────┬──────────┘
                                   │ HTTP/JSON
                         ┌─────────▼──────────┐
                         │       Go API       │
                         │ documents          │
                         │ rag/query          │
                         │ experiments        │
                         │ metrics/traces     │
                         └──────┬─────┬───────┘
                                │     │
                         SQL    │     │ trigger/status
                                │     │
                    ┌───────────▼─┐ ┌─▼────────────────┐
                    │ PostgreSQL │ │ Apache Airflow    │
                    │ + pgvector │ │ ingest/eval DAGs │
                    └──────┬─────┘ └───────┬──────────┘
                           │               │
                           │               ├─ extract
                           │               ├─ chunk
                           │               ├─ embed
                           │               └─ evaluate
                           │
                    ┌──────▼─────────────────┐
                    │ Model / Embedding APIs │
                    │ provider abstraction   │
                    └────────────────────────┘
```

## Query path

```text
question
  ↓
validate
  ↓
embed query
  ↓
retrieve top-k
  ↓
optional full-text merge / hybrid scoring
  ↓
optional rerank
  ↓
prompt build
  ↓
LLM generation
  ↓
citation mapping
  ↓
trace + token + cost persistence
  ↓
response
```

## Ingestion path

```text
upload
  ↓
documents row = queued
  ↓
Airflow DAG
  ↓
extract text
  ↓
normalize
  ↓
chunk
  ↓
embed
  ↓
insert doc_chunks
  ↓
build FTS metadata
  ↓
documents row = processed
```

## Evaluation path

```text
eval dataset + experiment config
  ↓
create eval_run
  ↓
Airflow evaluation DAG
  ↓
for each case:
    execute same RAG query pipeline
    score retrieval
    score generation
    record latency/tokens/cost
  ↓
aggregate metrics
  ↓
compare with baseline thresholds
  ↓
pass / regression
```

## Why Go + Airflow

Go owns the online path:
- APIs
- retrieval orchestration
- deterministic business logic
- request traces
- low-overhead runtime

Airflow owns batch workflows:
- ingestion
- re-indexing
- evaluation suites
- parameter sweeps
- scheduled regression checks

This keeps the PoC close to a realistic document platform without turning the Go service into a workflow engine.

## Observability

Use a lightweight internal tracing model first. Each RAG request gets a `trace_id`.

Suggested spans:
- request
- query_embedding
- retrieval
- rerank
- prompt_build
- llm_generation
- citation_mapping

OpenTelemetry export can be added later without changing the core schema.
