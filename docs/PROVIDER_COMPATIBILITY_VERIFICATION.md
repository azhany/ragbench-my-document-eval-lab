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

## OpenCode Zen free-model and Hugging Face fallback checks (2026-09-16)

- The testing-only OpenCode Zen `big-pickle` profile completed one synthetic
  grounded chat (trace `638a3a2d-a93a-4c7f-8cba-ed81721061d5`) and persisted
  `model=big-pickle`; subsequent Big Pickle and `mimo-v2.5-free` requests
  returned classified HTTP 429 rate limits.
- Hugging Face's official OpenAI-compatible router accepted a synthetic
  direct request for `google/gemma-3-4b-it:featherless-ai`, returning one
  completion and token usage. After explicit HF corpus/evidence egress
  authorization, application config `golden-huggingface-gemma-test`
  attempted all 20 cases in run `c932892f-a85d-4bb0-9f26-d9631ca00717` and
  finalized `partial` (3 completed with Recall@K/MRR 1.0 and judge scores
  5/5, 16 query failures, and 1 evaluator failure). A subsequent 20-case run
  `ab4d0402-d6f5-4953-8ce8-4a0ccc8be174` exhausted the account's HF inference
  allowance: eight chat calls and twelve embeddings returned HTTP 402. The
  run is visibly `failed`, with all 20 result rows and traces persisted.
- The official Zen catalog's `ling-3.0-flash-fin-free` also accepted a
  synthetic request when the required `X-OpenCode-Session` header was sent;
  this was a provider-only fallback probe and was not used to claim a
  corpus-backed result after HF embeddings became unavailable.

## Browser verification after explicit HF authorization (2026-09-16)

- The supplied Chromium headless-shell command from
  `/var/folders/6l/hcvknbgs7v5f1trkgl3063l40000gn/T/opencode/rb12-browser`
  reached the live frontend and received the expected provider HTTP 502 once
  HF credits were exhausted.
- A companion walkthrough in the same directory passed 6/6 checks: HF config
  selection, historical successful HF trace opening, retrieved-context
  rendering, visible 502 handling, distinct `provider-failure` UI state, and
  persisted failure-trace link.

## Criteria status

- RB-07: all acceptance criteria and verification checks pass.
- RB-08: provider-backed lifecycle and the Playwright Library/history checks
  pass; browser-triggered mutation coverage remains a follow-up.
- RB-09: all acceptance criteria and verification checks pass.
- RB-10: OpenAI-compatible contract, live synthetic generation, and a grounded
  answer/citations trace pass; the current HF account is now blocked by HTTP
  402 quota exhaustion.
- RB-11: cost/persistence/error criteria and historical grounded traces pass;
  current HF failures remain classified and inspectable rather than hidden.
- RB-12: component criteria pass; the authorized headless browser companion
  covers historical successful-trace inspection and the provider-failure UI.

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
- This closes the outstanding verification for RB-10 and RB-11; the current
  HF quota blocker is recorded separately above and remains visible in the
  failure run.

## RB-12 browser walkthrough close-out (2026-09-16)

The "no browser surface available" blocker was cleared: a real headless
Chromium (Playwright, Chrome Headless Shell 151) drove the live frontend at
`http://localhost:5173` against the live API. 15/15 checks passed:

- Config selection, grounded question submission (HTTP 200).
- Real answer, citation cards (count and snippets matching the API
  response), and selected config displayed.
- Latency, input/output tokens, and `cost-unavailable` /
  `usage_unavailable` state matched the persisted trace exactly.
- "Open trace" navigation rendered the successful chat trace panel with
  the historical config, the full retrieved-context snapshot as sent,
  and all six persisted spans.
- Out-of-library question showed the distinct insufficient-evidence state.

This closes all remaining RB-12 acceptance criteria and verification checks.

## RB-08, RB-20 and RB-22 Playwright re-verification (2026-09-16)

The same live Playwright/Chromium harness was then used for a non-mutating
walkthrough of the current persisted data. All 16 checks passed:

- RB-08 Library loaded processed and failed documents with chunk counts and
  actual errors; revision history and RB-12-style retained trace evidence
  opened successfully.
- RB-20 Experiments loaded the persisted failed matrix, showed no undefined
  combination labels, and the renderer fix is covered by a frontend test that
  exercises persisted run/config/index/policy identities, config differences,
  separate quality/efficiency groups, and no-single-winner messaging.
- RB-22 Monitor accepted the local IANA timezone, displayed filters and
  failure links, opened trace detail, and Overview values matched the metrics
  API.

This clears the browser-environment blocker for RB-08. The walkthrough did not
upload or reprocess new provider-backed documents, advance an experiment, or
create a new evaluation pair; those mutation/provider gates remain explicitly
open in RB-20 and RB-24.
