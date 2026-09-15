"""Database-backed Airflow stages. Each write rechecks deletion and ownership.

Only job IDs cross XCom. PostgreSQL stores stage handoffs and timing data. A
session advisory lock serializes retries of one job without holding a document
row lock during parser/provider work, so delete remains responsive.
"""
import logging
import os
import time

import psycopg2
from psycopg2.extras import Json, RealDictCursor

from ragbench.content import IngestionError, chunk, extract, normalize
from ragbench.embeddings import OpenAIEmbedder, embed_chunks, validate_vectors

log = logging.getLogger(__name__)


def connect():
    return psycopg2.connect(os.environ["DATABASE_URL"], connect_timeout=10)


def snapshot(conn, job_id):
    with conn.cursor(cursor_factory=RealDictCursor) as cur:
        cur.execute("""SELECT j.*, r.document_id, r.source_checksum, r.chunk_size,
            r.chunk_overlap, r.embedding_provider, r.embedding_model, r.embedding_dimensions,
            r.status AS revision_status, d.storage_path, d.mime_type, d.deleted_at,
            d.latest_revision_id
            FROM ingestion_jobs j JOIN index_revisions r ON r.id=j.index_revision_id
            JOIN documents d ON d.id=r.document_id WHERE j.id=%s FOR UPDATE OF d""", (job_id,))
        row = cur.fetchone()
        if row is None:
            raise IngestionError("job_not_found", "No persisted ingestion job")
        return row


def inactive(job):
    return (job["deleted_at"] is not None or job["state"] == "cancelled"
            or job["latest_revision_id"] != job["index_revision_id"]
            or job["revision_status"] == "ready")


def mark_failed(conn, job_id, stage, error, elapsed):
    with conn:
        job = snapshot(conn, job_id)
        if inactive(job):
            return
        with conn.cursor() as cur:
            cur.execute("""UPDATE index_revisions SET status='failed',error_code=%s,
                published_at=NULL WHERE id=%s""", (error.code, job["index_revision_id"]))
            cur.execute("""UPDATE ingestion_jobs SET state='failed',stage=%s,error_code=%s,
                error_message=%s,finished_at=now(),updated_at=now(),
                stage_metrics=stage_metrics || %s WHERE id=%s""",
                        (stage, error.code, str(error), Json({stage: {"duration_ms": elapsed, "status": "failed"}}), job_id))
            cur.execute("UPDATE documents SET status='failed',updated_at=now() WHERE id=%s", (job["document_id"],))


def run_stage(job_id, stage, dag_id=None, run_id=None, embedder=None):
    start = time.monotonic()
    conn = connect()
    try:
        with conn.cursor() as cur:
            cur.execute("SELECT pg_advisory_lock(hashtextextended(%s,0))", (str(job_id),))
        conn.commit()
        with conn:
            job = snapshot(conn, job_id)
            if dag_id is not None and (job["dag_id"] != dag_id or job["run_id"] != run_id):
                raise IngestionError("job_mismatch", "DAG/run does not own this job")
            if inactive(job):
                log.info("ingestion_stage_skipped job_id=%s stage=%s", job_id, stage)
                return
            with conn.cursor() as cur:
                cur.execute("UPDATE index_revisions SET status='pending',error_code=NULL WHERE id=%s", (job["index_revision_id"],))
                cur.execute("""UPDATE ingestion_jobs SET state='processing',stage=%s,
                    error_code=NULL,error_message=NULL,started_at=COALESCE(started_at,now()),
                    finished_at=NULL,updated_at=now() WHERE id=%s""", (stage, job_id))
                cur.execute("UPDATE documents SET status='processing',updated_at=now() WHERE id=%s", (job["document_id"],))
        try:
            output = compute_stage(job, stage, embedder)
            with conn:
                current = snapshot(conn, job_id)
                if inactive(current):
                    log.info("ingestion_stage_discarded job_id=%s stage=%s", job_id, stage)
                    return
                apply_stage(conn, job, stage, output)
                elapsed = round((time.monotonic() - start) * 1000)
                metrics = {"duration_ms": elapsed, "status": "succeeded"}
                if stage == "embed":
                    metrics.update({key: value for key, value in output.items() if key != "vectors"})
                with conn.cursor() as cur:
                    cur.execute("""UPDATE ingestion_jobs SET stage_metrics=stage_metrics || %s,
                        updated_at=now() WHERE id=%s""",
                                (Json({stage: metrics}), job_id))
            log.info("ingestion_stage_succeeded document_id=%s revision_id=%s job_id=%s stage=%s duration_ms=%s",
                     job["document_id"], job["index_revision_id"], job_id, stage, elapsed)
        except Exception as exc:
            conn.rollback()
            error = exc if isinstance(exc, IngestionError) else IngestionError(
                "indexing_failed" if stage == "publish" else f"{stage}_failed",
                f"{stage} failed ({type(exc).__name__}); inspect the correlated task log")
            mark_failed(conn, job_id, stage, error, round((time.monotonic() - start) * 1000))
            log.error("ingestion_stage_failed job_id=%s stage=%s error_code=%s detail=%s", job_id, stage, error.code, error)
            # Do not include provider exception bodies (which may contain source text).
            raise error from None
    finally:
        conn.close()  # also releases the session advisory lock


