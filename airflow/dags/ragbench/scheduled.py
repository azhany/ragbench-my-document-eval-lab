"""Client for the opt-in scheduled regression check.

The DAG only coordinates the existing Go evaluation and comparison APIs. It
does not retrieve, judge, score, or invent a baseline.
"""
import json
import logging
import os
import time
import urllib.error
import urllib.parse
import urllib.request

from ragbench.evaluation import APIError

log = logging.getLogger(__name__)
API_BASE_URL = os.environ.get("GO_API_URL", "http://backend:8080")


def _request(path, method="GET", body=None, timeout=30):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(API_BASE_URL + path, data=data, method=method,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            return json.load(response)
    except urllib.error.HTTPError as err:
        detail = err.read().decode("utf-8", "replace")
        raise APIError(f"API HTTP {err.code} on {method} {path}: {detail[:300]}") from None


def run_scheduled_check(check_id, poll_seconds=5, timeout_seconds=2700):
    started = _request(f"/api/v1/regression-checks/{check_id}/start", "POST", {}, 60)
    run = started["run"]
    run_id = run["id"]
    deadline = time.time() + timeout_seconds
    while True:
        status = _request(f"/api/v1/eval-runs/{run_id}")
        if status["status"] in {"completed", "partial", "failed", "dispatch_failed"}:
            break
        if time.time() >= deadline:
            raise APIError(f"scheduled evaluation {run_id} did not finish before timeout")
        time.sleep(poll_seconds)

    check = _request(f"/api/v1/regression-checks/{check_id}")
    policy = urllib.parse.quote(check["policy_name"])
    version = f"&version={check['policy_version']}" if check.get("policy_version", 0) else ""
    comparison = _request(f"/api/v1/eval-runs/{run_id}/compare/{check['baseline_run_id']}?policy={policy}{version}")
    finished = _request(f"/api/v1/regression-checks/{check_id}/finish", "POST",
                        {"verdict": comparison["verdict"]})
    log.info("scheduled regression check %s -> %s", check_id, finished["status"])
    if finished["status"] in {"regression", "failed"}:
        raise APIError(f"scheduled regression check {check_id} -> {finished['status']}: {finished.get('reason', '')}")
    return finished
