import threading

import psycopg
import pytest

from repopilot_worker import jobs


def row(conn, sql, *params):
    return conn.execute(sql, params).fetchone()


def test_claim_empty_queue(conn):
    assert jobs.claim_next(conn) is None


def test_claim_sets_running_and_repo_indexing(conn, make_job):
    repo_id, job_id = make_job(owner="Octo", name="Hello")
    job = jobs.claim_next(conn)
    assert job == jobs.Job(id=job_id, repo_id=repo_id, url="https://github.com/Octo/Hello",
                           owner="Octo", name="Hello", attempts=1)
    status, locked, started = row(conn, "SELECT status, locked_at, started_at FROM index_jobs WHERE id=%s", job_id)
    assert status == "running" and locked is not None and started is not None
    assert row(conn, "SELECT status FROM repositories WHERE id=%s", repo_id)[0] == "indexing"
    assert jobs.claim_next(conn) is None  # nothing else queued


def test_claim_is_fifo(conn, make_job):
    ids = [make_job()[1] for _ in range(3)]
    assert [jobs.claim_next(conn).id for _ in range(3)] == ids


def test_concurrent_claims_never_share_a_job(test_dsn, conn, make_job):
    ids = {make_job()[1] for _ in range(8)}
    claimed, errors = [], []
    lock = threading.Lock()

    def worker():
        try:
            with psycopg.connect(test_dsn, autocommit=True) as c:
                j = jobs.claim_next(c)
            with lock:
                claimed.append(j.id if j else None)
        except Exception as e:  # pragma: no cover
            errors.append(e)

    threads = [threading.Thread(target=worker) for _ in range(8)]
    [t.start() for t in threads]
    [t.join() for t in threads]

    assert not errors
    assert sorted(claimed) == sorted(ids)  # every job taken exactly once


def test_claim_skips_rows_locked_by_another_transaction(test_dsn, conn, make_job):
    first = make_job()[1]
    second = make_job()[1]
    with psycopg.connect(test_dsn, autocommit=True) as other:
        with other.transaction():
            other.execute("SELECT id FROM index_jobs WHERE id = %s FOR UPDATE", (first,))
            job = jobs.claim_next(conn)  # must not block on `first`
    assert job.id == second


def test_set_progress_keeps_unset_counters(conn, make_job):
    _, job_id = make_job()
    jobs.claim_next(conn)
    jobs.set_progress(conn, job_id, "scanning", files_total=40)
    jobs.set_progress(conn, job_id, "embedding", files_done=12)
    phase, done, total = row(conn, "SELECT phase, files_done, files_total FROM index_jobs WHERE id=%s", job_id)
    assert (phase, done, total) == ("embedding", 12, 40)


def test_fail_marks_job_and_repo_and_truncates(conn, make_job):
    repo_id, job_id = make_job()
    jobs.claim_next(conn)
    jobs.fail(conn, job_id, "x" * 900)
    status, error, finished = row(conn, "SELECT status, error, finished_at FROM index_jobs WHERE id=%s", job_id)
    assert status == "failed" and len(error) == 500 and finished is not None
    assert row(conn, "SELECT status FROM repositories WHERE id=%s", repo_id)[0] == "failed"


def test_release_returns_job_to_queue_without_burning_attempt(conn, make_job):
    repo_id, job_id = make_job()
    jobs.claim_next(conn)
    jobs.set_progress(conn, job_id, "scanning", files_total=5)
    jobs.release(conn, job_id)
    status, attempts, phase, total, locked = row(
        conn, "SELECT status, attempts, phase, files_total, locked_at FROM index_jobs WHERE id=%s", job_id)
    assert (status, attempts, phase, total, locked) == ("queued", 0, None, None, None)
    assert row(conn, "SELECT status FROM repositories WHERE id=%s", repo_id)[0] == "queued"
    assert jobs.claim_next(conn).id == job_id  # claimable again


