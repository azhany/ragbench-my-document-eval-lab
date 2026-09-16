"""Small real-provider interface; deterministic doubles belong only in tests."""
import json
import math
import os
from typing import Protocol
from urllib.error import HTTPError, URLError
from urllib.parse import quote
from urllib.request import Request, urlopen

from ragbench.content import IngestionError


class Embedder(Protocol):
    def embed(self, texts: list[str], model: str, dimensions: int) -> dict: ...


class OpenAICompatibleEmbedder:
    def __init__(self, provider="openai", base_url=None, api_key=None):
        self.provider = provider
        self.base_url = (base_url or os.getenv("OPENAI_BASE_URL") or "https://api.openai.com/v1").rstrip("/")
        self.api_key = api_key

    def embed(self, texts, model, dimensions):
        key = self.api_key or os.getenv("EMBEDDING_PROVIDER_API_KEY") or os.getenv("OPENAI_API_KEY")
        if not key:
            raise IngestionError("embedding_failed", "Embedding API key is not configured in Airflow")
        request = Request(self.base_url + "/embeddings", method="POST",
                          data=json.dumps({"input": texts, "model": model, "dimensions": dimensions,
                                           "encoding_format": "float"}).encode(),
                          headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json",
                                   "User-Agent": "ragbench-my/1.0"})
        try:
            with urlopen(request, timeout=45) as response:
                return json.load(response)
        except HTTPError as exc:
            raise IngestionError("embedding_failed", f"{self.provider} embeddings returned HTTP {exc.code}") from None
        except (URLError, TimeoutError, ValueError, OSError):
            raise IngestionError("embedding_failed", f"{self.provider} embeddings unavailable or response invalid") from None


class OpenAIEmbedder(OpenAICompatibleEmbedder):
    """Backwards-compatible name for the real OpenAI embedding integration."""


class HuggingFaceEmbedder:
    def __init__(self, base_url=None, api_key=None):
        self.base_url = (base_url or os.getenv("HUGGINGFACE_EMBEDDING_BASE_URL")
                         or "https://router.huggingface.co/hf-inference/models").rstrip("/")
        self.api_key = api_key

    def embed(self, texts, model, dimensions):
        key = (self.api_key or os.getenv("HF_TOKEN") or os.getenv("HUGGINGFACE_API_KEY")
               or os.getenv("EMBEDDING_PROVIDER_API_KEY"))
        if not key:
            raise IngestionError("embedding_failed", "Hugging Face embedding API key is not configured in Airflow")
        model_path = "/".join(quote(part, safe="") for part in model.split("/"))
        endpoint = f"{self.base_url}/{model_path}/pipeline/feature-extraction"
        request = Request(endpoint, method="POST",
                          data=json.dumps({"inputs": texts, "normalize": True}).encode(),
                          headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json",
                                   "User-Agent": "ragbench-my/1.0"})
        try:
            with urlopen(request, timeout=45) as response:
                vectors = json.load(response)
        except HTTPError as exc:
            raise IngestionError("embedding_failed", f"Hugging Face embeddings returned HTTP {exc.code}") from None
        except (URLError, TimeoutError, ValueError, OSError):
            raise IngestionError("embedding_failed", "Hugging Face embeddings unavailable or response invalid") from None
        # Normalize the native bare-vector response to the small internal
        # response contract shared with the OpenAI-compatible path.
        if not isinstance(vectors, list):
            raise IngestionError("embedding_failed", "Hugging Face embeddings response is not a vector array")
        return {"model": model,
                "data": [{"index": index, "embedding": vector} for index, vector in enumerate(vectors)]}


def runtime_embedder(provider):
    if provider == "openai":
        return OpenAIEmbedder()
    if provider == "huggingface":
        return HuggingFaceEmbedder()
    raise IngestionError("embedding_failed", f"Embedding provider {provider!r} is not configured")


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


SUPPORTED_PROFILES = {
    ("openai", "text-embedding-3-small", 1536),
    ("huggingface", "BAAI/bge-small-en-v1.5", 384),
}


def embed_chunks(chunks, provider, model, dimensions, embedder=None, batch_size=16):
    if (provider, model, dimensions) not in SUPPORTED_PROFILES:
        raise IngestionError("embedding_failed", "Persisted embedding profile is incompatible with this index")
    if not 1 <= batch_size <= 128:
        raise IngestionError("embedding_failed", "EMBEDDING_BATCH_SIZE must be between 1 and 128")
    embedder = embedder or runtime_embedder(provider)
    vectors, tokens, usage_known = [], 0, True
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
            if isinstance(count, int) and not isinstance(count, bool) and count >= 0 and usage_known:
                tokens += count
            else:
                usage_known = False
        except Exception as exc:
            detail = str(exc) if isinstance(exc, IngestionError) else f"Invalid provider response ({type(exc).__name__})"
            raise IngestionError("embedding_failed", f"Batch {start // batch_size + 1}, chunk {start}: {detail}") from None
    return {"vectors": vectors, "prompt_tokens": tokens if usage_known else None,
            "batch_size": batch_size, "chunk_count": len(chunks)}
