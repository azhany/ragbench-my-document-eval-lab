# Sprint Plan and Implementation Stories

Status: draft backlog, not implemented functionality. Prepared from the current repository documentation.

## 1. Baseline and planning assumptions

The repository currently contains design documents, a Go entry point that prints a banner, and partial Compose configuration. It has no working API, Vue application, SQL migrations, or Airflow DAGs. All stories below start as **Not started**; documentation and directory scaffolding do not count as completed implementation.

Proposed cadence: six two-week iterations. This is a sequencing proposal, not a twelve-week delivery commitment: team capacity, provider access, and actual velocity are unknown. At sprint planning, select a dependency-complete subset that fits capacity; split large stories along their acceptance criteria rather than declaring partial features done.

- **Priority:** P0 = prerequisite/core workflow; P1 = required PoC completeness; P2 = explicitly optional enhancement.
- **Size:** S = one bounded integration; M = several related components; L = cross-service or algorithmic work requiring refinement before commitment. Sizes are relative, not person-day estimates.
- **Owner:** a responsibility, not an assumed additional engineer. One person may own several roles.
- **Delivery target:** six required sprint gates plus the final PRD acceptance demonstration. Optional stories are separately identified and are not silently assumed delivered.

### Source documents

| Source | Planning authority |
|---|---|
| [PRD](../PRD.md) | Product scope, tuning parameters, five success criteria |
| [Architecture](../ARCHITECTURE.md) | Go query pipeline, Airflow batch ownership, PostgreSQL durability |
| [Repository rules](../AGENTS.md) | Small packages, persisted configuration, traceability, feature definition of done |
| [API sketch](API.md) | Existing endpoint names and chat response shape |
| [Data model](DATA_MODEL.md) | Nine planned application tables |
| [Evaluation design](EVALUATION.md) | 20–40 golden cases, separate quality/efficiency metrics, example regression policy |
| [Monitoring design](MONITORING.md) | Quality, reliability, performance, cost, failure taxonomy |
| [Test plan](TEST_PLAN.md) | Scoring/cost rules, integration paths, poor-retrieval demonstration |
| [UI design](UI.md) | Six navigation destinations and evidence-to-trace interaction |
| [DAG backlog](../airflow/dags/README.md) | Ingestion, reindex, evaluation, parameter-sweep DAGs |

### Architecture constraints

- PostgreSQL is authoritative for application state. Go owns synchronous APIs and the shared query pipeline. Airflow orchestrates ingestion, reindexing, evaluation, and sweeps; Python must not become another web backend.
- Use small provider interfaces, one working embedding provider and one working generation provider initially. Do not introduce a provider framework.
- Persist the exact configuration, prompt/model identity, evidence, and scoring policy used by each run. A mutable config name or an unversioned prompt is insufficient.
- Introduce numbered SQL migrations incrementally. Prefer shell entry points over Makefiles.
- Start with internal traces, structured logs, and database-backed summaries. No separate telemetry platform is necessary for this PoC.
- Keep Vue state local unless it is genuinely shared; Pinia is not a prerequisite.
- No multi-tenancy, elaborate authentication, Kubernetes, complex agents, or broad metric catalog. Real-provider execution must be deliberate; normal automated tests must not incur provider charges.

## 2. Decisions to close before dependent stories

These are **proposals or unresolved contract details**, not requirements already established by the source documents. Record adopted decisions in the existing API/data-model/evaluation documents during implementation.

| Decision | Proposed implementation boundary | Due / owner |
|---|---|---|
| Provider and embeddings | Select one accessible embedding/generation provider; pin embedding model, dimensions, model profile, and tokenizer/chunk unit. Store identifiers, not credentials, with runs. Unknown profiles fail explicitly. | Sprint 1 / backend + pipeline |
| Document storage and extraction | Local shared Docker volume for uploaded bytes, PostgreSQL for metadata. Text-bearing PDF, DOCX, TXT are supported. Image-only/encrypted/unreadable files produce a visible extraction failure; OCR is not currently specified. Document accepted encodings and upload-size limit. | Sprint 1 / backend + pipeline |
| Index reproducibility | Introduce an index revision associated with document checksum, chunk settings, embedding profile, and chunk IDs. Query/evaluation pins a compatible ready revision. Chunk-size changes create a revision, not an in-place overwrite of evidence used by old runs. | Sprint 1 / backend + database |
| Historical evidence and deletion | Remove deleted documents from future retrieval and remove source bytes. Retain the minimal evidence snapshots needed by existing traces/evaluations; explain this retention behavior in the UI/API. Reprocessing must not break historical citations. | Sprint 2 / backend |
| Work dispatch and state | Go triggers Airflow through its supported API. Persist job/run IDs and states; failed dispatch cannot look like successful processing. Define allowed transitions and retry/idempotency behavior, without adding a separate message broker. | Sprint 1 / backend + pipeline |
| Missing API contracts | Add health/readiness, config and dataset management, experiment dispatch, and retrieved-context contracts. Proposed resource roots: `/api/v1/rag-configs`, `/api/v1/eval-datasets`, `/api/v1/experiments`. Existing routes in API.md retain their names. | Owning sprint / backend |
| Evaluation semantics | Pin dataset version, corpus/index revisions, relevance granularity, scoring K, evaluator version, rubric, and aggregation rules. Compare chunk-size variants with stable document/source evidence or explicit relevance remapping, never stale chunk IDs. | Sprint 4 / evaluation |
| Judge implementation | Proposed: versioned model-based rubrics for relevance and groundedness, plus evidence/citation scoring. Persist judge profile, prompt, scores, rationale, and failures. Judge errors are not zero quality scores. | Sprint 4 / evaluation |
| Regression semantics | Persist metric direction, absolute vs relative delta, threshold, and the definition of “quality gain.” The 5%/3%/25%/30% examples are candidate policy values, not hidden defaults. Specify equality and zero-baseline behavior. | Sprint 5 / evaluation |
| Cost currency and scope | Persist pricing version, native currency, and component usage. Separate query embedding/generation, ingestion, and evaluator costs. If displaying RM from another currency, persist conversion rate/date; otherwise label native currency rather than assuming RM. | Sprint 3 / backend |

