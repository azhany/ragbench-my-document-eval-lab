# Provider compatibility verification — RB-07 through RB-12

Verified on 2026-09-16. Credential values and provider response bodies were
never printed, logged, or persisted.

## Root cause confirmed

The shipped implementation was strictly OpenAI despite provider interfaces:

- Go hard-coded `https://api.openai.com/v1` for query embedding and generation.
- Airflow hard-coded OpenAI's embedding URL, request, and response schema.
- only OpenAI profiles were registered.
- PostgreSQL pinned every vector to 1536 dimensions.

The configured generic credentials were therefore sent to the wrong service,
which explains the prior OpenAI HTTP 401 result.

## Implemented compatibility

- OpenAI remains supported through its v1 embeddings/Chat Completions wire contract.
- OpenAI-compatible generation is routed by persisted provider identity; the
  initial profile is OpenCode Go `glm-5.3-flash` at a configurable base URL.
- OpenCode requests include an identifying user-agent and per-request
  `x-opencode-session` value.
- Hugging Face uses the native feature-extraction contract (`inputs` request,
  bare vector-array response), with `BAAI/bge-small-en-v1.5` at 384 dimensions.
- Migration 0008 changes the storage column to dimension-flexible `vector` and
  adds explicit 1536d and 384d HNSW expression indexes. Publication and
  retrieval still require exact provider/model/dimension identity.
- Native Hugging Face responses have no token usage, so trace cost is
  `usage_unavailable`, never a false zero.

## Automated verification

- `go test -race -count=1 ./...` against a fresh schema-v8 PostgreSQL database:
  all packages passed, including 384d pgvector retrieval and both HTTP adapters.
- Airflow image suite: 12 tests passed, including native Hugging Face shape,
  dimensions, partial failure, atomic publish, retry identity, FTS, replacement,
  profile isolation, and delete-during-embedding.
- Frontend: 27 tests passed; production build passed.
- Migration 0008 applied both to a fresh database and the live application
  database; `/readyz` reports database/schema ready.

## Live provider and lifecycle evidence

- Minimal native Hugging Face probe: one vector returned with 384 dimensions.
- Full HTTP → Airflow smoke config:
  `32f8f25a-4015-4fd5-8acb-b874ca3ca337`.
- TXT `3adc756a-c308-4b5a-b6a2-67d06a014bc1`, DOCX
  `df580954-1b25-4baa-bc21-5278f2cb5021`, and PDF
  `33ddb67a-8711-430a-b9f6-f7b81e71924e` each published two chunks.
- SQL found eight ready Hugging Face vectors, all exactly 384d, and eight FTS rows.
- Reprocess published a replacement before switching the active pointer. A
  subsequent delete left the replacement job `cancelled/document_deleted`.
- Corrupt PDF `6074160e-56b8-4967-a7c2-9ac5646148a7` visibly failed as
  `corrupt_document`.
- An isolated synthetic-only Go chat run performed a real Hugging Face query
  embedding, retrieved two ranked chunks, built the prompt, and called the
  OpenCode-compatible client. OpenCode returned HTTP 429; the API returned
  `model_rate_limited` with trace `25a542c2-9c91-4bc6-8c75-5c57d6a831bc`.
  The persisted trace contains request, query_embedding, retrieval,
  prompt_build, and llm_generation spans. Embedding usage/cost is correctly null.

## Criteria status

- RB-07: all acceptance criteria and verification checks pass.
- RB-08: provider-backed lifecycle passes; browser walkthrough and successful
  trace-history inspection remain open.
- RB-09: all acceptance criteria and verification checks pass.
- RB-10: controlled criteria pass; successful real answer/citations remains
  blocked by OpenCode Go HTTP 429.
- RB-11: cost/persistence/error criteria pass and a real classified error trace
  is verified; successful answer trace and post-delete trace walkthrough remain open.
- RB-12: component criteria pass; successful provider/browser walkthrough
  remains open. Computer-use inventory reported no browser surfaces.
