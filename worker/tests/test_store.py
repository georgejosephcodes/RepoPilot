import dataclasses

import psycopg
import pytest

from repopilot_worker import store
from repopilot_worker.chunk import chunk_file
from repopilot_worker.embed import FakeEmbedder, sha256_hex

MODEL = "fake-embed"


def unit(dim=2048, hot=0):
    v = [0.0] * dim
    v[hot] = 1.0
    return v


def sample_chunks():
    src = b"def a():\n    return 1\n\ndef b():\n    return 2\n"
    return chunk_file("m.py", "python", src)


def vectors_for(chunks):
    return FakeEmbedder().embed_documents([c.embed_text for c in chunks])


# ---------------------------------------------------------------- pure helpers (no database)

def test_vector_literal_round_trip_and_format():
    v = [0.1, -0.25, 1e-9, 3.0, 0.123456789]
    text = store.vector_literal(v)
    assert text.startswith("[") and text.endswith("]") and " " not in text
    back = store.parse_vector(text)
    assert len(back) == len(v)
    assert all(abs(a - b) < 1e-7 for a, b in zip(v, back))


def test_parse_vector_accepts_postgres_output():
    assert store.parse_vector("[1,2.5,-3]") == [1.0, 2.5, -3.0]
    assert store.parse_vector("[]") == []


# ---------------------------------------------------------------- embedding cache

def test_cache_round_trip_and_missing_hashes(conn):
    v = unit(hot=3)
    store.cache_put(conn, MODEL, [("h1", v)])
    got = store.cache_get(conn, MODEL, ["h1", "nope"])
    assert set(got) == {"h1"}
    assert len(got["h1"]) == 2048 and got["h1"][3] == pytest.approx(1.0, abs=1e-3)
    assert store.cache_get(conn, MODEL, []) == {}


def test_cache_put_is_idempotent_and_first_write_wins(conn):
    store.cache_put(conn, MODEL, [("h1", unit(hot=0))])
    store.cache_put(conn, MODEL, [("h1", unit(hot=1))])
    assert conn.execute("SELECT count(*) FROM embedding_cache").fetchone()[0] == 1
    assert store.cache_get(conn, MODEL, ["h1"])["h1"][0] == pytest.approx(1.0, abs=1e-3)


def test_cache_is_separated_by_model(conn):
    store.cache_put(conn, "model-a", [("h", unit())])
    assert store.cache_get(conn, "model-b", ["h"]) == {}
    assert set(store.cache_get(conn, "model-a", ["h"])) == {"h"}


def test_cache_many_rows(conn):
    items = [(sha256_hex(str(i)), unit(hot=i % 2048)) for i in range(300)]
    store.cache_put(conn, MODEL, items)
    got = store.cache_get(conn, MODEL, [h for h, _ in items])
    assert len(got) == 300


# ---------------------------------------------------------------- replace_chunks

def repo_and_job(make_job, conn, status="indexing"):
    repo_id, job_id = make_job()
    conn.execute("UPDATE repositories SET status = %s WHERE id = %s", (status, repo_id))
    conn.execute("UPDATE index_jobs SET status = 'running' WHERE id = %s", (job_id,))
    return repo_id, job_id


def chunk_count(conn, repo_id):
    return conn.execute("SELECT count(*) FROM chunks WHERE repo_id = %s", (repo_id,)).fetchone()[0]


def test_replace_chunks_stores_rows_and_finishes_job(conn, make_job):
    repo_id, job_id = repo_and_job(make_job, conn)
    chunks = sample_chunks()
    store.replace_chunks(conn, job_id, repo_id, "a" * 40, chunks, vectors_for(chunks), MODEL)

    assert chunk_count(conn, repo_id) == len(chunks) == 2
    row = conn.execute(
        "SELECT commit_sha, file_path, language, symbol, kind, start_line, end_line, content, embed_model, "
        "vector_dims(embedding) FROM chunks WHERE repo_id = %s ORDER BY start_line LIMIT 1", (repo_id,)).fetchone()
    assert row == ("a" * 40, "m.py", "python", "a", "function", 1, 2, "def a():\n    return 1", MODEL, 2048)

    assert conn.execute("SELECT status, commit_sha FROM repositories WHERE id = %s", (repo_id,)).fetchone() == ("ready", "a" * 40)
    status, finished = conn.execute("SELECT status, finished_at FROM index_jobs WHERE id = %s", (job_id,)).fetchone()
    assert status == "succeeded" and finished is not None