**Schema gaps to resolve through migrations:** index revisions and historical evidence; prompt/model/config snapshots; ingestion/job error details; evaluator and regression-policy snapshots; experiment-to-run links; currency/pricing metadata. These are missing from the sketch and are necessary to make the documented behavior reproducible, not invitations to redesign the application.

## 3. Sprint roadmap

| Sprint | Goal | Required stories | Exit demonstration |
|---|---|---|---|
| 1 — Runnable foundation | Boot the real stack and persist a valid RAG configuration | RB-01–RB-04 | Compose starts API, Vue, PostgreSQL/pgvector, and operational Airflow; config survives restart |
| 2 — Document library | Upload, process, inspect, reprocess, and delete supported documents | RB-05–RB-08 | PDF/DOCX/TXT reach processed status with chunks; an extraction failure is visible in Library |
| 3 — Traceable chat | Ask a grounded question with real retrieval and model generation | RB-09–RB-12 | Answer links to stored evidence, timing, usage, and cost; provider failure has a classified trace |
| 4 — Repeatable evaluation | Execute a versioned golden dataset through the same query pipeline | RB-13–RB-16 | A 20–40-case run stores per-case scores, aggregate metrics, failures, and trace links |
| 5 — Measurable experiments | Compare configurations and detect explicit regressions | RB-17–RB-20 | Two compatible runs show metric deltas and reasons for regression; a small parameter sweep completes |
| 6 — Observable PoC | Connect monitoring, scheduled checks, and the complete demonstration | RB-21–RB-24 | Fresh local startup supports all five PRD success criteria and the poor-retrieval failure-mode demo |

Main dependency chain: foundation → indexed library → traced chat → scored evaluations → comparison → integrated demonstration. Monitoring signals are added in each story, not postponed until Sprint 6. Sprint 6 aggregates and exposes them.

Within a sprint, independent work can proceed after shared contracts are fixed: extraction and Library UI after the upload contract; scoring and evaluation UI after result schemas; comparison and sweep orchestration after configuration/run identity. Shared schema/API changes need one integration owner.

## 4. Detailed stories

Every story inherits the definition of done in section 5. “Verification” below describes future implementation checks, not tests that currently exist or have already passed.

### Sprint 1 — Runnable foundation

#### RB-01 — Boot the local application stack

**P0 · L · Owner:** platform/backend · **Depends on:** none

**Story:** As a developer, I can start the required services locally so implementation can be exercised against the intended architecture.

**Tasks / surfaces:** Replace the banner in `backend/cmd/api/main.go` with an HTTP lifecycle; add backend/frontend Dockerfiles and minimal Vue/Vite entry point; complete `docker-compose.yml` with Airflow initialization, required runtime components, DAG/shared-file mounts, and service readiness; retain `scripts/dev.sh` as the shell launcher. Configure separate Airflow metadata and application database ownership.

**Acceptance criteria:**
- `sh scripts/dev.sh` builds and starts a reachable API and Vue page, PostgreSQL with pgvector, and Airflow capable of loading and executing DAGs; an idle Airflow image alone is not completion.
- Proposed `/healthz` reports process health; `/readyz` reports database/schema readiness and returns a non-success status when unavailable.
- Required environment settings and local credentials are documented; provider keys are not committed. Startup errors identify the failed service/dependency.

**Verification:** Clean disposable Compose environment; HTTP health checks and browser page load; execute a non-provider Airflow connectivity task; remove that temporary verification task afterward. Restart services and confirm shutdown/startup behaves predictably.

#### RB-02 — Establish durable schema and migrations

**P0 · M · Owner:** backend/database · **Depends on:** RB-01

**Story:** As a developer, I can apply versioned schema changes so application state is durable and valid.

**Tasks / surfaces:** Add numbered migrations under `db/migrations/` and a shell migration entry point. Create `documents`, `doc_chunks`, and the index-revision relationship first; subsequent owning stories add the remaining tables from `DATA_MODEL.md`. Enable vector and required UUID support; define foreign keys, uniqueness, status constraints, and retrieval indexes appropriate to the selected dimensions.

**Acceptance criteria:**
- A fresh database migrates successfully; rerunning the migration runner does not reapply completed migrations.
- Invalid document/chunk relationships are rejected. Index revisions identify chunk settings and embedding compatibility.
- Migration failure is visible and prevents ready status; application startup does not silently create an alternative schema.

