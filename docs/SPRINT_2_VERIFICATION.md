# Sprint 2 verification — RB-05 through RB-08

Implementation was performed in dependency order on 2026-09-14. The stories
remain subject to the shared runtime/browser/provider gates; code completion
alone is not marked as full acceptance.

## Checks exercised

- Go unit/integration suite with `-race` against the separate
  `ragbench_sprint2_verification` database: passed. Includes upload format/size/
  traversal rejection and cleanup, durable dispatch failure/retry correlation,
  Airflow conflict ownership, byte duplicate rejection, concurrent reprocess,
  tombstone/repeated delete and late-dispatch cancellation.
- Airflow-image Python suite: 10 tests passed against real PostgreSQL/pgvector,
  including PDF/DOCX/TXT extraction, boundaries, partial/dimension failures,
  atomic publication, vectors/FTS, retries, profile filtering, failed replacement,
  stale task fencing and delete during embedding. Provider doubles are test-only.
- Frontend: 19 tests passed; production bundle built successfully.
- Compose images built and affected services started. API `/readyz` reports
  healthy database and schema version 6. Both `document_ingestion` and
  `document_reindex` registered in Airflow with manual scheduling, unpaused.
- Live `sh scripts/smoke-documents.sh --expect-missing-key`: passed through
  the HTTP API, shared volume, Airflow v2 dispatch and actual scheduled tasks.
  All three supported formats reached successful chunking before the expected
  classified missing-key embedding failure. A corrupt PDF failed extraction.
  Reprocess dispatched `document_reindex`; repeated delete returned 204 and
  removed the TXT from the live list. No zero-chunk revision was published.
- A later database check confirmed that the tombstoned TXT's reindex job stayed
  cancelled. Read-only source reconciliation found four referenced sources and
  no missing or unreferenced files, including the retained deleted source.

Runtime evidence retained in the local application database:

| Evidence | ID |
|---|---|
| Smoke configuration | `975dae24-9f6d-48ba-88e5-25a7b5936088` |
| TXT (tombstoned after reprocess) | `e860a3ed-058e-4a3a-a8e0-4f7d6ce5b360` |
| DOCX | `2cfdae33-10f5-44e6-998a-22cad49f1a78` |
| PDF | `6e940df7-9ab8-48f2-a09f-2e79f34c128f` |
| Corrupt PDF | `36add5cf-827c-4b66-bdc1-156984a219ac` |

The three live synthetic fixtures remain visible in Library with their real
errors. The deleted TXT's source, chunks and job history remain retained by
the documented policy. These are synthetic verification inputs, not user data.

## Outstanding external gates

- **Real provider smoke:** after keys were configured and Airflow recreated,
  both `EMBEDDING_PROVIDER_API_KEY` and `OPENAI_API_KEY` independently returned
  HTTP 401 (2026-09-15). Correct the credentials privately and recreate Airflow
  before rerunning `sh scripts/smoke-documents.sh`. No real embedding success
  is claimed. The configured embedding-specific key takes precedence.
- **Browser verification:** the original computer-use inventory returned no
  browsers, but the Playwright/Chromium harness became available on
  2026-09-16. Its read-only live walkthrough passed 16/16 checks, including
  Library processed/failed states, errors/chunk counts, revision history and
  RB-12-style retained trace evidence. Browser upload and
  browser-triggered reprocess/delete remain mutation-specific follow-up checks;
  the underlying lifecycle is covered by the recorded HTTP/Airflow smoke.
- **Historical trace rendering:** RB-11 is now present and the Playwright
  walkthrough opened retained trace evidence successfully. Database retention
  and trace rendering are both covered; the remaining RB-08 follow-up is only
  browser-triggered mutation coverage.

These limitations carry forward explicitly; no test double is presented as a
real-provider or browser success.

## Reverification — 2026-09-15

The user explicitly prohibited reading `.env`. It was not opened, displayed,
searched, sourced by an inspection command, or copied. Docker Compose loaded
the configured service environment normally; no environment dump or credential
value was exposed. Credential probes used only the existing runtime values to
authenticate tiny synthetic embedding requests and printed HTTP status only.

- Recreated Airflow API server, DAG processor and scheduler. All services
  became healthy; Go `/readyz` passed database/schema checks.
- Re-ran 10 pipeline unit/integration tests against the isolated PostgreSQL
  database: passed, including partial failure, dimensions, idempotency, failed
  replacement, profile isolation and deletion during embedding.
- Re-ran 19 frontend tests: passed. Go document/API tests with the race detector
  also passed (cached results for unchanged code).
- Started the real `smoke-documents.sh` run. TXT, DOCX and PDF each produced two
  source chunks, then failed embedding with `embedding_failed` / OpenAI HTTP
  401. SQL inspection confirmed zero vectors and null active revision pointers
  for all three. The corrupt PDF produced `corrupt_document` and zero chunks.
- Tested each configured credential separately inside Airflow with one tiny
  synthetic embedding request: both returned HTTP 401. No error response body
  or credential value was printed.
- Extended the real-provider smoke to wait for a successful replacement,
  assert active revision switching and two retained ready revisions, then queue
  another revision for deletion during processing. These new success assertions
  remain unexecuted because the initial embedding request failed authentication.
- Rechecked browser availability through the Playwright harness: the live
  read-only walkthrough passed 16/16 checks. Mutation-specific browser actions
  remain a follow-up; RB-11 historical trace rendering is now exercised.

New retained runtime evidence:

| Evidence | ID |
|---|---|
| Configuration | `853dac4d-4faa-4699-abe5-b438184ce74d` |
| TXT | `c493e844-7e65-41d7-98ed-027db53cf61f` |
| DOCX | `6f357a19-3f56-46a7-9737-e6e14a13859d` |
| PDF | `e047b38f-f80d-4bbd-8fe3-60aa00906a03` |
| Corrupt PDF | `dca2bf23-47ca-42f2-ac63-c5f3296fa5c9` |

RB-07 is fully accepted. RB-08's browser-environment blocker is cleared;
authentication is no longer a blocker for the recorded provider lifecycle.

## Provider compatibility reverification — 2026-09-16

The HTTP 401 blocker was traced to OpenAI-only routing of non-OpenAI
credentials. Native Hugging Face embedding support, a 384d pgvector migration,
and OpenAI-compatible generation routing were added and verified. The full
real Hugging Face TXT/DOCX/PDF lifecycle and successful replacement passed;
SQL confirmed eight 384d ready vectors and eight FTS rows. RB-07 is now fully
verified. RB-08's browser-environment blocker is cleared; only
mutation-specific browser actions remain a follow-up. Full evidence is in
[PROVIDER_COMPATIBILITY_VERIFICATION.md](PROVIDER_COMPATIBILITY_VERIFICATION.md).
