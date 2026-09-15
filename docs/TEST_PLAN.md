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
RB-11 historical trace rendering remains a downstream verification dependency.

Sprint 2 execution evidence and outstanding gates are recorded in
[SPRINT_2_VERIFICATION.md](SPRINT_2_VERIFICATION.md).

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
- regression threshold evaluation

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