**Verification:** Apply migrations twice to a disposable database; exercise valid/invalid inserts and a failed migration in an isolated fixture; confirm stored records survive service restart.

#### RB-03 — Persist immutable RAG configurations

**P0 · M · Owner:** backend · **Depends on:** RB-02

**Story:** As an evaluator, I can save a named configuration so runs can reproduce the settings I selected.

**Tasks / surfaces:** Migrate `rag_configs`; implement proposed create/list/detail config APIs in Go; persist chunk size/overlap, top-k, vector/hybrid mode, rerank flag, prompt version, model profile, and embedding identity. Define the small provider interfaces and immutable prompt/model references consumed later.

**Acceptance criteria:**
- Valid configurations survive restart; changing settings creates a new immutable identity rather than changing past runs.
- Reject invalid chunk bounds, overlap not smaller than size, invalid top-k, and unknown profiles/versions with structured errors.
- Unsupported capabilities are explicit. Until RB-17/RB-25 lands, hybrid/rerank-enabled execution is rejected as unavailable, never silently treated as vector/no-rerank.

**Verification:** API persistence checks; boundary tests for config validation and a historical-config immutability test. Publish request/response/error contracts in `API.md`.

#### RB-04 — Connect the Vue application shell

**P0 · S · Owner:** frontend · **Depends on:** RB-01, RB-03

**Story:** As a user, I can open the application and see whether its backend is reachable before beginning a workflow.

**Tasks / surfaces:** Create Vue navigation for Overview, Library, Chat, Evaluations, Experiments, Monitor; use one API client and a working config list. Unimplemented destinations remain explicitly unavailable until their stories land, without fake metrics or sample success states.

**Acceptance criteria:**
- Navigation and config data use the running backend; loading, empty, and unavailable states are distinct.
- A backend failure gives an actionable error instead of a blank page. No model secrets reach browser configuration.
- State remains component-local except for proven shared needs.

**Verification:** Browser-drive navigation, retrieve a persisted configuration, and stop the API to observe the visible failure state.

### Sprint 2 — Document library

#### RB-05 — Upload and dispatch documents

**P0 · M · Owner:** backend · **Depends on:** RB-02, RB-03

**Story:** As a developer/evaluator, I can upload a document and receive its queued identity so processing can begin asynchronously.

**Tasks / surfaces:** Implement `POST /api/v1/documents`, list/detail routes, durable metadata and shared-volume storage; validate allowed files and configured size bounds; associate ingestion config and trigger `document_ingestion` in Airflow. Persist dispatch correlation and errors.

**Acceptance criteria:**
- Accepted PDF/DOCX/TXT uploads return the persisted document with queued status; Go does not perform the batch pipeline synchronously.
- Unsupported/oversized uploads and storage/dispatch failures return documented errors; partial uploads do not appear processed or leave unexplained records/files.
- File names cannot escape the upload directory. Duplicate/retried dispatch cannot create duplicate active ingestion for the same document revision.

**Verification:** HTTP multipart upload → queued database row and Airflow run; rejected format/size/path-name cases; unavailable Airflow produces a visible dispatch failure and a documented recovery path.

#### RB-06 — Extract, normalize, and chunk source content

**P0 · L · Owner:** pipeline · **Depends on:** RB-05

**Story:** As a reviewer, I can rely on processed chunks retaining source identity so answers can cite the original evidence.

**Tasks / surfaces:** Implement extraction/normalization/chunking stages in `airflow/dags/` with reusable Python modules; support text-bearing PDF, DOCX, TXT; retain page/section or available location metadata and deterministic chunk ordering for each revision.

**Acceptance criteria:**
- All three formats produce nonempty ordered chunks using persisted size/overlap settings and a documented unit.
- Corrupt, encrypted, image-only, or empty extracted content ends in a visible classified failure, not a processed document with zero useful content.
- Retrying a task does not duplicate chunks; a changed chunk configuration cannot overwrite historical evidence identity.

**Verification:** Small representative fixtures for all three formats; exact chunk boundary/overlap tests; corrupt/empty extraction tests; retry the same task and confirm stable revision/chunk identity.

#### RB-07 — Embed and publish searchable revisions

**P0 · L · Owner:** pipeline/database · **Depends on:** RB-06

**Story:** As a chat user, I can query only fully indexed documents so partial ingestion does not produce misleading evidence.

**Tasks / surfaces:** Implement the selected real embedding integration; insert vectors and full-text metadata in `doc_chunks`; publish ready revision and document chunk count only after successful indexing. Persist provider/model identity, job timings, and classified failure details.

**Acceptance criteria:**
- A real-provider smoke run stores vectors with the configured dimensions and FTS content; the document transitions to processed with the actual chunk count.
- Partial embedding failure does not publish an incomplete revision. Provider errors are recorded as `embedding_failed` with useful stage context.
- Retry completes without duplicate chunks; revisions from incompatible embedding profiles cannot be mixed in one retrieval query.

**Verification:** Small authorized real embedding run, database vector/FTS query, and dimension/partial-failure/idempotency integration tests using controlled provider doubles for deterministic failures.

#### RB-08 — Operate the Library lifecycle

**P1 · L · Owner:** backend/frontend/pipeline · **Depends on:** RB-04, RB-07

