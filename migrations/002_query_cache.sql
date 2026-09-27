-- Question vectors, kept across API restarts, so a repeated question costs no embedding request.
-- A separate table from embedding_cache on purpose: questions are embedded with input_type search_query and
-- chunks with search_document, so the same text gives different vectors and must never share a cache key.
-- Idempotent: safe to apply to a database that already has it.
CREATE TABLE IF NOT EXISTS query_embedding_cache (
    embed_model TEXT          NOT NULL,
    text_hash   TEXT          NOT NULL,       -- sha256 hex of the exact text sent (trimmed, cut to EMBED_MAX_CHARS)
    embedding   halfvec(2048) NOT NULL,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    last_used   TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (embed_model, text_hash)
);
