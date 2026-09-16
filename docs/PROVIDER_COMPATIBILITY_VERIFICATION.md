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
  OpenCode-compatible client. At the time of that run OpenCode returned HTTP
  429; the API returned `model_rate_limited` with trace
  `25a542c2-9c91-4bc6-8c75-5c57d6a831bc`. The persisted trace contains request,
  query_embedding, retrieval, prompt_build, and llm_generation spans.
  Embedding usage/cost is correctly null.

## OpenCode 429 re-verification (2026-09-16)

- A fresh synthetic request to the configured OpenCode Go endpoint returned
  HTTP 200 (no `Retry-After`) for `glm-5.3-flash`; the compatible endpoint,
  bearer authentication, user-agent, and `x-opencode-session` routing were all
  accepted.
- The response had one completed choice, `finish_reason=stop`, and reported
  prompt/completion usage. A controlled synthetic prompt also produced the
  expected citation marker.
- Therefore the earlier HTTP 429 is no longer reproducible and should not be
  treated as the current provider blocker. A grounded application chat still
  needs an authorized run that permits retrieved database chunks to be sent to
  the provider; no such payload was transmitted during this re-check.

## Criteria status

- RB-07: all acceptance criteria and verification checks pass.
- RB-08: provider-backed lifecycle passes; browser walkthrough and successful
  trace-history inspection remain open.
- RB-09: all acceptance criteria and verification checks pass.
- RB-10: OpenAI-compatible contract and live synthetic generation pass; a
  grounded answer/citations smoke remains to be authorized.
- RB-11: cost/persistence/error criteria and the prior classified 429 trace
  pass; a current successful application trace and post-delete walkthrough
  remain open.
- RB-12: component criteria pass and the live provider 429 blocker is cleared;
  successful browser walkthrough remains open because no browser surface was
  available.

## Grounded chat and lifecycle close-out (2026-09-16)

- Grounded chat against the published database indices returned HTTP 200 with
  the answer `A reviewer must approve a document before publication [2][4].`
  and two citations mapped to retrieved chunks (trace
  `5323b9d0-79cd-4eb8-8a19-612439eb4c95`, latency 2621 ms, 202/24 reported
  tokens).
- The trace persisted in PostgreSQL with spans for request, query_embedding,
  retrieval, prompt_build, llm_generation, and citation_mapping.
- Reprocessing the cited PDF and tombstone-deleting the cited DOCX left the
  historical trace fully inspectable via `GET /api/v1/traces/{id}`.
- This closes the outstanding verification for RB-10 and RB-11 (RB-12's
  browser walkthrough remains open, no browser surface available).