**Story:** As a developer/evaluator, I can inspect, reprocess, and delete documents from Library so the indexed corpus stays manageable.

**Tasks / surfaces:** Implement Library upload/table/status/chunk-count/error views; `POST /api/v1/documents/{id}/reprocess`, `DELETE /api/v1/documents/{id}`; implement `document_reindex` using shared ingestion stages. Define active revision switching and retention semantics.

**Acceptance criteria:**
- UI shows queued/processing/processed/failed states, chunk counts, and the actual error; reprocess creates and activates a new ready revision only after success.
- Failed reprocessing preserves the previous usable revision and reports the new failure. Delete removes the document from new retrieval and handles in-flight tasks without resurrection.
- Historical trace evidence remains readable under the documented retention policy; invalid IDs and repeated actions have documented behavior.

**Verification:** Browser upload of all supported formats, failed extraction, reprocess, and delete; integration test delete during processing and failed replacement revision. Inspect retained history once RB-11 exists.

### Sprint 3 — Traceable chat

#### RB-09 — Retrieve ranked vector evidence in Go

**P0 · M · Owner:** backend · **Depends on:** RB-03, RB-07

**Story:** As a reviewer, I can ask across processed documents and retrieve the most relevant available evidence.

**Tasks / surfaces:** Implement query validation, query embedding, pgvector retrieval, top-k truncation, and evidence identity in the Go query pipeline. Restrict retrieval to selected compatible ready revisions and record stage timing.

**Acceptance criteria:**
- Results are ordered and capped by top-k, with chunk/document/revision identity and source content; deleted or unpublished revisions are excluded.
- Empty retrieval returns the documented insufficient-evidence outcome (`retrieval_empty`) without inventing an answer or silently selecting unrelated context.
- Unknown config, unavailable revision, and embedding failure are distinct structured errors.

**Verification:** Known-vector ranking, ties, top-k, no-result and revision-filter tests against pgvector; authorized query-embedding/retrieval smoke run on the Sprint 2 corpus.

#### RB-10 — Generate grounded answers and citations

**P0 · L · Owner:** backend · **Depends on:** RB-09

**Story:** As a reviewer, I receive an answer supported by retrieved chunks so I can check its evidence.

**Tasks / surfaces:** Implement `POST /api/v1/chat`; versioned prompt construction, context budgeting, selected real generation provider, response parsing, and citation mapping. Keep the pipeline callable by normal chat and evaluation.

**Acceptance criteria:**
- A real model answer returns the API sketch's answer/citations shape, with citations mapped only to retrieved evidence.
- Prompt version, model profile, and context actually sent are recoverable. Document text is treated as evidence, not higher-priority instructions.
- Missing/invalid citations and malformed responses are visible failures; timeout and rate-limit errors use the documented taxonomy. No fallback fabricated answer is returned.

**Verification:** Authorized grounded-question smoke run; controlled tests for outside-context citation IDs, `citation_missing`, `malformed_response`, `model_timeout`, and `model_rate_limited`.

#### RB-11 — Persist traces, token usage, and cost

**P0 · M · Owner:** backend · **Depends on:** RB-10

**Story:** As a reviewer, I can explain an answer's evidence, latency, and estimated cost, including when a request fails.

**Tasks / surfaces:** Migrate `rag_traces` and `rag_spans`; record request, query_embedding, retrieval, prompt_build, llm_generation, citation_mapping, and rerank when enabled. Implement trace list/detail APIs; snapshot effective config, ranked evidence, prompt/model/pricing identity, reported usage, and cost components.

**Acceptance criteria:**
- Chat returns its persisted trace ID with latency/tokens/estimated cost; errors also have correlated traces and structured logs where storage is available.
- Estimated cost uses explicit input/output rates and currency; unknown pricing or missing provider usage is unavailable, not falsely zero. Query totals include the documented embedding/generation components.
- A trace write failure cannot be reported as a fully traceable success; the client sees a structured persistence error and logs retain correlation.

**Verification:** Chat → citations + trace + spans in PostgreSQL; exact token-cost calculations, unknown-profile/missing-usage cases, classified provider error traces, and database failure behavior. Reprocess/delete source and verify historical evidence remains inspectable.

#### RB-12 — Deliver the evidence-connected Chat screen

**P1 · M · Owner:** frontend · **Depends on:** RB-04, RB-11

**Story:** As a reviewer, I can move from question to evidence to answer to metrics to trace without manually inspecting API payloads.

**Tasks / surfaces:** Build question composer/config selection, answer display, citation cards, optional retrieved-context drawer, and latency/token/cost summary with trace navigation. Document how the drawer gets context from the trace/detail contract.

**Acceptance criteria:**
- A submitted question displays the real answer, source snippets, selected config, and persisted trace metadata.
- Empty retrieval, loading, provider failure, and unavailable cost have distinct UI states. Citation text is rendered safely.
- Reopening a trace shows the evidence/config used then, not whatever document/config is currently active.

**Verification:** Browser-drive successful chat, open citations/context/trace, then exercise insufficient evidence and a controlled provider error; verify displayed values match the returned trace.

### Sprint 4 — Repeatable evaluation

#### RB-13 — Maintain a versioned golden dataset

**P0 · M · Owner:** evaluation/backend · **Depends on:** RB-08, RB-11

