# Sprint Plan and Implementation Stories

Status: implementation in progress. Story files record current completion and outstanding verification gates.

## 1. Baseline and planning assumptions

The original planning baseline was design documents, a banner-only Go entry point,
and partial Compose configuration. Sprint 1 has since delivered the foundation.
Sprint 2 implementation and verification are recorded in
[SPRINT_2_VERIFICATION.md](SPRINT_2_VERIFICATION.md); native Hugging Face
provider verification passes and the browser exit gate remains open.
Documentation/scaffolding alone never count as completion. Sprint 3
implementation and open verification gates are recorded in the RB-09–RB-12
story files; real query embedding/retrieval and classified provider failure
pass, while successful generation (OpenCode Go HTTP 429) and browser gates
remain open.

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
| [API contract](API.md) | Existing endpoint names and chat/trace response shape |
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
| Historical evidence and deletion | Adopted for this PoC: tombstone to exclude new retrieval, cancel unfinished work, and retain source bytes/chunks/revisions/configuration indefinitely for historical evidence. A source-erasure/snapshot-compaction policy is deferred until trace retention exists (RB-11); deletion is explicitly not an erasure API. See API/data-model contracts. | Sprint 2 / backend |
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

Each story now lives in its own Markdown file under `docs/stories/`. Those files are the source of truth for story details and task progress; the roadmap and shared policies remain here.

Open a story and change `- [ ]` to `- [x]` only after completing the task or verifying the acceptance criterion. Update its status as work progresses. All stories initially remain **Not started**; creating these documents does not complete implementation work.

Every story inherits the [shared definition of ready and done](#5-shared-definition-of-ready-and-done). Verification describes future implementation checks, not tests that currently exist or have already passed.

### Sprint 1 — Runnable foundation

- [RB-01 — Boot the local application stack](stories/RB-01.md)
- [RB-02 — Establish durable schema and migrations](stories/RB-02.md)
- [RB-03 — Persist immutable RAG configurations](stories/RB-03.md)
- [RB-04 — Connect the Vue application shell](stories/RB-04.md)

### Sprint 2 — Document library

- [RB-05 — Upload and dispatch documents](stories/RB-05.md)
- [RB-06 — Extract, normalize, and chunk source content](stories/RB-06.md)
- [RB-07 — Embed and publish searchable revisions](stories/RB-07.md)
- [RB-08 — Operate the Library lifecycle](stories/RB-08.md)

### Sprint 3 — Traceable chat

- [RB-09 — Retrieve ranked vector evidence in Go](stories/RB-09.md)
- [RB-10 — Generate grounded answers and citations](stories/RB-10.md)
- [RB-11 — Persist traces, token usage, and cost](stories/RB-11.md)
- [RB-12 — Deliver the evidence-connected Chat screen](stories/RB-12.md)

### Sprint 4 — Repeatable evaluation

- [RB-13 — Maintain a versioned golden dataset](stories/RB-13.md)
- [RB-14 — Orchestrate durable evaluation runs](stories/RB-14.md)
- [RB-15 — Score retrieval and generation separately](stories/RB-15.md)
- [RB-16 — Run and inspect evaluations in the UI](stories/RB-16.md)

### Sprint 5 — Measurable experiments

- [RB-17 — Add configurable hybrid retrieval](stories/RB-17.md)
- [RB-18 — Execute bounded parameter experiments](stories/RB-18.md)
- [RB-19 — Compare runs with persisted regression policy](stories/RB-19.md)
- [RB-20 — Show configuration differences and comparison results](stories/RB-20.md)

### Sprint 6 — Observable PoC

- [RB-21 — Aggregate operational and quality metrics](stories/RB-21.md)
- [RB-22 — Deliver Overview and Monitor surfaces](stories/RB-22.md)
- [RB-23 — Schedule repeatable regression checks](stories/RB-23.md)
- [RB-24 — Prove the complete PoC and failure-mode story](stories/RB-24.md)

### Optional backlog — not required for the six-sprint core gate

- [RB-25 — Enable real reranking as an experiment dimension](stories/RB-25.md)
- [RB-26 — Add graded nDCG@K scoring](stories/RB-26.md)

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
