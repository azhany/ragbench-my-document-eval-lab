"""Small real-provider interface; deterministic doubles belong only in tests."""
import json
import math
import os
from typing import Protocol
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

from ragbench.content import IngestionError


class Embedder(Protocol):
    def embed(self, texts: list[str], model: str, dimensions: int) -> dict: ...


class OpenAIEmbedder:
    def embed(self, texts, model, dimensions):
        key = os.getenv("EMBEDDING_PROVIDER_API_KEY") or os.getenv("OPENAI_API_KEY")
        if not key:
            raise IngestionError("embedding_failed", "Embedding API key is not configured in Airflow")
        request = Request("https://api.openai.com/v1/embeddings", method="POST",
                          data=json.dumps({"input": texts, "model": model, "dimensions": dimensions,
                                           "encoding_format": "float"}).encode(),
                          headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"})
        try:
            with urlopen(request, timeout=45) as response:
                return json.load(response)
        except HTTPError as exc:
            raise IngestionError("embedding_failed", f"OpenAI embeddings returned HTTP {exc.code}") from None
        except (URLError, TimeoutError, ValueError, OSError):
            raise IngestionError("embedding_failed", "OpenAI embeddings unavailable or response invalid") from None


def validate_vectors(vectors, count, dimensions):
    if len(vectors) != count:
        raise IngestionError("embedding_failed", "Provider returned the wrong number of vectors")
    for vector in vectors:
        if not isinstance(vector, list) or len(vector) != dimensions:
            raise IngestionError("embedding_failed", f"Provider vector dimensions must equal {dimensions}")
        if any(isinstance(value, bool) or not isinstance(value, (float, int))
               or not math.isfinite(value) or abs(value) > 3.402823466e38 for value in vector):
            raise IngestionError("embedding_failed", "Provider returned invalid vector values")
        if not any(value != 0 for value in vector):
            raise IngestionError("embedding_failed", "Provider returned a zero vector, unusable for cosine search")


def embed_chunks(chunks, provider, model, dimensions, embedder, batch_size):
    if provider != "openai" or model != "text-embedding-3-small" or dimensions != 1536:
        raise IngestionError("embedding_failed", "Persisted embedding profile is incompatible with this index")
    if not 1 <= batch_size <= 128:
        raise IngestionError("embedding_failed", "EMBEDDING_BATCH_SIZE must be between 1 and 128")
    vectors, tokens = [], 0
    for start in range(0, len(chunks), batch_size):
        batch = chunks[start:start + batch_size]
        try:
            response = embedder.embed([item["content"] for item in batch], model, dimensions)
            if response["model"] != model:
                raise IngestionError("embedding_failed", "Provider response model differs from persisted profile")
            data = sorted(response["data"], key=lambda item: item["index"])
            if [item["index"] for item in data] != list(range(len(batch))):
                raise IngestionError("embedding_failed", "Provider response indexes are missing or duplicated")
            batch_vectors = [item["embedding"] for item in data]
            validate_vectors(batch_vectors, len(batch), dimensions)
            vectors.extend(batch_vectors)
            count = response.get("usage", {}).get("prompt_tokens")
            tokens = tokens + count if tokens is not None and isinstance(count, int) and count >= 0 else None
        except Exception as exc:
            detail = str(exc) if isinstance(exc, IngestionError) else f"Invalid provider response ({type(exc).__name__})"
            raise IngestionError("embedding_failed", f"Batch {start // batch_size + 1}, chunk {start}: {detail}") from None
    return {"vectors": vectors, "prompt_tokens": tokens, "batch_size": batch_size, "chunk_count": len(chunks)}