**Story:** As an evaluator, I can maintain 20–40 questions with expected evidence and reference answers so comparisons use a stable benchmark.

**Tasks / surfaces:** Migrate `eval_datasets` and `eval_cases`; implement proposed dataset create/import/list/detail and versioned case-edit APIs; prepare a small lawful local corpus and reviewed golden cases. Validate references and pin source/relevance policy.

**Acceptance criteria:**
- A dataset version contains 20–40 reviewed cases for the demonstration, with question, reference answer, and expected document/chunk or mapped source evidence.
- Updating cases creates a new version; old runs retain their original cases. Invalid references and empty/unusable datasets are rejected.
- Different chunk configurations use a documented stable relevance unit or explicit remapping; old chunk UUIDs are not treated as ground truth for a different index revision.

**Verification:** Import and read back the golden dataset; invalid-reference and version-immutability tests; inspect representative source evidence against reference answers.

#### RB-14 — Orchestrate durable evaluation runs

**P0 · L · Owner:** backend/pipeline · **Depends on:** RB-13

**Story:** As an evaluator, I can launch a dataset against a named config so every case uses the normal Go query pipeline.

**Tasks / surfaces:** Migrate `eval_runs`/`eval_results`; implement create/list/detail/results APIs and `rag_evaluation` DAG. Go exposes the same query path with evaluation request attribution; Airflow calls it per case and records run/result/trace relationships.

**Acceptance criteria:**
- Run creation pins dataset, config, corpus/index revision, and evaluator policy; every attempted case has status and a query trace or explicit pre-query failure.
- Retries do not duplicate completed results. A case/provider failure remains visible; run status and aggregation distinguish completed, partial, and failed work.
- Airflow dispatch failure is surfaced; Python does not duplicate retrieval, prompt construction, or generation logic.

**Verification:** Small dataset DAG smoke run through real Go API; one injected failed case and one retried task; verify run status, unique results, trace links, and immutable inputs in PostgreSQL.

#### RB-15 — Score retrieval and generation separately

**P0 · L · Owner:** evaluation · **Depends on:** RB-14

**Story:** As an evaluator, I can distinguish retrieval quality, answer quality, and efficiency instead of receiving an unexplained combined score.

**Tasks / surfaces:** Implement Recall@K and MRR, versioned answer-relevance and groundedness rubrics, citation correctness/evidence match, and operational aggregates in importable evaluation modules. Persist scoring configuration, judge outputs/errors, and evaluation cost separately from query cost.

**Acceptance criteria:**
- Recall@K is relevant evidence retrieved within K divided by total expected evidence at the configured relevance unit; MRR uses the first relevant rank. Deduplication, missing relevance, and K semantics are documented.
- Each result stores separate retrieval, generation, citation, timing, token, and cost metrics. Aggregates include denominators, failure counts, and missing-score counts; undefined values do not become zero or perfect scores.
- Judge failure is `evaluator_failed`, distinct from low quality. p50/p95 method and successful/failed request populations are explicit. Judge model, rubric, and policy are versioned.

**Verification:** Hand-calculated Recall@K/MRR cases, duplicates/empty/missing judgments, percentile/aggregate fixtures, and controlled judge failures; a small real scoring run produces inspectable rationale and separate evaluator cost.

#### RB-16 — Run and inspect evaluations in the UI

**P1 · M · Owner:** frontend · **Depends on:** RB-12, RB-15

**Story:** As an evaluator, I can select a dataset/config, launch a run, and inspect individual failures so evaluation is usable without database access.

**Tasks / surfaces:** Build Evaluations dataset/config selectors, run action/status, aggregate cards, per-question answer/evidence/score views, and trace links. Add dataset import/versioned-edit controls against RB-13 contracts.

**Acceptance criteria:**
- A versioned dataset can be maintained and selected; a launched run reaches an accurate terminal or partial state without fake progress.
- Aggregate cards keep quality, efficiency, and failure counts separate; each case leads to its evidence and trace.
- Invalid dataset, failed launch, missing score, and failed case states remain visible after reload.

**Verification:** Browser import/edit dataset, launch the 20–40-case golden run, reload during execution, and inspect one successful and one controlled failed/scoring-error case.

### Sprint 5 — Measurable experiments

#### RB-17 — Add configurable hybrid retrieval

**P1 · M · Owner:** backend · **Depends on:** RB-09, RB-15

**Story:** As an evaluator, I can compare vector and hybrid retrieval using the same query pipeline.

**Tasks / surfaces:** Add PostgreSQL FTS candidate retrieval, deterministic merge/deduplication and ranking; persist fusion method, weights or rank constants, and candidate limits as effective configuration. Keep the existing vector mode unchanged.

**Acceptance criteria:**
- Vector/hybrid selection changes actual execution; returned results include final ranking and usable provenance.
- Top-k applies after merge; duplicate chunks appear once; tie ordering and empty branch behavior are deterministic.
- Fusion settings and candidate limits are recorded with traces/runs so an experiment can be reproduced.

**Verification:** Hybrid ordering, ties, deduplication, top-k, and empty-branch unit tests from `TEST_PLAN.md`; run the same golden subset in vector and hybrid modes and inspect actual retrieved evidence.

#### RB-18 — Execute bounded parameter experiments

