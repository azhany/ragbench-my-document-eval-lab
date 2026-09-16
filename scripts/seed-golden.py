#!/usr/bin/env python3
"""RB-13 golden dataset seeding (idempotent).

  1. ensure the demonstration rag config exists (create if missing)
  2. upload each demonstration corpus file not already processed
  3. wait until every document is processed (real embeddings; the script
     refuses to fabricate evidence and reports ingestion failures visibly)
  4. map expected_source_name -> live document id from the document list
  5. import the versioned golden dataset with resolved document references

Usage: API_URL=http://localhost:8080 python3 scripts/seed-golden.py
"""
import json
import os
import sys
import time
import urllib.request

BASE = os.environ.get("API_URL", "http://localhost:8080")
FIXTURE = "db/fixtures/golden_dataset_v1.json"
CORPUS_DIR = "db/fixtures/corpus"
CONFIG_NAME = os.environ.get("GOLDEN_CONFIG_NAME", "golden-demo-hf")
DATASET_NAME = "golden-dataset-v1"
WAIT_SECONDS = int(os.environ.get("SEED_WAIT_SECONDS", "600"))


def request(path, method="GET", body=None):
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(BASE + path, data=data, method=method, headers=headers)
    with urllib.request.urlopen(req) as resp:
        return json.load(resp)


def post(path, body):
    data = json.dumps(body).encode()
    req = urllib.request.Request(BASE + path, data=data, method="POST",
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        return json.load(resp)


def get(path):
    with urllib.request.urlopen(BASE + path) as resp:
        return json.load(resp)


def ensure_config(configs):
    for c in configs:
        if c.get("name") == CONFIG_NAME:
            print(f"rag config exists: {CONFIG_NAME} -> {c['id']}")
            return c["id"]
    try:
        created = post("/api/v1/rag-configs", {
            "name": CONFIG_NAME,
            "chunk_size": 800,
            "chunk_overlap": 120,
            "retrieval_mode": "vector",
            "top_k": 5,
            "rerank_enabled": False,
            "prompt_version": "v1",
            "model_profile": "opencode-go-glm-5.3-flash",
            "embedding_profile": "huggingface-bge-small-en-v1.5",
        })
        print(f"rag config created: {CONFIG_NAME} -> {created['id']}")
        return created["id"]
    except urllib.error.HTTPError as e:
        if e.code == 409:
            # Lost a create race (e.g. retried run): resolve by refetch.
            for c in get("/api/v1/rag-configs")["configs"]:
                if c["name"] == CONFIG_NAME:
                    print(f"rag config exists: {CONFIG_NAME} -> {c['id']}")
                    return c["id"]
        raise


def upload_doc(path, config_id):
    boundary = "ragbench-seed-boundary"
    filename = os.path.basename(path)
    with open(path, "rb") as fh:
        content = fh.read()
    body = b""
    body += f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{filename}\"\r\nContent-Type: text/plain\r\n\r\n".encode()
    body += content + b"\r\n"
    body += f"--{boundary}\r\nContent-Disposition: form-data; name=\"config_id\"\r\n\r\n{config_id}\r\n".encode()
    body += f"--{boundary}--\r\n".encode()
    req = urllib.request.Request(
        BASE + "/api/v1/documents", data=body, method="POST",
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}"})
    try:
        with urllib.request.urlopen(req) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        detail = e.read().decode(errors="replace")[:300]
        sys.exit(f"upload of {filename} failed: HTTP {e.code} {detail}")


def document_already_processed(docs, filename):
    for d in docs:
        if d.get("filename") == filename and d.get("status") == "processed" and d.get("deleted_at") is None:
            return d["id"]
    return None


def find_live_document(docs, filename):
    for d in docs:
        if d.get("filename") == filename and d.get("deleted_at") is None:
            return d["id"]
    return None


def reprocess_document(doc_id, config_id):
    req = urllib.request.Request(
        BASE + f"/api/v1/documents/{doc_id}/reprocess",
        data=json.dumps({"config_id": config_id}).encode(),
        method="POST", headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        detail = e.read().decode(errors="replace")[:300]
        sys.exit(f"reprocess of {doc_id} failed: HTTP {e.code} {detail}")


def main():
    payload = json.load(open(FIXTURE))
    configs = get("/api/v1/rag-configs")["configs"]
    config_id = ensure_config(configs)
    docs = get("/api/v1/documents")["documents"]
    name_to_id = {}

    for source in sorted({c["expected_source_name"] for c in payload["cases"]}):
        filename = source
        doc_id = document_already_processed(docs, filename)
        if not doc_id:
            src = os.path.join(CORPUS_DIR, filename)
            if not os.path.exists(src):
                sys.exit(f"corpus file missing: {src}")
            live = find_live_document(docs, filename)
            if live:
                # Same bytes already live (scroll a failed run): build a new
                # revision under the demo config through document_reindex.
                print(f"reprocessing {filename} -> new revision (config {config_id})")
                result = reprocess_document(live, config_id)
                doc = result.get("document") or result
                doc_id = doc["id"]
            else:
                print(f"uploading {filename} (real embedding ingestion; failures surface from the API)")
                result = upload_doc(src, config_id)
                doc = result.get("document") or result
                doc_id = doc["id"]
        deadline = time.time() + WAIT_SECONDS
        while True:
            d = get(f"/api/v1/documents/{doc_id}")
            status = d.get("status")
            if status == "processed":
                break
            if status == "failed":
                sys.exit(f"document {filename} failed; inspect GET /api/v1/documents/{doc_id}")
            if time.time() > deadline:
                sys.exit(f"document {filename} not processed within {WAIT_SECONDS}s (status={status})")
            time.sleep(5)
        name_to_id[filename] = doc_id
        print(f"document ready: {filename} -> {doc_id}")

    cases = []
    for c in payload["cases"]:
        src = c["expected_source_name"]
        case = {k: v for k, v in c.items() if k != "expected_source_name"}
        case["expected_evidence"] = [{"document_id": name_to_id[src], "label": src}]
        cases.append(case)

    existing = [d for d in get("/api/v1/eval-datasets")["datasets"] if d["name"] == DATASET_NAME]
    body = {"name": DATASET_NAME, "description": payload["description"], "cases": cases}
    if existing:
        # Versioned edit creates a new immutable version with resolved refs.
        created = post(f"/api/v1/eval-datasets/{existing[0]['id']}/cases", body)
        dataset = created
    else:
        dataset = post("/api/v1/eval-datasets", body)
    print(f"golden dataset: {dataset['id']} (latest_version={dataset['latest_version']})")


if __name__ == "__main__":
    main()
