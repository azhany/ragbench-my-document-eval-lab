# Airflow DAGs

Implemented DAGs (Airflow 3.0.6 Task SDK):

- `document_ingestion`
- `document_reindex`

Both use the same five stages: extract → normalize → chunk → embed → publish.
They are manually dispatched by Go with `conf: {"job_id":"UUID"}` and the
persisted `rb_<revision UUID>` run ID. There is no scheduled ingestion scan.
The public API and task validate job ownership. PostgreSQL supplies source
identity and settings; XCom never carries document contents or vectors.

Reusable logic lives in `ragbench/`. Parser dependencies are pinned in
`airflow/requirements.txt` and installed by `airflow/Dockerfile`. PDF is text
extraction only (no OCR). DOCX includes body paragraphs, headings and table
rows in source order; headers/footers/comments and embedded objects are outside
this PoC. TXT must be UTF-8 (BOM accepted).

Chunk units are Unicode characters after NFC normalization, control removal,
whitespace collapse and a newline between source blocks. Fixed-size windows
advance by size minus overlap. The final window ends at EOF; no redundant
overlap-only tail is emitted. Blank windows are skipped. These are not tokenizer
units; a provider token-limit error is surfaced as `embedding_failed`.

`OPENAI_API_KEY` or `EMBEDDING_PROVIDER_API_KEY` is required in Airflow's private
environment for real embeddings. No fake provider mode is available in the DAG.
The selected profile is `openai/text-embedding-3-small/1536`. The client sends
explicit dimensions and float encoding, checks response model/order/count/
dimensions/finiteness/nonzero vectors, then writes a complete batch of vectors
transactionally. Provider failure never publishes an incomplete revision.

Stages retry twice, ten seconds apart, with a ten-minute task timeout. To recover
an exhausted failure, use Library Reprocess (new revision). Clearing the same
task in Airflow is also idempotent while that revision is still latest; a later
revision or deletion fences off the old task. Parser/chunk changes that alter
existing chunk evidence fail with `revision_conflict` instead of overwriting it.

Implemented since Sprint 4/5 (orchestration only — evaluation logic lives in
`ragbench/evaluation.py`, which just calls the Go API; Python never
duplicates retrieval, prompt construction, generation or scoring logic):

- `rag_evaluation` (RB-14): conf `{"run_id": UUID}`; drives case execution
  through the Go API per case (idempotent per run+case) then re-computes the
  aggregate run status via finalize. Two tasks: `cases`, `finalize`.
- `rag_parameter_sweep` (RB-18): conf `{"experiment_id": UUID}`; performs one
  idempotent `advance` step per call until the experiment is terminal; a
  failed terminal state is surfaced as task failure.

Keep DAGs orchestration-focused. Put reusable evaluation logic in importable Python modules rather than embedding everything in DAG definitions.
