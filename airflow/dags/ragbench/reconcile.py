"""Read-only source-volume reconciliation after a crash or uncertain commit."""
import json
import os
from pathlib import Path

from ragbench.pipeline import connect


def main():
    conn = connect()
    try:
        with conn, conn.cursor() as cur:
            cur.execute("SELECT id,storage_path,deleted_at IS NOT NULL FROM documents")
            sources = cur.fetchall()
        referenced = {Path(path).resolve() for _, path, _ in sources}
        root = Path(os.getenv("UPLOAD_DIR", "/data/uploads"))
        findings = []
        for doc_id, path, deleted in sources:
            if not Path(path).is_file():
                findings.append({"issue": "missing_source", "document_id": doc_id, "path": path, "deleted": deleted})
        for path in root.iterdir():
            if path.is_file() and path.resolve() not in referenced:
                findings.append({"issue": "unreferenced_source", "path": str(path),
                                 "recovery": "Check the filename UUID against upload_persistence_uncertain logs and database state before removing bytes"})
        print(json.dumps({"referenced_sources": len(sources), "findings": findings}, indent=2))
        return 1 if findings else 0
    finally:
        conn.close()


if __name__ == "__main__":
    raise SystemExit(main())