**P1 · L · Owner:** pipeline/backend · **Depends on:** RB-08, RB-14, RB-17

**Story:** As an evaluator, I can run a small configuration matrix so chunking and query changes are measured systematically.

**Tasks / surfaces:** Implement `rag_parameter_sweep`, proposed experiment create/list/detail APIs, and persisted experiment-to-config/run links. Resolve required index revisions through `document_reindex`, then launch evaluations. Support chunk size/overlap, top-k, vector/hybrid, prompt version, and model profile; enable rerank dimension only after RB-25.

**Acceptance criteria:**
- Persist the expanded configuration matrix before execution with an explicit configured combination limit; a two-configuration experiment produces separate traceable runs.
- Chunk/embedding changes build compatible revisions before evaluation; query-only changes reuse compatible ready revisions. A failed reindex prevents that config from being evaluated against the wrong corpus.
- Invalid/unsupported combinations are rejected visibly, and partial matrix failures retain successful run links. Retries do not create duplicate logical experiments/runs.

**Verification:** Two-config sweep including a chunk-size change; inspect different index identities and the same dataset/source corpus; controlled reindex failure and task retry; no hidden fallback to the old index.

#### RB-19 — Compare runs with persisted regression policy

**P0 · L · Owner:** backend/evaluation · **Depends on:** RB-15, RB-17

**Story:** As an evaluator, I can see whether a candidate regressed against a baseline and which policy rule caused the result.

**Tasks / surfaces:** Implement `GET /api/v1/eval-runs/{id}/compare/{baselineId}` and policy persistence. Return baseline/candidate values, absolute/relative deltas, metric direction, threshold, evaluability, and pass/regression reasons without collapsing dimensions into one score.

**Acceptance criteria:**
- Compare only compatible dataset versions, source corpus, relevance unit/scoring K, and evaluator policies; differing index settings are allowed when evidence mapping supports a valid comparison.
- Thresholds are explicit persisted inputs. Test strictly greater-than vs equality, zero baseline, missing metrics, incomplete runs, and the defined quality-gain exception for latency/cost.
- A non-comparable or incomplete pair cannot be labelled a clean pass. A quality regression and a pipeline/evaluator failure remain distinct.

**Verification:** Hand-calculated comparison fixtures exercising every adopted threshold and boundary; API comparison of two actual runs; confirm response explains exactly why each rule passed, failed, or could not be evaluated.

#### RB-20 — Show configuration differences and comparison results

**P1 · M · Owner:** frontend · **Depends on:** RB-16, RB-18, RB-19

**Story:** As an evaluator, I can see which configuration is better and by how much, including its cost/latency tradeoffs.

**Tasks / surfaces:** Implement Experiments config creation/detail, bounded matrix launch/status, baseline selection, config diff, side-by-side metrics/deltas, regression reasons, and per-case drill-down from Evaluations.

**Acceptance criteria:**
- UI exposes all implemented PRD tuning fields and identifies unavailable optional reranking instead of implying it ran.
- Baseline and candidate dataset/config/index/policy identities are visible; quality and efficiency remain separate, including cases with no single overall winner.
- Incompatible comparisons, missing metrics, and partial experiments display their reason; delta units and metric direction are unambiguous.

**Verification:** Browser-create two configs, launch a small experiment, compare runs, inspect a case trace, and attempt an incompatible comparison; check displayed deltas against API results.

### Sprint 6 — Observable PoC

#### RB-21 — Aggregate operational and quality metrics

**P1 · M · Owner:** backend · **Depends on:** RB-11, RB-15, RB-19

**Story:** As a reviewer, I can distinguish provider/pipeline reliability problems from quality regressions over time.

**Tasks / surfaces:** Implement `GET /api/v1/metrics/summary`; aggregate query success/error, ingestion and evaluation failures, provider errors, p50/p95 total latency, retrieval/generation latency, tokens, cost/query, daily experiment cost, Recall@K/MRR/faithfulness/relevance trends, and regression count.

**Acceptance criteria:**
- Filters/time window/timezone and denominators are documented; query vs evaluation traffic and query vs ingestion/judge spending are identifiable.
- Trends do not silently mix incompatible dataset/evaluator versions or currencies. Empty windows and unknown usage/cost display unavailable data, not manufactured zero quality/cost.
- All failure codes listed in `MONITORING.md` can be surfaced from their source records; failure summaries link to trace/run/document detail.

**Verification:** Controlled persisted records yield known counts, percentiles, totals, and trends; check empty windows, failed cases, mixed policies/currencies, and summary API against those expected values.

#### RB-22 — Deliver Overview and Monitor surfaces

**P1 · M · Owner:** frontend · **Depends on:** RB-20, RB-21

**Story:** As a reviewer, I can inspect system health and navigate to the evidence behind a metric or failure.

**Tasks / surfaces:** Build Overview summary and Monitor success/error, latency, token/cost and quality trends, recent traces/failures, trace span view, and links back to Library/Evaluations. Reuse existing summary/detail APIs rather than add a dashboard backend.

**Acceptance criteria:**
- All six documented navigation destinations now offer real workflows, not unavailable placeholders.
- Filters and metric units are visible; selecting a failure reaches its actual trace, evaluation result, or ingestion status.
- Loading, no-data, missing metric, and API failure states remain distinguishable; displayed values are database-backed.