def test_storing_twice_gives_the_same_row_count(conn, make_job):
    repo_id, job_id = repo_and_job(make_job, conn)
    chunks = sample_chunks()
    vecs = vectors_for(chunks)
    store.replace_chunks(conn, job_id, repo_id, "a" * 40, chunks, vecs, MODEL)
    store.replace_chunks(conn, job_id, repo_id, "a" * 40, chunks, vecs, MODEL)
    assert chunk_count(conn, repo_id) == 2


def test_new_commit_replaces_all_old_rows(conn, make_job):
    repo_id, job_id = repo_and_job(make_job, conn)
    chunks = sample_chunks()
    store.replace_chunks(conn, job_id, repo_id, "a" * 40, chunks, vectors_for(chunks), MODEL)
    newer = chunk_file("n.py", "python", b"def only():\n    pass\n")
    store.replace_chunks(conn, job_id, repo_id, "b" * 40, newer, vectors_for(newer), MODEL)
    rows = conn.execute("SELECT DISTINCT commit_sha, file_path FROM chunks WHERE repo_id = %s", (repo_id,)).fetchall()
    assert rows == [("b" * 40, "n.py")]


def test_failure_midway_leaves_previous_rows_intact(conn, make_job):
    repo_id, job_id = repo_and_job(make_job, conn)
    old = sample_chunks()
    store.replace_chunks(conn, job_id, repo_id, "a" * 40, old, vectors_for(old), MODEL)

    conn.execute("UPDATE repositories SET status = 'indexing' WHERE id = %s", (repo_id,))
    conn.execute("UPDATE index_jobs SET status = 'running', finished_at = NULL WHERE id = %s", (job_id,))
    bad = [dataclasses.replace(c, start_line=0) for c in sample_chunks()]     # violates the CHECK constraint
    with pytest.raises(psycopg.errors.CheckViolation):
        store.replace_chunks(conn, job_id, repo_id, "b" * 40, bad, vectors_for(bad), MODEL)

    assert chunk_count(conn, repo_id) == 2
    assert conn.execute("SELECT DISTINCT commit_sha FROM chunks WHERE repo_id = %s", (repo_id,)).fetchall() == [("a" * 40,)]
    assert conn.execute("SELECT status FROM repositories WHERE id = %s", (repo_id,)).fetchone()[0] == "indexing"
    assert conn.execute("SELECT status FROM index_jobs WHERE id = %s", (job_id,)).fetchone()[0] == "running"


def test_replace_only_touches_its_own_repository(conn, make_job):
    repo_a, job_a = repo_and_job(make_job, conn)
    repo_b, job_b = repo_and_job(make_job, conn)
    chunks = sample_chunks()
    store.replace_chunks(conn, job_a, repo_a, "a" * 40, chunks, vectors_for(chunks), MODEL)
    store.replace_chunks(conn, job_b, repo_b, "b" * 40, chunks, vectors_for(chunks), MODEL)
    store.replace_chunks(conn, job_a, repo_a, "c" * 40, chunks[:1], vectors_for(chunks[:1]), MODEL)
    assert chunk_count(conn, repo_a) == 1 and chunk_count(conn, repo_b) == 2


def test_length_mismatch_is_rejected_before_touching_the_database(conn, make_job):
    repo_id, job_id = repo_and_job(make_job, conn)
    chunks = sample_chunks()
    with pytest.raises(ValueError):
        store.replace_chunks(conn, job_id, repo_id, "a" * 40, chunks, vectors_for(chunks)[:1], MODEL)


def test_nul_characters_do_not_abort_the_store(conn, make_job):
    repo_id, job_id = repo_and_job(make_job, conn)
    chunks = chunk_file("z.py", "python", b"x = 1\nname = 'a\x00b'\n")
    store.replace_chunks(conn, job_id, repo_id, "a" * 40, chunks, vectors_for(chunks), MODEL)
    content = conn.execute("SELECT content FROM chunks WHERE repo_id = %s", (repo_id,)).fetchone()[0]
    assert "\x00" not in content and "�" in content


def test_hnsw_search_works_on_stored_halfvec(conn, make_job):
    repo_id, job_id = repo_and_job(make_job, conn)
    chunks = sample_chunks()
    vecs = vectors_for(chunks)
    store.replace_chunks(conn, job_id, repo_id, "a" * 40, chunks, vecs, MODEL)
    nearest = conn.execute(
        "SELECT symbol FROM chunks WHERE repo_id = %s ORDER BY embedding <=> %s::halfvec LIMIT 1",
        (repo_id, store.vector_literal(vecs[1]))).fetchone()[0]
    assert nearest == "b"
