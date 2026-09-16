# Test Plan

## Sprint 2 executable verification

Go unit tests run with `cd backend && go test ./...`; to include PostgreSQL
and concurrency checks, use a **separate migrated test database**:

```sh
docker compose exec -T postgres createdb -U ragbench ragbench_sprint2_verification
POSTGRES_DB=ragbench_sprint2_verification sh scripts/migrate.sh
cd backend
RAGBENCH_TEST_DATABASE_URL=postgres://ragbench:ragbench@localhost:5432/ragbench_sprint2_verification go test -race ./...
```

Create the database once; use a fresh name if that name already belongs to
another workflow. Match local credentials/ports if overridden. Document tests
remove their own rows; existing configuration tests retain their saved configs
in this test database. Never point integration tests at a production database.

From the repository root, run the Python suite in the actual Airflow image:

```sh
docker compose run --rm --no-deps \
  -v "$PWD/airflow/tests:/opt/airflow/tests:ro" \
  -e PYTHONPATH=/opt/airflow/dags \
  -e RAGBENCH_TEST_DATABASE_URL=postgres://ragbench:ragbench@postgres:5432/ragbench_sprint2_verification \
  airflow-scheduler python -m unittest discover -s /opt/airflow/tests -v
```

This covers generated text-bearing PDF/DOCX/TXT fixtures (including DOCX table
evidence), corrupt/encrypted/image-only/empty failures, Unicode/page locations,
exact boundaries/overlap, stable retry identity, wrong vector dimensions,
partial provider failure, nonfinite/zero vectors, FTS/1536-dimensional vectors,
incomplete publication, failed replacement, old task retries after replacement,
profile isolation, and deletion during embedding. Controlled provider doubles
are injected only in tests; the shipped DAG always uses the real integration.

Frontend: `cd frontend && npm test && npm run build`. The Library tests cover
multipart upload/config selection, actual errors, prior searchable count,
reprocess, dispatch recovery, deletion confirmation and unavailable API state.

Actual HTTP → Airflow smoke (synthetic fixtures only):

```sh
sh scripts/smoke-documents.sh --expect-missing-key  # verifies extraction and visible missing-key failure
sh scripts/smoke-documents.sh                     # requires configured real embedding key
```

The smoke creates one saved configuration and four uniquely named documents
(TXT, DOCX, PDF and a corrupt PDF), observes each run, then reprocesses and
deletes the TXT through the API. In real-provider mode it first waits for a
successful replacement and checks active revision switching with retained ready
history, then queues another replacement for deletion during processing.
It retains source/chunk/job evidence for review
and prints IDs. The normal mode requires real processed output; it cannot pass
with fake embeddings. The missing-key mode explicitly does **not** satisfy the
RB-07 real-provider acceptance criterion. Do not use it when a key is configured.

Browser gate: open Library, upload each format with a saved config, observe
queued/processing/processed and nonzero counts; inspect an extraction failure,
revision history, reprocess failure with prior evidence, retry-dispatch and
delete confirmation. Test a narrow viewport and keyboard file/config controls.
Sprint 3 historical trace rendering is covered by the Chat checks below.

Sprint 2 execution evidence and outstanding gates are recorded in
[SPRINT_2_VERIFICATION.md](SPRINT_2_VERIFICATION.md).

## Sprint 3 executable verification

Backend unit and PostgreSQL integration coverage:

```sh
cd backend
RAGBENCH_TEST_DATABASE_URL=postgres://ragbench:ragbench@localhost:5432/<fresh-db> \
  go test -race ./...
```

The migrated database must be separate from the application database. Sprint 3
tests cover deterministic pgvector ranking/ties/top-k and compatible revision
filtering; prompt budgeting; citation missing/invalid/insufficient outcomes;
provider response validation; timeout/rate-limit/malformed classifications;
trace list/detail snapshots; exact cost components; and atomic trace-write
failure behavior.

Frontend verification:

```sh
cd frontend
npm test
npm run build
```

