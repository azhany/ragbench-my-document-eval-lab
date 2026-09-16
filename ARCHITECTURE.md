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

## Document intelligence path

Sprint 7 extends the existing document platform with a bounded financial-document workflow. PostgreSQL remains authoritative; Go owns domain contracts, validation rules, provider/tool interfaces, result APIs, and trace semantics; Airflow remains the asynchronous workflow orchestrator.

```text
financial PDF/JPG/PNG upload
  ↓
persist document + analysis run
  ↓
Airflow document_intelligence workflow
  ↓
text extraction / OCR tool
  ↓
LLM structured extraction tool
  ↓
schema validation
  ↓
deterministic financial validation tool
  ↓
summary generation tool
  ↓
persist result + validation findings + trace/usage/latency
  ↓
reviewer-facing API/UI result
```

### Boundary with the RAG path

The mandatory assessment path does not require retrieval. Structured financial extraction is performed from the uploaded document evidence. Existing RAG infrastructure may optionally retrieve previous invoices/receipts for comparison after the primary extraction result is persisted.

### Agent semantics

`DocumentIntelligenceAgent` is a small explicit orchestrator over named tools. It does not autonomously discover arbitrary tools, mutate infrastructure, or create an additional service boundary. Tool order is constrained so validation always occurs before final summary generation.

### Evidence and reproducibility

Persist enough immutable context to explain a result later: document/revision identity, extraction method, raw extracted text or evidence reference, prompt/version, model profile, schema version, validation policy/version, tool outcomes, usage, latency, retry/error state, and final summary.

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
