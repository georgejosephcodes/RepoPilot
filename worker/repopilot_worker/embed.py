"""Turn text into vectors through an OpenAI-compatible embeddings API.

Facts this code is built on (probed 2026-09-27):
- Without `input_type` the model behaves like `search_query`, so documents must ask for
  `search_document` explicitly.
- One request can carry hundreds of inputs; the free tier limits requests, not items.
- The model rejects inputs over its token limit with a loud 422 instead of truncating.
- Nothing in an error message may contain the API key or the URL.
"""

import hashlib
import http.client
import json
import logging
import math
import random
import time
import urllib.error
import urllib.request
from collections import deque
from typing import Callable, Protocol

from .errors import UserError

log = logging.getLogger(__name__)

MAX_ATTEMPTS = 6
BACKOFF_SECONDS = (1, 2, 4, 8, 16)   # waits between the 6 attempts
MAX_WAIT_S = 60.0
_RETRY_STATUSES = frozenset({429, 500, 502, 503, 504})
_TOO_LARGE_STATUSES = frozenset({413, 422})
_STATUS_MESSAGES = {
    400: "embedding request rejected",
    401: "embedding API key rejected or expired",
    403: "embedding API key rejected or expired",
    404: "embedding model not found",
}

Vector = list[float]
Post = Callable[[str, "dict | None", dict], "tuple[int, dict, dict]"]


class EmbedError(UserError):
    """The message is safe to show users: no key, no URL, no raw response."""


class _TooLarge(Exception):
    """The provider rejected a request as too large or too long (HTTP 413 or 422)."""


class Embedder(Protocol):
    model_name: str
    dimension: int

    def prepare(self, text: str) -> str: ...
    def embed_documents(self, texts: list[str], on_batch: "Callable[[list[str], list[Vector]], None] | None" = None) -> list[Vector]: ...
    def embed_query(self, text: str) -> Vector: ...
    def remaining_requests(self) -> "int | None": ...


# ---------------------------------------------------------------- helpers

