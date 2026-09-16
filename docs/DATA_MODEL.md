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
- embedding VECTOR: NULL until embedded; migration `0008` removes the original
  1536 typmod so registered profiles can retain their native dimensions.
  Publication verifies every vector against its revision identity. Partial
  HNSW expression indexes cover the registered 1536d and 384d profiles; adding
  another dimension requires an explicit companion index migration.
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
- model_profile: registry-governed (`openai-gpt-4o-mini`,
  `opencode-go-glm-5.3-flash`, testing-only `opencode-zen-big-pickle`, or
  fallback `opencode-zen-mimo-v2.5-free`, or `huggingface-gemma-3-4b-it-free`)
- embedding_profile: registry-governed key (`openai-text-embedding-3-small` or
  `huggingface-bge-small-en-v1.5`)
- embedding_provider, embedding_model, embedding_dimensions: resolved identity
  persisted at creation so the exact embedding identity survives registry
  changes; must stay compatible with `index_revisions`
- created_at

### eval_datasets (implemented, `0009`/`0013`)
- id UUID PK
- name UNIQUE (1–200 chars)
- description
- latest_version 1–9999 (immutable numbered case versions)
- created_at

### eval_cases (implemented, `0009`/`0013`)
- id UUID PK
- dataset_id FK
- version 1–9999, immutable per version (edits create a NEW version)
- case_key UNIQUE per (dataset, version)
- question, reference_answer
- expected_evidence JSONB object `{document_ids[], labels[]}` — the stable
  relevance unit is the document identity; chunk UUIDs are never treated as
  ground truth for another index revision, so cross-chunk-size comparisons
  stay valid without remapping
- notes, created_at

### eval_runs (implemented, `0010`)
- id UUID PK
- dataset_id FK + dataset_version (pinned at creation)
- rag_config_id FK (immutable identity; hybrid since `0014`)
- corpus_revisions JSONB (pinned snapshot of per-document active ready
  revisions under the config's embedding identity; null revision entries
  make a missing corpus diagnosable, never silently "ready")
- evaluator_policy JSONB (policy version, rubric version, scoring K)
- status created|running|completed|partial|failed|dispatch_failed
  (dispatch failure is a visible failure, never success)
- dag_run_id UNIQUE, dispatch_attempts, dispatch_error
- created_at, updated_at

### eval_results (`0010`)
- id UUID PK
- run_id FK, eval_case_id (UNIQUE per run+case: idempotent retries store
  exactly one terminal row)
- case_key, dataset_version, status completed|failed|evaluator_failed
- trace_id (public rag_traces.trace_id; every generated answer is traceable,
  including classified failures)
- query_error_code/message
- recall_k, mrr (document-level, NULL = not evaluable, never zero)
- answer_relevance/rationale, groundedness/rationale (versioned rubric
  judge; judge failure is evaluator_failed, not a zero score)
- citation_correct
- evaluator input/output tokens and cost (`e.rag.EvalJudgeCost` explicit
  rates) — tracked separately from query cost
- operational copies: total/retrieval/generation latency, tokens, cost

### eval_regression_policies (`0011`, RB-19)
- id UUID PK, name + version UNIQUE (seeded `default-v1` in `0015`)
- policy JSONB: per-metric {direction higher|lower, delta
  absolute|relative, threshold ≥ 0}
- quality_gain_definition TEXT (the stored exception wording)

### experiments / experiment_combinations (`0012`, RB-18)
- experiments: name UNIQUE, description, dataset_id+dataset_version,
  rubric_version, scoring_k, requested_matrix JSONB (chunk sizes/overlaps,
  top-k, retrieval mode, prompt version, model profile; rerank reserved to
  RB-25), combination_limit 1–64 (explicit expansion cap), status
  created|running|completed|partial|failed|dispatch_failed
- experiment_combinations: deterministic settings JSONB (persisted before
  execution), rag_config_id (created immutably per combination; retried
  identities reused by deterministic name), index_dispatched/index_ready/
  index_error (a failed reindex never evaluates against the wrong corpus),
  eval_run_id, eval_run_error, cached eval_run_status

### rag_traces
Migration `0007_rag_traces.sql` creates both tables and indexes their
creation/trace lookup paths.
One durable row is written for every chat request after configuration and
capability validation. A trace write and all spans commit in one transaction;
the API never returns a successful answer if this transaction fails.

- `id` UUID PK; internal foreign-key target for spans
- `trace_id` unique public trace identifier
- `request_type`: currently `chat`
- `question`: normalized question text
- `rag_config_id` FK → `rag_configs`; the configuration is immutable, so this
  is the exact effective configuration identity without copying credentials
- `success`; failed rows require `error_code`, successful rows cannot have one
- `error_message` nullable; provider bodies and credentials are never stored
- `total_latency_ms` ≥ 0
- `input_tokens`, `output_tokens`, `embedding_input_tokens`: provider-reported
  usage, NULL when not reported
- `estimated_cost`: native-cost total, NULL when usage or pricing is
  unavailable; never a fabricated zero
- `cost_currency`, `pricing_version`, and `cost_components` JSONB: explicit
  pricing identity plus `embedding_query`, `generation_input`, and
  `generation_output` breakdown
- `answer` and `citations`: NULL when no answer is safely returned; valid
  successful answers contain only citations mapped to the stored context
- `prompt_snapshot` JSONB: prompt version/identifier and exact rendered text
- `context_snapshot` JSONB: ranked evidence actually sent to generation,
  including chunk, document, revision identity and content
- `created_at`

Source bytes, chunks, revisions, and immutable configurations remain retained
after tombstoning/reprocess so the snapshots and evidence identities remain
inspectable. No API trace field contains API keys.

### rag_spans
Child rows are inserted atomically with `rag_traces`; only stages actually
executed are present. Current `span_name` values are `request`,
`query_embedding`, `retrieval`, `prompt_build`, `llm_generation`, and
`citation_mapping`. Each row has:

- `id` UUID PK
- `trace_id` FK → `rag_traces(id)` `ON DELETE CASCADE`
- `span_name`
- `started_at`
- `duration_ms` ≥ 0
- `metadata` JSONB with stage identity, counts, provider-reported usage, and
  classified diagnostic details where safe

Reranking is not yet an executable Sprint 3 capability; a configuration that
requests it is rejected rather than silently omitting a rerank span.
