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
- checksum: SHA-256 of the uploaded bytes, unique — the same bytes are the same document
- chunk_count: published chunk count, NULL until a revision is indexed
- created_at
- updated_at

### index_revisions
One revision per (document, chunk settings, embedding profile) combination.
Chunk-size or embedding changes create a new revision instead of overwriting
evidence referenced by historical traces and evaluations.

- id UUID PK
- document_id FK → documents ON DELETE CASCADE
- revision_number: per-document monotonic, ≥ 1; unique with document_id
- source_checksum: checksum of the source bytes this revision was built from
- chunk_size (> 0), chunk_overlap (≥ 0)
- embedding_provider, embedding_model, embedding_dimensions (> 0): embedding
  compatibility identity; retrieval never mixes incompatible revisions
- status: `pending` | `ready` | `failed`, default `pending`
- error_code: set when `failed`
- created_at, published_at (set when `ready`)
- State check: `ready` ⇒ published_at set and no error; `failed` ⇒ error_code
  set; `pending` ⇒ neither.

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
