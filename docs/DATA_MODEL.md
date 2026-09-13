# Data Model

## Core tables

### documents
- id UUID PK
- filename
- mime_type
- storage_path
- status
- checksum
- chunk_count
- created_at
- updated_at

### doc_chunks
- id UUID PK
- document_id FK
- chunk_index
- content
- metadata JSONB
- content_tsv TSVECTOR
- embedding VECTOR
- created_at

### rag_configs
- id UUID PK
- name
- chunk_size
- chunk_overlap
- retrieval_mode
- top_k
- rerank_enabled
- prompt_version
- model_profile
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
