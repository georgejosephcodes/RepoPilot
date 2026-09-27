import pytest

from repopilot_worker import db


def test_connect_requires_database_url(monkeypatch):
    monkeypatch.delenv("DATABASE_URL", raising=False)
    with pytest.raises(RuntimeError, match="DATABASE_URL"):
        db.connect()


def test_embedding_dimension_matches_the_schema(conn):
    from repopilot_worker import db
    assert db.embedding_dimension(conn) == 2048


def test_hnsw_index_uses_halfvec_cosine_ops(conn):
    definition = conn.execute(
        "SELECT indexdef FROM pg_indexes WHERE indexname = 'chunks_embedding_hnsw'").fetchone()[0]
    assert "hnsw" in definition and "halfvec_cosine_ops" in definition
