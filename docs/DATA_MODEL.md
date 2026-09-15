# Data Model

Schema state is versioned under `db/migrations/` and applied with
`scripts/migrate.sh`; applied versions are tracked in `schema_migrations`
(created by the runner). The Go API verifies the schema version at startup and
on `/readyz` but never creates or alters schema itself.

## Core tables

### documents
- id UUID PK (default `gen_random_uuid()`)
- filename
- mime_type
- storage_path
- status: `queued` | `processing` | `processed` | `failed`, default `queued`
- checksum: SHA-256 of the uploaded bytes, unique among non-deleted documents
- chunk_count: published chunk count, NULL until a revision is indexed
- created_at
- updated_at
- size_bytes: uploaded byte count
- deleted_at: tombstone; excludes the document from Library and new retrieval
- latest_revision_id: most recent attempt, used to fence stale tasks
- active_revision_id: last successfully indexed revision; composite FK enforces document ownership

### index_revisions
Each upload/reprocess appends a new revision, including reprocess with identical settings.
Chunk-size or embedding changes create a new revision instead of overwriting
evidence referenced by historical traces and evaluations.

- id UUID PK
- document_id FK → documents ON DELETE CASCADE
- revision_number: per-document monotonic, ≥ 1; unique with document_id
- source_checksum: checksum of the source bytes this revision was built from
- config_id: saved immutable RAG configuration identity, copied into the revision
- chunk_unit: `unicode_characters` (NFC text, collapsed whitespace, newline between source blocks)
- chunk_size (> 0), chunk_overlap (≥ 0)
- embedding_provider, embedding_model, embedding_dimensions (> 0): embedding
  compatibility identity; retrieval never mixes incompatible revisions
- status: `pending` | `ready` | `failed`, default `pending`
- error_code: set when `failed`
- created_at, published_at (set when `ready`)
- State check: `ready` ⇒ published_at set and no error; `failed` ⇒ error_code
  set; `pending` ⇒ neither.
- At most one pending revision per document. A publication trigger rejects zero chunks or missing/wrong-dimension vectors.

### doc_chunks
- id UUID PK
- index_revision_id FK → index_revisions ON DELETE CASCADE
- document_id FK → documents ON DELETE CASCADE; composite FK
  (index_revision_id, document_id) → index_revisions(id, document_id) keeps
  chunk document ownership consistent with its revision
- chunk_index: ≥ 0, unique with index_revision_id
- content: non-empty
- metadata JSONB
- content_tsv TSVECTOR: generated (`to_tsvector('simple', content)`), GIN-indexed
- embedding VECTOR(1536): NULL until embedded; dimension pinned by migration
  `0003_doc_chunks.sql` — changing it requires a new migration that alters the
  column, rebuilds the HNSW index, and creates new index revisions
- created_at

Retrieval indexes: HNSW on `embedding vector_cosine_ops`, GIN on
`content_tsv`, btree on `document_id`.

Migration 0005 removes the erroneous RB-02 unique `(index_revision_id, document_id)`
chunk constraint: multiple ordered chunks must belong to one revision. The
composite ownership FK and unique `(index_revision_id, chunk_index)` remain.
Chunk UUIDs are UUIDv5(revision UUID, decimal chunk index). Metadata retains
normalized character start/end offsets and all intersecting source page,
paragraph/heading/table-row locations. Offsets are half-open, relative to the
normalized text; they are not PDF pixel positions or DOCX page numbers.

`searchable_chunks(config_id)` is the retrieval boundary for RB-09/RB-17. It
returns only live documents' active ready revisions with non-null vectors and
provider/model/dimensions matching the saved configuration. Direct evidence
lookup by chunk ID is separate and may read retained non-active/deleted sources.

### ingestion_jobs

One durable job per revision, with a unique deterministic Airflow run ID:

- id, index_revision_id (unique FK), dag_id, run_id (unique)
- state: dispatch_pending, dispatch_failed, queued, processing, succeeded, failed, cancelled
- stage, error_code, error_message, dispatch_attempts
- created_at, updated_at, dispatched_at, started_at, finished_at
- stage_metrics JSONB: duration/status per stage; embedding batch size, chunk count,
  and actual provider prompt tokens (null if unavailable)
- artifacts JSONB: internal extracted/normalized/chunk handoff, cleared on successful
  publication. Never exposed through the document API or XCom.

Workers serialize the same job using a PostgreSQL advisory lock. All state
writes lock the owning document and verify that it is live and this job still
owns the latest revision. Provider calls execute outside that row lock so
delete can commit immediately; later provider output is then discarded.
New revision publication, active-pointer switch, count, and job success commit
together. Failed replacement leaves the active pointer and count unchanged.
Tombstoning retains all source/chunk/config evidence indefinitely in this PoC.

### rag_configs
Immutable saved configurations: no update or delete — changing settings
creates a new configuration identity under a new name (migration
`0004_rag_configs.sql`).

- id UUID PK
- name: unique, 1–200 characters
- chunk_size: 1–8192
- chunk_overlap: ≥ 0 and strictly smaller than chunk_size
- retrieval_mode: `vector` | `hybrid`
- top_k: 1–100
- rerank_enabled
- prompt_version: registry-governed (`backend/internal/providers`, currently `v1`)
- model_profile: registry-governed (currently `openai-gpt-4o-mini`)
- embedding_profile: registry-governed key (currently `openai-text-embedding-3-small`)
- embedding_provider, embedding_model, embedding_dimensions: resolved identity
  persisted at creation so the exact embedding identity survives registry
  changes; must stay compatible with `index_revisions`
- created_at

### eval_datasets
- id UUID PK
- name
- version
- created_at

### eval_cases
- id UUID PK
- dataset_id FK
- question
- reference_answer
- expected_document_ids JSONB
- expected_chunk_ids JSONB

### eval_runs
- id UUID PK
- dataset_id FK
- rag_config_id FK
- baseline_run_id nullable
- status
- started_at
- completed_at
- aggregate_metrics JSONB

### eval_results
- id UUID PK
- eval_run_id FK
- eval_case_id FK
- answer
- retrieved_chunk_ids JSONB
- metrics JSONB
- trace_id
- created_at

### rag_traces
- id UUID PK
- trace_id
- request_type
- question
- rag_config_id
- success
- error_code nullable
- total_latency_ms
- input_tokens
- output_tokens
- estimated_cost
- created_at

### rag_spans
- id UUID PK
- trace_id
- span_name
- started_at
- duration_ms
- metadata JSONB