**Verification:** Browser-drive each destination, apply a time filter, open a failed ingestion/query/evaluation, and inspect a trace's evidence and spans; compare selected cards with API values.

#### RB-23 — Schedule repeatable regression checks

**P1 · M · Owner:** pipeline/evaluation · **Depends on:** RB-18, RB-19, RB-21

**Story:** As an evaluator, I can schedule a benchmark against an explicit baseline so regressions are detected without manually rebuilding run settings.

**Tasks / surfaces:** Add an opt-in Airflow schedule using the existing evaluation/comparison workflow, not a second evaluator. Persist dataset/config/corpus/baseline/policy identities and execution status; document local enable/disable behavior.

**Acceptance criteria:**
- A scheduled execution uses the pinned benchmark and policy and records pass/regression/not-evaluable with reasons visible through existing APIs/UI.
- Missing baseline, unavailable corpus, or evaluator failure produces an explicit job failure/not-evaluable state, not a pass.
- Local schedules are disabled by default to avoid accidental provider spending; no external alerting platform is required.

**Verification:** Exercise one scheduled interval in an isolated environment with explicit provider authorization; inspect run/comparison records, then disable it. Test missing-baseline behavior with a controlled fixture.

#### RB-24 — Prove the complete PoC and failure-mode story

**P0 · M · Owner:** integration owner · **Depends on:** RB-22, RB-23

**Story:** As a project reviewer, I can start locally and observe all five PRD success criteria, including a diagnosable regression.

**Tasks / surfaces:** Add a reproducible shell-driven smoke/demo procedure and reviewed corpus/dataset fixtures; update README startup instructions, actual API contracts, data model, and test instructions to match landed code. Run the documented integration paths, not just isolated functions.

**Acceptance criteria:**
- From a fresh disposable environment: start Compose; upload/process supported files; chat with citations; run the same versioned dataset on two configs; inspect comparison and Monitor.
- Use an intentionally poor retrieval configuration from `TEST_PLAN.md`; demonstrate observed retrieval degradation and the policy result while traces show a healthy model call. If it does not regress, report that result and select a justified evidence-sensitive fixture/configuration; never fabricate scores or adjust thresholds merely to make the demo pass.
- Every chat/evaluation case exposes evidence, effective config, latency, tokens/cost availability, and failure context. A controlled provider failure produces the classified error trace.
- Existing unit/integration checks and browser/Compose smoke checks pass without modifying verification assets to conceal failures. Credentials and required manual steps are documented.

**Verification:** Execute the section 7 acceptance walkthrough; capture actual command exit statuses, run/config IDs, comparison outputs, and browser observations. No completion claim based solely on a successful build.

### Optional backlog — not required for the six-sprint core gate

#### RB-25 — Enable real reranking as an experiment dimension

**P2 · M · Owner:** backend/evaluation · **Depends on:** RB-17, RB-19

**Story:** As an evaluator, I can measure whether reranking improves evidence quality enough to justify its latency/cost.

**Tasks / surfaces:** Select a real supported reranker behind a small interface; persist reranker profile, candidate limit, and configuration; expose the on/off setting through RB-18/RB-20 once executable.

**Acceptance criteria:** Enabled runs actually rerank retrieved candidates and record a rerank span, usage/cost when applicable, and final order. Disabled runs skip the call. Reranker failures are visible, not silent vector/hybrid fallbacks.

**Verification:** Deterministic ordering/on-off/failure tests plus a small authorized reranked evaluation comparison. If pulled into Sprint 5, reserve capacity explicitly; do not claim rerank support before this story lands.

#### RB-26 — Add graded nDCG@K scoring

**P2 · M · Owner:** evaluation · **Depends on:** RB-15, RB-19

**Story:** As an evaluator, I can assess ranked evidence with graded relevance when binary relevance is insufficient.

**Tasks / surfaces:** Add versioned graded judgments, documented gain/discount/normalization policy, per-case/aggregate nDCG@K, and comparison/UI exposure without replacing Recall@K or MRR.

**Acceptance criteria:** Scores match hand-calculated graded rankings; missing judgments and zero ideal gain have documented handling. Runs using different judgment/scoring policies are not silently compared.

**Verification:** Hand-calculated ideal/reversed/tied/zero-gain fixtures and one real result display. Keep deferred unless the golden dataset actually needs graded relevance.

## 5. Shared definition of ready and done

### Ready for implementation

- Dependencies are complete or the shared API/schema contract is settled before concurrent work.
- Owner has an agreed observable happy path and important failure path.
- Required provider access, supported versions, sample data, limits, and configuration decisions are available.
- L-sized stories are refined into executable subtasks before sprint commitment; acceptance criteria stay intact.

### Done for every feature story

- Happy path runs through the actual affected surface; no mock provider/UI data in the delivered workflow.
- Important failure is visible as a structured API error, failed job/state, or UI error as appropriate.
- Correlated logging and applicable metrics exist; traces preserve evidence/configuration without storing credentials.
- Actual API contract and schema/config changes are documented in the existing documents, with migrations for schema changes.
- At least one behavioral test covers the core rule. Retrieval scoring, comparison policy, and cost calculations receive focused boundary tests.
- Changed-contract tests pass; UI stories are browser-verified; pipeline stories run through Airflow; startup is checked through Compose. Record what was exercised and any external prerequisites.
- No unfinished placeholders, silent unsupported modes, or historical evidence/config corruption remain in the completed story.