Live failure-path smoke, with the stack rebuilt and healthy, exercises a chat
request through the real API using the configured provider credentials. A
provider failure must return a classified error with `trace_id`; fetching
`GET /api/v1/traces/{trace_id}` must show the failed trace and executed spans.
This check does not print credentials or provider response bodies.

Authorized happy-path smoke additionally requires a processed document whose
embedding identity matches the selected configuration and valid embedding and
generation credentials. Ask a grounded question, assert a non-empty answer
and mapped citations, then fetch the trace detail and compare its prompt,
context, usage, cost, and spans with the response. No test double or
`INSUFFICIENT_EVIDENCE` response satisfies the real-provider gate.

The executable smoke can cover RB-07 through RB-11 in one run. To exercise the
non-OpenAI integrations, configure `HF_TOKEN` and `OPENCODE_API_KEY`, rebuild
the backend/Airflow services, and run:

```sh
SMOKE_EMBEDDING_PROFILE=huggingface-bge-small-en-v1.5 \
SMOKE_MODEL_PROFILE=opencode-go-glm-5.3-flash \
  sh scripts/smoke-documents.sh --chat
```

It verifies native 384d Hugging Face ingestion, compatible query embedding,
OpenAI-compatible Chat Completions generation, citations, trace/spans, and
historical trace context after reprocess/delete. It never prints credentials
or provider error bodies.

Browser verification opens Chat, selects a saved configuration, submits the
same grounded question, opens a citation and persisted trace, inspects the
historical context drawer, then exercises loading, empty retrieval, provider
failure, and unavailable-cost states. Reopening a trace must show its saved
configuration/context after current UI selection changes.

Sprint 3 retains two external prerequisites: valid provider credentials for
the authorized happy path and a connected browser for the walkthrough. A
failed prerequisite is recorded as blocked rather than presented as a
successful story acceptance.

## Unit tests

### Retrieval
- hybrid score ordering
- top-k truncation
- no-result behavior

### Cost
- token cost calculation
- unknown model profile behavior

### Evaluation
- Recall@K
- MRR
- graded nDCG@K gain/discount, ties, missing judgments and zero ideal gain
- regression threshold evaluation

## Sprint 6 and optional backlog verification

The end-to-end demonstration is `sh scripts/smoke-poc.sh` after Compose is
healthy and authorized provider credentials are configured. It seeds the
reviewed three-source corpus and golden dataset, launches the normal Airflow
evaluation path, and prints run IDs. Inspect the same IDs in Evaluations and
Monitor; no sample metrics are accepted.

For the poor-retrieval failure mode, create a second immutable configuration
with a deliberately small chunk size and low top-k, reprocess the same corpus,
and launch the same pinned dataset. Compare it with the baseline under the
persisted policy. Record the observed Recall@K/MRR/judge values and policy
state; if the chosen fixture does not regress, report that outcome and select
an evidence-sensitive fixture/configuration without changing thresholds.

RB-25 verification enables `rerank_enabled` with `reranker_profile=lexical-v1`
and a candidate limit. Assert a `rerank` span, changed final order where the
fixture has a lexical signal, and visible `rerank_failed` behavior from a
controlled failing reranker; the disabled configuration must have no rerank
span. RB-26 imports a case with `graded_judgments`, checks hand-calculated
nDCG@K and confirms runs with different `ndcg_policy_version` values are not
comparable.

RB-23 verification creates an enabled persisted regression check, triggers
one isolated `rag_scheduled_regression` interval, inspects the linked run and
comparison, then disables it. A missing/incomplete baseline must remain
`not_evaluable`, and local Compose must show the DAG with no automatic schedule
when the opt-in variables are absent.

## Integration tests

- upload → queued document
- processed document → chunks available
- chat → citations + trace persisted
- eval run → aggregate metrics persisted
- failed provider request → classified error trace

## One important failure-mode demo

Intentionally configure a poor retrieval setup, for example:
- very small chunk size
- vector-only retrieval
- low top-k

Run the same dataset and show:
- Recall@K drops
- answer faithfulness/relevance changes
- trace still identifies a healthy model call
- comparison page flags a regression

This makes the PoC tell a stronger engineering story than a normal happy-path chatbot.
