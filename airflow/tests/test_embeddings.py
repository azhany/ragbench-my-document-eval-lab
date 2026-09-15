import unittest
from ragbench.content import IngestionError
from ragbench.embeddings import embed_chunks, validate_vectors


class ProviderDouble:
    def __init__(self, failure=None):
        self.calls = 0
        self.failure = failure

    def embed(self, texts, model, dimensions):
        self.calls += 1
        if self.failure == "partial" and self.calls == 2:
            raise IngestionError("embedding_failed", "controlled HTTP 429")
        vector = [0.1] * (dimensions - 1 if self.failure == "dimensions" else dimensions)
        return {"model": model, "data": [{"index": i, "embedding": vector} for i in reversed(range(len(texts)))],
                "usage": {"prompt_tokens": len(texts)}}


class EmbeddingTests(unittest.TestCase):
    def test_order_count_dimensions_and_partial_failure(self):
        chunks = [{"content": "a"}, {"content": "b"}]
        result = embed_chunks(chunks, "openai", "text-embedding-3-small", 1536, ProviderDouble(), 1)
        self.assertEqual(len(result["vectors"]), 2)
        self.assertEqual(result["prompt_tokens"], 2)
        for failure in ("partial", "dimensions"):
            with self.assertRaises(IngestionError) as caught:
                embed_chunks(chunks, "openai", "text-embedding-3-small", 1536, ProviderDouble(failure), 1)
            self.assertEqual(caught.exception.code, "embedding_failed")
            self.assertIn("Batch", str(caught.exception))

    def test_rejects_unsearchable_vectors(self):
        for vector in ([0, 0], [float("nan"), 1], [float("inf"), 1], [True, 1], [1e39, 1], [1]):
            with self.assertRaises(IngestionError):
                validate_vectors([vector], 1, 2)


if __name__ == "__main__":
    unittest.main()