## 6. Coverage and scope control

| Documented requirement | Owning stories |
|---|---|
| PDF/DOCX/TXT upload, status, count, errors, delete/reprocess | RB-05–RB-08 |
| Processed-document chat, citations, optional context inspection | RB-09, RB-10, RB-12 |
| Persist config, prompt/model, evidence, spans, tokens, cost | RB-03, RB-07, RB-11, RB-14 |
| Golden dataset maintenance; per-case and aggregate metrics | RB-13–RB-16 |
| Baseline comparison and explicit regression flags | RB-19, RB-20, RB-23 |
| Chunk size, overlap, top-k, vector/hybrid, prompt, model profile | RB-03, RB-08, RB-17, RB-18, RB-20 |
| Optional reranking and optional nDCG@K | RB-25 and RB-26; explicitly deferred unless selected |
| Ingestion, reindex, evaluation, parameter-sweep DAGs | RB-05–RB-08, RB-14, RB-18 |
| Scheduled regression checks | RB-23 |
| Quality/reliability/performance/cost monitoring | RB-11, RB-15, RB-19, RB-21, RB-22 |
| Overview, Library, Chat, Evaluations, Experiments, Monitor | RB-04, RB-08, RB-12, RB-16, RB-20, RB-22 |
| Retrieval/cost/comparison unit rules and integration paths | RB-05, RB-07, RB-09, RB-11, RB-14, RB-15, RB-17, RB-19, RB-24 |
| Whole-stack startup and poor-retrieval demonstration | RB-01, RB-24 |

**Nine-table ownership:** `documents`/`doc_chunks` → RB-02; `rag_configs` → RB-03; `rag_traces`/`rag_spans` → RB-11; `eval_datasets`/`eval_cases` → RB-13; `eval_runs`/`eval_results` → RB-14. Necessary additional relationships/snapshots belong to the feature that first writes them.

**API sketch ownership:** document routes → RB-05/RB-08; chat → RB-10/RB-11; evaluation create/list/detail/results → RB-14; compare → RB-19; metrics summary → RB-21; trace list/detail → RB-11. Proposed additional routes are identified in section 2, not presented as existing contracts.

## 7. Final acceptance walkthrough

These are future release checks, not current runnable capabilities. Exact fixture paths and test commands must be supplied by the implementing stories; do not pretend nonexistent scripts or tests are available today.

1. **Local startup:** run `sh scripts/dev.sh` in a disposable environment with documented settings. Expect successful image builds, schema initialization, reachable Vue/API, and functioning Airflow. `docker compose config --services` alone is insufficient.
2. **Document lifecycle:** upload text-bearing PDF, DOCX, TXT through Library; observe queued → processing → processed and positive chunk counts. Upload one invalid extraction fixture and inspect its failure. Reprocess and delete; confirm new retrieval and historical trace behavior.
3. **Traceable query:** ask a corpus-backed question through Chat; open a citation, retrieved context, and trace. Expect evidence identity and persisted config/prompt/model plus stage timings and truthful token/cost metadata.
4. **Repeatable evaluation:** select the same reviewed 20–40-case dataset version and source corpus for baseline and candidate configs; run both through Airflow and the Go pipeline. Expect stored per-case results, aggregate metrics, failure counts, and trace links.
5. **Comparison:** select the baseline in UI; inspect config differences and quality/efficiency deltas. Expect explicit stored policy, threshold units, compatibility checks, and explained pass/regression/not-evaluable status.
6. **Poor-retrieval demonstration:** execute the intentional degraded retrieval setup; inspect actual Recall@K/MRR and generation-quality changes, a healthy generation span, and the resulting comparison status. Retain observed evidence; do not substitute staged scores.
7. **Operational failures and monitoring:** exercise a controlled provider failure and inspect its classified trace; inspect ingestion/evaluation failures and Overview/Monitor trends. Expect coherent denominators and links to source records.
8. **Scheduled check and persistence:** execute an explicitly enabled scheduled benchmark, then disable it; restart services and reload documents/configs/runs/traces. Expect durable state and the same comparison/evidence identities.

### PRD success-criterion mapping

| Success criterion | Demonstration steps |
|---|---|
| Same dataset against two configurations | 4 |
| UI shows which configuration is better and by how much | 5, including explicit tradeoffs rather than a fabricated overall winner |
| Every chat/evaluation case diagnoses retrieval vs generation failure | 3, 4, 6, 7 |
| Regression detected using explicit thresholds | 5, 6, 8 |
| Whole stack starts locally through Docker Compose | 1, 8 |

## 8. Sprint planning and review protocol

Before each iteration, review the previous gate, choose capacity-fitting stories in dependency order, settle due decisions, and assign an integration owner for shared contracts. No sprint dates or velocity are inferred from this repository.

At review, demonstrate the actual sprint gate and attach the runtime/test/browser evidence named by each story. A failed external prerequisite is reported as blocked, not marked done. Carry incomplete stories forward explicitly without deleting acceptance criteria. Adjust cadence/scope commitments based on observed capacity, while retaining the required PoC scope and the separately optional backlog.
