-- Rerank results, so the same question over the same candidates never calls the reranker twice.
-- This makes evaluation re-runs free and repeatable (a cached ranking is frozen, while the model may vary).
-- cache_key is sha256 hex of the reranker name (prompt version + model), the trimmed question, and every
-- candidate's chunk id and content hash, in order (api/internal/rerank.Key).
-- ranking holds 0-based candidate indexes, most relevant first; it may list only some candidates.
-- took_ms is how long the upstream call took, so a run served from the cache still reports real latency.
-- Idempotent: safe to apply to a database that already has it.
CREATE TABLE IF NOT EXISTS rerank_cache (
    cache_key  TEXT        PRIMARY KEY,
    ranking    INT[]       NOT NULL,
    model      TEXT        NOT NULL,
    took_ms    BIGINT      NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used  TIMESTAMPTZ NOT NULL DEFAULT now()
);
