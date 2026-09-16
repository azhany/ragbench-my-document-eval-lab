"""Importable evaluation orchestration client (RB-14/RB-18): tiny HTTP calls
against the Go API. All evaluation logic — the query pipeline, scoring,
result persistence and aggregate run status — lives in Go; this module only
drives execution, so Python never duplicates retrieval, prompt construction,
generation or scoring logic.
"""
import json
import logging
import os
import time
import urllib.error
import urllib.request

logger = logging.getLogger(__name__)

API_BASE_URL = os.environ.get("GO_API_URL", "http://backend:8080")

# Terminal experiment states (RB-18); mirrors the Go experiment statuses.
TERMINAL_STATES = {"completed", "partial", "failed", "dispatch_failed"}


class APIError(Exception):
    """A non-2xx API response; failures stay visible, never silent."""


def _post(path, timeout=120):
    req = urllib.request.Request(API_BASE_URL + path, method="POST", data=b"{}",
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as err:
        detail = err.read().decode("utf-8", "replace")
        raise APIError(f"API HTTP {err.code} on POST {path}: {detail[:300]}") from None


def _get(path):
    req = urllib.request.Request(API_BASE_URL + path, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as err:
        detail = err.read().decode("utf-8", "replace")
        raise APIError(f"API HTTP {err.code} on GET {path}: {detail[:300]}") from None


def execute_case(run_id, case_id):
    """Execute one case idempotently: the Go endpoint stores exactly one
    result row per (run, case), so retried tasks return stored state."""
    return _post(f"/api/v1/eval-runs/{run_id}/cases/{case_id}/execute")


def run_status(run_id):
    return _get(f"/api/v1/eval-runs/{run_id}")


def results(run_id):
    return _get(f"/api/v1/eval-runs/{run_id}/results").get("results", [])


def finalize_run(run_id):
    return _post(f"/api/v1/eval-runs/{run_id}/finalize", timeout=60)


def dataset_cases(run):
    return _get(f"/api/v1/eval-datasets/{run['dataset_id']}?version={run['dataset_version']}").get("cases", [])


def run_pending_cases(run_id):
    """Orchestration loop: execute every pending case. Re-entry after a
    retry finds stored terminal result rows and skips completed work."""
    attempted = {r["eval_case_id"] for r in results(run_id)}
    run = run_status(run_id)
    case_list = dataset_cases(run)
    if not case_list:
        raise APIError("pinned dataset version has no cases; the run cannot succeed")
    for case in case_list:
        if case["id"] in attempted:
            logger.info("case %s already %s", case["case_key"], attempted_status(run_id, case))
            continue
        result = execute_case(run_id, case["id"])
        logger.info("case %s -> %s", case["case_key"], result.get("status"))


def attempted_status(run_id, case):
    for r in results(run_id):
        if r["eval_case_id"] == case["id"]:
            return r["status"]
    return "pending"


def advance_one_step(experiment_id):
    """RB-18 sweep orchestration: perform exactly one idempotent experiment
    step; repeats until the experiment state is terminal."""
    return _post(f"/api/v1/experiments/{experiment_id}/advance")


def finalize_experiment(experiment_id):
    """Keep advancing until terminal (bounded by Airflow's task timeout)."""
    terminal = {"completed", "partial", "failed", "dispatch_failed"}
    for _ in range(1000):
        result = advance_one_step(experiment_id)
        if result["state"] in terminal:
            return result
    raise APIError(f"experiment {experiment_id} did not reach a terminal state")