def test_requeue_stale(conn, make_job):
    repo_a, stale_ok = make_job()
    repo_b, stale_exhausted = make_job()
    repo_c, fresh = make_job()
    for _ in range(3):
        jobs.claim_next(conn)
    conn.execute("UPDATE index_jobs SET attempts = 3 WHERE id = %s", (stale_exhausted,))
    conn.execute("UPDATE index_jobs SET locked_at = now() - interval '1 hour' WHERE id IN (%s, %s)",
                 (stale_ok, stale_exhausted))

    assert jobs.requeue_stale(conn, older_than_seconds=600, max_attempts=3) == (1, 1)

    def state(j):
        return row(conn, "SELECT status, error FROM index_jobs WHERE id=%s", j)

    assert state(stale_ok) == ("queued", None)
    assert state(stale_exhausted) == ("failed", "worker stopped responding")
    assert state(fresh)[0] == "running"  # heartbeat was recent, left alone
    assert row(conn, "SELECT status FROM repositories WHERE id=%s", repo_a)[0] == "queued"
    assert row(conn, "SELECT status FROM repositories WHERE id=%s", repo_b)[0] == "failed"
    assert row(conn, "SELECT status FROM repositories WHERE id=%s", repo_c)[0] == "indexing"


def test_heartbeat_refreshes_lock(conn, make_job):
    _, job_id = make_job()
    jobs.claim_next(conn)
    conn.execute("UPDATE index_jobs SET locked_at = now() - interval '1 hour' WHERE id = %s", (job_id,))
    jobs.heartbeat(conn, job_id)
    assert jobs.requeue_stale(conn, 600, 3) == (0, 0)


def _give_repo_an_index(conn, repo_id):
    conn.execute(
        "INSERT INTO chunks (repo_id, commit_sha, file_path, language, kind, start_line, end_line, content, "
        "embedding, embed_model) VALUES (%s, 'c', 'a.py', 'python', 'window', 1, 1, 'x', "
        "('[1' || repeat(',0', 2047) || ']')::halfvec, 'm')", (repo_id,))


def test_failure_keeps_repository_ready_when_an_index_exists(conn, make_job):
    repo_id, job_id = make_job()
    _give_repo_an_index(conn, repo_id)
    jobs.claim_next(conn)
    jobs.fail(conn, job_id, "embedding service unavailable, try again later")
    assert row(conn, "SELECT status FROM index_jobs WHERE id=%s", job_id)[0] == "failed"
    assert row(conn, "SELECT status FROM repositories WHERE id=%s", repo_id)[0] == "ready"


def test_release_keeps_repository_ready_when_an_index_exists(conn, make_job):
    repo_id, job_id = make_job()
    _give_repo_an_index(conn, repo_id)
    jobs.claim_next(conn)
    jobs.release(conn, job_id)
    assert row(conn, "SELECT status FROM index_jobs WHERE id=%s", job_id)[0] == "queued"
    assert row(conn, "SELECT status FROM repositories WHERE id=%s", repo_id)[0] == "ready"


def test_exhausted_stale_job_keeps_repository_ready_when_an_index_exists(conn, make_job):
    repo_id, job_id = make_job()
    _give_repo_an_index(conn, repo_id)
    jobs.claim_next(conn)
    conn.execute("UPDATE index_jobs SET attempts = 3, locked_at = now() - interval '1 hour' WHERE id = %s", (job_id,))
    assert jobs.requeue_stale(conn, older_than_seconds=600, max_attempts=3) == (0, 1)
    assert row(conn, "SELECT status FROM index_jobs WHERE id=%s", job_id)[0] == "failed"
    assert row(conn, "SELECT status FROM repositories WHERE id=%s", repo_id)[0] == "ready"


def test_claim_keeps_repository_ready_while_reindexing(conn, make_job):
    repo_id, job_id = make_job()
    _give_repo_an_index(conn, repo_id)
    conn.execute("UPDATE repositories SET status = 'ready' WHERE id = %s", (repo_id,))
    jobs.claim_next(conn)
    assert row(conn, "SELECT status FROM index_jobs WHERE id=%s", job_id)[0] == "running"
    assert row(conn, "SELECT status FROM repositories WHERE id=%s", repo_id)[0] == "ready"