def sha256_hex(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def estimate_tokens(text: str) -> int:
    """Pessimistic: 2 characters per token (measured 2.1 to 3.3 on real code)."""
    return max(1, math.ceil(len(text) / 2.0))


def normalize(vec: Vector) -> Vector:
    if not all(math.isfinite(x) for x in vec):
        raise EmbedError("embedding service returned an invalid vector")
    norm = math.sqrt(sum(x * x for x in vec))
    if norm == 0:
        raise EmbedError("embedding service returned an invalid vector")
    return [x / norm for x in vec]


def _snippet(data) -> str:
    try:
        return json.dumps(data)[:300]
    except (TypeError, ValueError):
        return "<unprintable>"


def _urllib_request(url: str, body: "dict | None", headers: dict, timeout: float = 60.0) -> "tuple[int, dict, dict]":
    """POST when `body` is given, otherwise GET. Network errors propagate as OSError."""
    data = json.dumps(body).encode("utf-8") if body is not None else None
    req = urllib.request.Request(url, data=data, headers=headers, method="POST" if body is not None else "GET")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            status, raw, resp_headers = resp.status, resp.read(), dict(resp.headers)
    except urllib.error.HTTPError as e:
        status, raw, resp_headers = e.code, e.read(), dict(e.headers)
    try:
        parsed = json.loads(raw)
    except ValueError:
        parsed = {"_raw": raw[:300].decode("utf-8", "replace")}
    if not isinstance(parsed, dict):
        parsed = {"_raw": str(parsed)[:300]}
    return status, parsed, resp_headers


class Pacer:
    """Keeps requests under a per-minute limit with a rolling 60-second window."""

    def __init__(self, rpm: int, clock=time.monotonic, sleep=time.sleep, safety: float = 0.9):
        self.limit = max(1, int(rpm * safety))
        self._clock, self._sleep = clock, sleep
        self._events: deque[float] = deque()

    def wait(self) -> None:
        while True:
            now = self._clock()
            while self._events and now - self._events[0] >= 60:
                self._events.popleft()
            if len(self._events) < self.limit:
                self._events.append(now)
                return
            self._sleep(self._events[0] + 60 - now)


# ---------------------------------------------------------------- the adapter

class OpenAICompatEmbedder:
    def __init__(
        self,
        base_url: str,
        api_key: str,
        model: str,
        dimension: int,
        *,
        send_dimensions: bool = False,
        input_types: bool = True,
        batch_size: int = 64,
        batch_tokens: int = 40000,
        max_chars: int = 8000,
        rpm: int = 20,
        post: Post = _urllib_request,
        sleep=time.sleep,
        clock=time.monotonic,
        jitter=random.random,
    ):
        self._url = base_url.rstrip("/") + "/embeddings"
        self._key_url = base_url.rstrip("/") + "/key"
        self._headers = {"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"}
        self.model_name = model
        self.dimension = dimension
        self._send_dimensions = send_dimensions
        self._input_types = input_types
        self._batch_size = max(1, batch_size)
        self._batch_tokens = max(1, batch_tokens)
        self._max_chars = max(1, max_chars)
        self._post, self._sleep, self._jitter = post, sleep, jitter
        self._pacer = Pacer(rpm, clock=clock, sleep=sleep)

    # -- public API

    def prepare(self, text: str) -> str:
        """The exact text that will be embedded. Cut to the character limit."""
        return text if len(text) <= self._max_chars else text[: self._max_chars]

    def embed_documents(self, texts: list[str], on_batch=None) -> list[Vector]:
        """Embed texts as documents. `on_batch(texts, vectors)` fires after each successful request."""
        prepared = [self.prepare(t) for t in texts]
        cut = sum(1 for a, b in zip(texts, prepared) if a != b)
        if cut:
            log.warning("%d of %d texts were cut to %d characters before embedding", cut, len(texts), self._max_chars)
        unique = list(dict.fromkeys(prepared))
        out: dict[str, Vector] = {}
        for batch in self._batches(unique):
            self._embed_batch(batch, "search_document", out, on_batch)
        return [out[p] for p in prepared]

    def embed_query(self, text: str) -> Vector:
        out: dict[str, Vector] = {}
        prepared = self.prepare(text)
        self._embed_batch([prepared], "search_query", out, None)
        return out[prepared]

    def remaining_requests(self) -> "int | None":
        """Requests left today on a free key, or None when unknown or not applicable."""
        try:
            status, data, _ = self._post(self._key_url, None, self._headers)
        except (OSError, http.client.HTTPException) as e:
            log.warning("could not read the remaining request budget: %s", type(e).__name__)
            return None
        if status != 200:
            return None
        info = ((data or {}).get("data") or {}).get("free_model_daily_requests")
        if isinstance(info, dict) and isinstance(info.get("remaining"), int):
            return info["remaining"]
        return None

    # -- internals

    def _batches(self, texts: list[str]):
        batch: list[str] = []
        tokens = 0
        for text in texts:
            est = estimate_tokens(text)
            if batch and (len(batch) >= self._batch_size or tokens + est > self._batch_tokens):
                yield batch
                batch, tokens = [], 0
            batch.append(text)
            tokens += est
        if batch:
            yield batch

    def _embed_batch(self, batch: list[str], input_type: str, out: dict, on_batch) -> None:
        try:
            vectors = self._request_vectors(batch, input_type)
        except _TooLarge:
            if len(batch) == 1:
                raise EmbedError("embedding input rejected") from None
            log.warning("batch of %d rejected as too large; splitting in half", len(batch))
            mid = len(batch) // 2
            self._embed_batch(batch[:mid], input_type, out, on_batch)
            self._embed_batch(batch[mid:], input_type, out, on_batch)
            return
        for text, vec in zip(batch, vectors):
            out[text] = vec
        if on_batch is not None:
            on_batch(batch, vectors)

    def _request_vectors(self, batch: list[str], input_type: str) -> list[Vector]:
        body: dict = {"model": self.model_name, "input": batch}
        if self._input_types:
            body["input_type"] = input_type
        if self._send_dimensions:
            body["dimensions"] = self.dimension
        return self._parse(self._send(body), len(batch))

    def _send(self, body: dict) -> dict:
        for attempt in range(MAX_ATTEMPTS):
            self._pacer.wait()
            try:
                status, data, resp_headers = self._post(self._url, body, self._headers)
            except (OSError, http.client.HTTPException) as e:
                log.warning("embedding request failed (attempt %d): %s", attempt + 1, type(e).__name__)
                status, data, resp_headers = None, {}, {}

            if status == 200:
                return data
            if status is None or status in _RETRY_STATUSES:
                if status is not None:
                    log.warning("embedding request got HTTP %s (attempt %d): %s", status, attempt + 1, _snippet(data))
                if attempt < MAX_ATTEMPTS - 1:
                    self._sleep(self._wait_seconds(attempt, resp_headers))
                continue
            log.warning("embedding request failed: HTTP %s %s", status, _snippet(data))
            if status in _TOO_LARGE_STATUSES:
                raise _TooLarge()
            raise EmbedError(_STATUS_MESSAGES.get(status, "embedding service error"))
        raise EmbedError("embedding service unavailable, try again later")

    def _wait_seconds(self, attempt: int, resp_headers: dict) -> float:
        for name, value in resp_headers.items():
            if name.lower() == "retry-after":
                try:
                    return min(max(float(value), 0.0), MAX_WAIT_S)
                except ValueError:
                    break
        base = BACKOFF_SECONDS[min(attempt, len(BACKOFF_SECONDS) - 1)]
        return min(MAX_WAIT_S, base * (1 + 0.25 * self._jitter()))

    def _parse(self, data: dict, expected: int) -> list[Vector]:
        items = data.get("data")
        if not isinstance(items, list) or len(items) != expected:
            raise EmbedError("embedding service returned an unexpected response")
        indexed: dict[int, Vector] = {}
        for position, item in enumerate(items):
            index = item.get("index", position) if isinstance(item, dict) else None
            vec = item.get("embedding") if isinstance(item, dict) else None
            if not isinstance(index, int) or index in indexed or not 0 <= index < expected:
                raise EmbedError("embedding service returned an unexpected response")
            if not isinstance(vec, list) or len(vec) != self.dimension:
                raise EmbedError("embedding service returned a vector of the wrong size")
            indexed[index] = normalize([float(x) for x in vec])
        return [indexed[i] for i in range(expected)]


# ---------------------------------------------------------------- test double and factory

class FakeEmbedder:
    """Deterministic unit vectors, no network. `calls` records every batch."""

    def __init__(self, dimension: int = 2048, model_name: str = "fake-embed", batch_size: int = 64,
                 remaining: "int | None" = None, fail_on_batch: "int | None" = None, max_chars: int = 8000):
        self.dimension = dimension
        self.model_name = model_name
        self.calls: list[tuple[str, list[str]]] = []
        self._batch_size = batch_size
        self._remaining = remaining
        self._fail_on_batch = fail_on_batch
        self._max_chars = max_chars

    def prepare(self, text: str) -> str:
        return text[: self._max_chars]

    def _vector(self, text: str) -> Vector:
        rng = random.Random(int.from_bytes(hashlib.sha256(text.encode("utf-8")).digest()[:8], "big"))
        return normalize([rng.gauss(0, 1) for _ in range(self.dimension)])

    def embed_documents(self, texts, on_batch=None):
        prepared = [self.prepare(t) for t in texts]
        unique = list(dict.fromkeys(prepared))
        out: dict[str, Vector] = {}
        for number, start in enumerate(range(0, len(unique), self._batch_size)):
            batch = unique[start:start + self._batch_size]
            if self._fail_on_batch is not None and number == self._fail_on_batch:
                raise EmbedError("fake embedding failure")
            self.calls.append(("search_document", list(batch)))
            vectors = [self._vector(t) for t in batch]
            out.update(zip(batch, vectors))
            if on_batch is not None:
                on_batch(batch, vectors)
        return [out[p] for p in prepared]

    def embed_query(self, text):
        self.calls.append(("search_query", [text]))
        return self._vector(self.prepare(text))

    def remaining_requests(self):
        return self._remaining


def make_embedder(cfg) -> OpenAICompatEmbedder:
    if not cfg.embed_api_key:
        raise EmbedError("embedding is not configured: set EMBED_API_KEY")
    return OpenAICompatEmbedder(
        cfg.embed_base_url, cfg.embed_api_key, cfg.embed_model, cfg.embed_dim,
        send_dimensions=cfg.embed_send_dimensions, input_types=cfg.embed_input_types,
        batch_size=cfg.embed_batch_size, batch_tokens=cfg.embed_batch_tokens,
        max_chars=cfg.embed_max_chars, rpm=cfg.embed_rpm,
    )
