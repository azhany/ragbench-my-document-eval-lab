"""Airflow coordination for the Sprint 7 document-intelligence workflow.

Airflow owns scheduling/retry boundaries. The Go API owns structured
extraction, schema validation, financial validation, summary generation, and
all reviewer-visible state. Only the bounded text/OCR evidence is produced in
the Airflow worker because it already owns the document parser runtime.
"""
import json
import logging
import os
import time
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

import psycopg2
from psycopg2.extras import Json, RealDictCursor

from ragbench.content import IngestionError, extract, normalize

log = logging.getLogger(__name__)

ORDERED_STAGES = ("extract_text", "structured_extract", "schema_validate",
                  "financial_validate", "summarize")


def extraction_descriptor(mime_type):
    """Return the persisted method/profile for the selected parser."""
    if mime_type == "application/pdf":
        return "pdf_text", "pypdf-text-v1"
    if mime_type in ("image/jpeg", "image/png"):
        return "image_ocr", "tesseract-ocr-v1"
    return "document_text", "document-parser-v1"


class AnalysisError(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code


def connect():
    return psycopg2.connect(os.environ["DATABASE_URL"], connect_timeout=10)


def analysis_snapshot(analysis_id):
    conn = connect()
    try:
        with conn.cursor(cursor_factory=RealDictCursor) as cur:
            cur.execute("""SELECT a.*, d.storage_path, d.mime_type, d.deleted_at
                FROM document_analyses a JOIN documents d ON d.id=a.document_id
                WHERE a.id=%s""", (analysis_id,))
            row = cur.fetchone()
            if row is None:
                raise AnalysisError("analysis_not_found", "No persisted document analysis")
            return row
    finally:
        conn.close()


def api_stage(stage, analysis, evidence=None):
    payload = {
        "analysis_id": str(analysis["id"]),
        "job_id": str(analysis["job_id"]),
        "dag_id": analysis["dag_id"],
        "run_id": analysis["run_id"],
        "stage": stage,
    }
    if evidence is not None:
        payload["source_checksum"] = analysis["source_checksum"]
        payload["extraction_method"] = evidence["method"]
        payload["extraction_profile"] = evidence["profile"]
        payload["evidence"] = evidence
    body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
    endpoint = (os.getenv("RAGBENCH_API_URL", "http://backend:8080").rstrip("/")
                + "/api/v1/document-analyses/" + str(analysis["id"])
                + "/stages/" + stage)
    request = Request(endpoint, data=body, method="POST",
                      headers={"Content-Type": "application/json"})
    try:
        with urlopen(request, timeout=150) as response:
            return json.load(response)
    except HTTPError as exc:
        # The response body is intentionally not included in task exceptions;
        # provider/source text must not be copied into scheduler logs.
        try:
            payload = json.loads(exc.read().decode("utf-8"))
            error = payload.get("error", {})
            code = error.get("code", "stage_failed")
        except Exception:
            code = "stage_failed"
        raise AnalysisError(code, f"Go stage returned HTTP {exc.code}") from None
    except (URLError, TimeoutError):
        raise AnalysisError("api_unavailable", "Go analysis stage API is unavailable") from None


def extraction_evidence(analysis):
    method, profile = extraction_descriptor(analysis["mime_type"])
    sections = extract(
        analysis["storage_path"], analysis["mime_type"], analysis["source_checksum"],
        os.getenv("UPLOAD_DIR", "/data/uploads"),
        int(os.getenv("MAX_EXTRACTED_CHARS", "2000000")),
        int(os.getenv("MAX_DOCX_EXPANDED_BYTES", "104857600")),
        int(os.getenv("MAX_IMAGE_PIXELS", "25000000")),
        int(os.getenv("OCR_TIMEOUT_SECONDS", "30")),
    )
    normalized = normalize(sections)
    return {
        "method": method,
        "profile": profile,
        "source_checksum": analysis["source_checksum"],
        "raw_text": normalized["text"],
        "sections": [{"text": item["text"], "location": item["location"]}
                     for item in sections],
    }


def mark_failed(analysis_id, stage, code, message, dag_id=None, run_id=None,
                duration_ms=0):
    """Persist failures that happen before the Go stage endpoint is reached.

    The update is fenced by the Airflow identity and is idempotent for a
    cancellation or a previously persisted terminal result.
    """
    conn = connect()
    try:
        with conn:
            with conn.cursor(cursor_factory=RealDictCursor) as cur:
                cur.execute("""SELECT id, trace_id, job_id, dag_id, run_id,
                    structured_data IS NOT NULL AS has_structured,
                    job_state, stage_metrics FROM document_analyses
                    WHERE id=%s FOR UPDATE""",
                            (analysis_id,))
                row = cur.fetchone()
                if row is None or (dag_id and (row["dag_id"] != dag_id or row["run_id"] != run_id)):
                    return
                if row["job_state"] in ("cancelled", "succeeded"):
                    return
                stage_status = (row.get("stage_metrics") or {}).get(stage, {}).get("status")
                if stage_status == "succeeded":
                    # A task may fail after the API committed its stage. Do
                    # not overwrite a durable success with a scheduler-side
                    # failure while handling the callback.
                    return
                if row["job_state"] == "failed" and stage_status == "failed":
                    # The Go stage endpoint already persisted the terminal
                    # failure. Airflow's callback is advisory and must not
                    # add a duplicate event/retry count.
                    return
                status = "partial" if stage == "summarize" and row["has_structured"] else "failed"
                event = {"name": stage, "status": "failed", "attempt": 1,
                         "duration_ms": duration_ms, "error_code": code}
                metrics = {stage: {"duration_ms": duration_ms, "status": "failed",
                                   "error_code": code}}
                cur.execute("""UPDATE document_analyses SET stage=%s,job_state='failed',status=%s,
                    error_code=%s,error_message=%s,stage_metrics=stage_metrics || %s,
                    tool_events=tool_events || %s, retry_count=retry_count+1,
                    last_retry_reason=%s,finished_at=now(),updated_at=now()
                    WHERE id=%s""",
                            (stage, status, code, str(message), Json(metrics), Json([event]),
                             code, analysis_id))
                cur.execute("""INSERT INTO document_analysis_spans
                    (analysis_id,trace_id,span_name,started_at,duration_ms,status,metadata)
                    VALUES(%s,%s,%s,now(),%s,'failed',%s)""",
                            (analysis_id, row["trace_id"], stage, max(0, duration_ms),
                             Json({"error_code": code})))
    finally:
        conn.close()


def run_stage(analysis_id, stage, dag_id, run_id):
    if stage not in ORDERED_STAGES:
        raise AnalysisError("invalid_stage", "Unknown document-intelligence stage")
    started = time.monotonic()
    try:
        analysis = analysis_snapshot(analysis_id)
        if analysis["dag_id"] != dag_id or analysis["run_id"] != run_id:
            raise AnalysisError("job_mismatch", "DAG/run does not own this analysis")
        if stage == "extract_text":
            evidence = extraction_evidence(analysis)
            api_stage(stage, analysis, evidence)
        else:
            api_stage(stage, analysis)
        log.info("document_analysis_stage_succeeded analysis_id=%s stage=%s duration_ms=%s",
                 analysis_id, stage, round((time.monotonic() - started) * 1000))
    except IngestionError as exc:
        elapsed = round((time.monotonic() - started) * 1000)
        mark_failed(analysis_id, stage, exc.code, str(exc), dag_id, run_id, elapsed)
        raise AnalysisError(exc.code, str(exc)) from None
    except AnalysisError as exc:
        elapsed = round((time.monotonic() - started) * 1000)
        mark_failed(analysis_id, stage, exc.code, str(exc), dag_id, run_id, elapsed)
        raise
    except Exception as exc:
        elapsed = round((time.monotonic() - started) * 1000)
        mark_failed(analysis_id, stage, stage + "_failed", "stage failed; inspect the persisted analysis", dag_id, run_id, elapsed)
        raise AnalysisError(stage + "_failed", "stage failed; inspect the persisted analysis") from None


def task_failure(context):
    dag_run = context.get("dag_run")
    if not dag_run or not dag_run.conf.get("analysis_id"):
        return
    stage = context["task_instance"].task_id
    mark_failed(dag_run.conf["analysis_id"], stage, "task_failed",
                f"Airflow task {stage} failed; inspect the persisted DAG/run",
                dag_run.dag_id, dag_run.run_id, 0)
