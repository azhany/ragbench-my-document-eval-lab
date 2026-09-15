"""Real PostgreSQL integration; provider doubles never enter the running DAGs."""
import hashlib
import os
import tempfile
import unittest
import uuid
from pathlib import Path
from unittest.mock import patch

import psycopg2

from ragbench.content import IngestionError
from ragbench.pipeline import run_stage
from test_embeddings import ProviderDouble


@unittest.skipUnless(os.getenv("RAGBENCH_TEST_DATABASE_URL"), "RAGBENCH_TEST_DATABASE_URL is required")
class PipelineTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.environment = patch.dict(os.environ, {"DATABASE_URL": os.environ["RAGBENCH_TEST_DATABASE_URL"],
                                                  "UPLOAD_DIR": self.temp.name, "EMBEDDING_BATCH_SIZE": "1"})
        self.environment.start()
        self.conn = psycopg2.connect(os.environ["DATABASE_URL"])
        self.config, self.doc = str(uuid.uuid4()), str(uuid.uuid4())
        self.path = Path(self.temp.name) / "fixture.txt"
        self.path.write_text("Approval policy requires a reviewer. Keep source evidence.", encoding="utf-8")
        self.checksum = hashlib.sha256(self.path.read_bytes()).hexdigest()
        with self.conn, self.conn.cursor() as cur:
            cur.execute("""INSERT INTO rag_configs(id,name,chunk_size,chunk_overlap,retrieval_mode,top_k,
                prompt_version,model_profile,embedding_profile,embedding_provider,embedding_model,embedding_dimensions)
                VALUES(%s,%s,20,4,'vector',5,'v1','openai-gpt-4o-mini','openai-text-embedding-3-small',
                'openai','text-embedding-3-small',1536)""", (self.config, "test-" + self.config))
            cur.execute("""INSERT INTO documents(id,filename,mime_type,storage_path,checksum,size_bytes)
                VALUES(%s,'fixture.txt','text/plain',%s,%s,%s)""", (self.doc, str(self.path), self.checksum, self.path.stat().st_size))
        self.job, self.revision = self.new_revision(1)

    def tearDown(self):
        with self.conn, self.conn.cursor() as cur:
            cur.execute("DELETE FROM ingestion_jobs WHERE index_revision_id IN (SELECT id FROM index_revisions WHERE document_id=%s)", (self.doc,))
            cur.execute("UPDATE documents SET active_revision_id=NULL,latest_revision_id=NULL WHERE id=%s", (self.doc,))
            cur.execute("DELETE FROM documents WHERE id=%s", (self.doc,))
            cur.execute("DELETE FROM rag_configs WHERE id=%s", (self.config,))
        self.conn.close(); self.environment.stop(); self.temp.cleanup()

    def new_revision(self, number, chunk_size=20):
        job, revision = str(uuid.uuid4()), str(uuid.uuid4())
        with self.conn, self.conn.cursor() as cur:
            cur.execute("""INSERT INTO index_revisions(id,document_id,revision_number,source_checksum,chunk_size,
                chunk_overlap,embedding_provider,embedding_model,embedding_dimensions,config_id)
                VALUES(%s,%s,%s,%s,%s,4,'openai','text-embedding-3-small',1536,%s)""",
                        (revision, self.doc, number, self.checksum, chunk_size, self.config))
            cur.execute("INSERT INTO ingestion_jobs(id,index_revision_id,dag_id,run_id) VALUES(%s,%s,'document_ingestion',%s)",
                        (job, revision, "rb_" + revision))
            cur.execute("UPDATE documents SET latest_revision_id=%s,status='queued' WHERE id=%s", (revision, self.doc))
        return job, revision

    def read(self, sql, args=()):
        with self.conn, self.conn.cursor() as cur:
            cur.execute(sql, args)
            return cur.fetchall()

    def prepare(self, job=None):
        for stage in ("extract", "normalize", "chunk"):
            run_stage(job or self.job, stage)

    def publish(self, job=None):
        run_stage(job or self.job, "embed", embedder=ProviderDouble())
        run_stage(job or self.job, "publish")

    def test_retry_identity_partial_failure_atomic_publish_and_fts(self):
        self.prepare()
        before = self.read("SELECT id,content FROM doc_chunks WHERE index_revision_id=%s ORDER BY chunk_index", (self.revision,))
        self.prepare()
        self.assertEqual(before, self.read("SELECT id,content FROM doc_chunks WHERE index_revision_id=%s ORDER BY chunk_index", (self.revision,)))
        for failure in ("dimensions", "partial"):
            with self.assertRaises(IngestionError):
                run_stage(self.job, "embed", embedder=ProviderDouble(failure))
            self.assertEqual(self.read("SELECT active_revision_id,status FROM documents WHERE id=%s", (self.doc,)), [(None, "failed")])
            self.assertEqual(self.read("SELECT count(*) FROM searchable_chunks(%s) WHERE document_id=%s", (self.config, self.doc)), [(0,)])
            self.assertEqual(self.read("SELECT error_code FROM ingestion_jobs WHERE id=%s", (self.job,)), [("embedding_failed",)])
        self.publish()
        self.assertEqual(self.read("SELECT active_revision_id,status,chunk_count FROM documents WHERE id=%s", (self.doc,)), [(self.revision, "processed", len(before))])
        self.assertEqual(self.read("SELECT DISTINCT vector_dims(embedding) FROM searchable_chunks(%s) WHERE document_id=%s", (self.config, self.doc)), [(1536,)])
        self.assertTrue(self.read("SELECT id FROM searchable_chunks(%s) WHERE document_id=%s AND content_tsv @@ plainto_tsquery('simple','approval')", (self.config, self.doc)))
        run_stage(self.job, "chunk"); self.publish()
        self.assertEqual(before, self.read("SELECT id,content FROM doc_chunks WHERE index_revision_id=%s ORDER BY chunk_index", (self.revision,)))
        self.assertTrue(self.read("SELECT stage_metrics->'embed'->'prompt_tokens' FROM ingestion_jobs WHERE id=%s", (self.job,))[0][0])

    def test_incomplete_revision_cannot_publish(self):
        self.prepare()
        with self.assertRaises(IngestionError):
            run_stage(self.job, "publish")
        self.assertEqual(self.read("SELECT active_revision_id FROM documents WHERE id=%s", (self.doc,)), [(None,)])

    def test_failed_replacement_preserves_previous_and_stale_retry_cannot_replace(self):
        self.prepare(); self.publish()
        job2, rev2 = self.new_revision(2, 25)
        self.prepare(job2)
        with self.assertRaises(IngestionError):
            run_stage(job2, "embed", embedder=ProviderDouble("partial"))
        self.assertEqual(self.read("SELECT active_revision_id,status FROM documents WHERE id=%s", (self.doc,)), [(self.revision, "failed")])
        self.assertTrue(self.read("SELECT id FROM searchable_chunks(%s) WHERE document_id=%s", (self.config, self.doc)))
        job3, rev3 = self.new_revision(3, 30)
        self.prepare(job3); self.publish(job3)
        self.publish(job2)
        self.assertEqual(self.read("SELECT active_revision_id FROM documents WHERE id=%s", (self.doc,)), [(rev3,)])
        self.assertTrue(self.read("SELECT id FROM doc_chunks WHERE index_revision_id=%s", (self.revision,)))

    def test_delete_during_embedding_cannot_resurrect_or_erase_history(self):
        self.prepare(); self.publish()
        job2, rev2 = self.new_revision(2)
        self.prepare(job2)
        owner = self
        class DeleteDuringEmbed(ProviderDouble):
            def embed(self, texts, model, dimensions):
                with owner.conn, owner.conn.cursor() as cur:
                    cur.execute("UPDATE documents SET deleted_at=now() WHERE id=%s", (owner.doc,))
                    cur.execute("UPDATE ingestion_jobs SET state='cancelled' WHERE id=%s", (job2,))
                return super().embed(texts, model, dimensions)
        run_stage(job2, "embed", embedder=DeleteDuringEmbed())
        run_stage(job2, "publish")
        self.assertEqual(self.read("SELECT active_revision_id FROM documents WHERE id=%s", (self.doc,)), [(self.revision,)])
        self.assertEqual(self.read("SELECT count(*) FROM searchable_chunks(%s) WHERE document_id=%s", (self.config, self.doc)), [(0,)])
        self.assertTrue(self.read("SELECT id FROM doc_chunks WHERE index_revision_id=%s", (self.revision,)))

    def test_incompatible_profile_is_excluded(self):
        self.prepare(); self.publish()
        with self.conn, self.conn.cursor() as cur:
            cur.execute("UPDATE index_revisions SET embedding_model='incompatible-model' WHERE id=%s", (self.revision,))
        self.assertEqual(self.read("SELECT count(*) FROM searchable_chunks(%s) WHERE document_id=%s", (self.config, self.doc)), [(0,)])