def compute_stage(job, stage, embedder=None):
    artifacts = job["artifacts"]
    if stage == "extract":
        return extract(job["storage_path"], job["mime_type"], job["source_checksum"],
                       os.getenv("UPLOAD_DIR", "/data/uploads"),
                       int(os.getenv("MAX_EXTRACTED_CHARS", "2000000")),
                       int(os.getenv("MAX_DOCX_EXPANDED_BYTES", "104857600")))
    if stage == "normalize":
        return normalize(artifacts["extract"])
    if stage == "chunk":
        return chunk(artifacts["normalize"], job["index_revision_id"], job["chunk_size"], job["chunk_overlap"])
    if stage == "embed":
        return embed_chunks(artifacts["chunk"], job["embedding_provider"], job["embedding_model"],
                            job["embedding_dimensions"], embedder or OpenAIEmbedder(),
                            int(os.getenv("EMBEDDING_BATCH_SIZE", "16")))
    if stage == "publish":
        return None
    raise IngestionError("invalid_stage", "Unknown ingestion stage")


def apply_stage(conn, job, stage, output):
    with conn.cursor() as cur:
        if stage == "embed":
            chunks = job["artifacts"]["chunk"]
            validate_vectors(output["vectors"], len(chunks), job["embedding_dimensions"])
            for item, vector in zip(chunks, output["vectors"]):
                cur.execute("UPDATE doc_chunks SET embedding=%s::vector WHERE id=%s AND index_revision_id=%s",
                            (str(vector), item["id"], job["index_revision_id"]))
                if cur.rowcount != 1:
                    raise IngestionError("indexing_failed", "An expected chunk is missing")
            # Keep usage, not another copy of every vector, in stage handoff.
            output = {key: value for key, value in output.items() if key != "vectors"}
        if stage == "publish":
            cur.execute("""SELECT count(*),count(embedding) FROM doc_chunks WHERE index_revision_id=%s""",
                        (job["index_revision_id"],))
            count, indexed = cur.fetchone()
            if count == 0 or count != indexed or count != len(job["artifacts"]["chunk"]):
                raise IngestionError("indexing_failed", "Revision is incomplete; no searchable revision was published")
            cur.execute("UPDATE index_revisions SET status='ready',published_at=now(),error_code=NULL WHERE id=%s",
                        (job["index_revision_id"],))
            cur.execute("""UPDATE documents SET active_revision_id=%s,chunk_count=%s,status='processed',
                updated_at=now() WHERE id=%s AND deleted_at IS NULL""", (job["index_revision_id"], count, job["document_id"]))
            cur.execute("""UPDATE ingestion_jobs SET state='succeeded',finished_at=now(),error_code=NULL,
                error_message=NULL,artifacts='{}'::jsonb WHERE id=%s""", (job["id"],))
            return
        if stage == "chunk":
            for item in output:
                cur.execute("""INSERT INTO doc_chunks(id,index_revision_id,document_id,chunk_index,content,metadata)
                    VALUES(%s,%s,%s,%s,%s,%s) ON CONFLICT(index_revision_id,chunk_index) DO NOTHING""",
                            (item["id"], job["index_revision_id"], job["document_id"], item["index"], item["content"], Json(item["metadata"])))
                if cur.rowcount == 0:
                    cur.execute("SELECT id,content,metadata FROM doc_chunks WHERE index_revision_id=%s AND chunk_index=%s",
                                (job["index_revision_id"], item["index"]))
                    if cur.fetchone() != (item["id"], item["content"], item["metadata"]):
                        raise IngestionError("revision_conflict", "Retry changed historical chunk content; create a new revision")
        cur.execute("UPDATE ingestion_jobs SET artifacts=artifacts || %s WHERE id=%s", (Json({stage: output}), job["id"]))


def task_failure(context):
    """Also expose scheduler/task timeout failures outside normal stage errors."""
    dag_run = context.get("dag_run")
    if not dag_run or not dag_run.conf.get("job_id"):
        return
    conn = connect()
    try:
        stage = context["task_instance"].task_id
        with conn:
            job = snapshot(conn, dag_run.conf["job_id"])
            if job["dag_id"] != dag_run.dag_id or job["run_id"] != dag_run.run_id or job["state"] == "failed":
                return
        mark_failed(conn, job["id"], stage,
                    IngestionError("task_failed", f"Airflow task {stage} failed; inspect the persisted DAG/run"), 0)
    finally:
        conn.close()
