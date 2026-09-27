import os
from pathlib import Path

import pytest

from repopilot_worker import jobs, pipeline
from repopilot_worker.clone import CloneError
from repopilot_worker.config import Config


def cfg(tmp_path, **kw):
    return Config(work_dir=tmp_path / "work", **kw)


def fake_clone_ok(url, dest, timeout_s, max_mb):
    (dest / "pkg").mkdir(parents=True)
    (dest / "main.py").write_text("print(1)\n")
    (dest / "pkg" / "a.go").write_text("package pkg\n")
    (dest / "node_modules").mkdir()
    (dest / "node_modules" / "x.js").write_text("x")
    return "c" * 40


def work_dir_empty(path):
    return not path.exists() or not any(path.iterdir())


def test_process_success_stop_after_scan(conn, make_job, tmp_path):
    repo_id, job_id = make_job()
    job = jobs.claim_next(conn)
    c = cfg(tmp_path)

    report = pipeline.process(conn, job, c, stop_after="scan", clone_fn=fake_clone_ok)

    assert report.commit_sha == "c" * 40
    assert report.files_by_language == {"go": 1, "python": 1}
    assert report.total_files == 2
    assert report.skipped == {"ignored_dir": 1}
    assert work_dir_empty(c.work_dir)  # clone removed
    status, attempts = conn.execute("SELECT status, attempts FROM index_jobs WHERE id=%s", (job_id,)).fetchone()
    assert (status, attempts) == ("queued", 0)  # released, attempt not burned
    assert conn.execute("SELECT status FROM repositories WHERE id=%s", (repo_id,)).fetchone()[0] == "queued"


def test_process_records_progress_before_release(conn, make_job, tmp_path):
    _, job_id = make_job()
    job = jobs.claim_next(conn)
    seen = []
    real = jobs.set_progress

    def spy(c, jid, phase, files_done=None, files_total=None):
        seen.append((phase, files_done, files_total))
        real(c, jid, phase, files_done, files_total)

    orig, jobs.set_progress = jobs.set_progress, spy
    try:
        pipeline.process(conn, job, cfg(tmp_path), stop_after="scan", clone_fn=fake_clone_ok)
    finally:
        jobs.set_progress = orig
    assert seen == [("cloning", None, None), ("scanning", None, None), ("scanning", 0, 2)]


def test_process_clone_error_fails_job_and_cleans_up(conn, make_job, tmp_path):
    repo_id, job_id = make_job()
    job = jobs.claim_next(conn)
    c = cfg(tmp_path)

    def bad_clone(url, dest, timeout_s, max_mb):
        dest.mkdir(parents=True)
        (dest / "partial.py").write_text("x")
        raise CloneError("repository not found or not public")

    assert pipeline.process(conn, job, c, stop_after="scan", clone_fn=bad_clone) is None
    status, error = conn.execute("SELECT status, error FROM index_jobs WHERE id=%s", (job_id,)).fetchone()
    assert (status, error) == ("failed", "repository not found or not public")
    assert conn.execute("SELECT status FROM repositories WHERE id=%s", (repo_id,)).fetchone()[0] == "failed"
    assert work_dir_empty(c.work_dir)


def test_process_unexpected_error_hides_details(conn, make_job, tmp_path):
    _, job_id = make_job()
    job = jobs.claim_next(conn)

    def crash(url, dest, timeout_s, max_mb):
        raise RuntimeError("/secret/path exploded")

    assert pipeline.process(conn, job, cfg(tmp_path), stop_after="scan", clone_fn=crash) is None
    error = conn.execute("SELECT error FROM index_jobs WHERE id=%s", (job_id,)).fetchone()[0]
    assert error == "internal error while indexing"
    assert "secret" not in error


def test_process_no_supported_files_fails_with_clear_message(conn, make_job, tmp_path):
    _, job_id = make_job()
    job = jobs.claim_next(conn)

    def only_binary(url, dest, timeout_s, max_mb):
        dest.mkdir(parents=True)
        (dest / "logo.png").write_bytes(b"\x89PNG")
        return "d" * 40

    assert pipeline.process(conn, job, cfg(tmp_path), stop_after="scan", clone_fn=only_binary) is None
    error = conn.execute("SELECT error FROM index_jobs WHERE id=%s", (job_id,)).fetchone()[0]
    assert error == "no supported source files found"


def test_process_rejects_unknown_stage(conn, make_job, tmp_path):
    make_job()
    job = jobs.claim_next(conn)
    with pytest.raises(ValueError):
        pipeline.process(conn, job, cfg(tmp_path), stop_after="parse", clone_fn=fake_clone_ok)


# ---------------------------------------------------------------- chunk stage

def fake_clone_mixed(url, dest, timeout_s, max_mb):
    (dest / "pkg").mkdir(parents=True)
    (dest / "main.py").write_text("def hello():\n    return 1\n\ndef bye():\n    return 2\n")
    (dest / "pkg" / "a.go").write_text("package pkg\n\nfunc A() {}\n\nfunc B() {}\n")
    (dest / "README.md").write_text("# Title\n\ntext\n\n## Usage\n\nrun it\n")
    return "e" * 40


def test_chunk_stage_reports_counts_and_releases_job(conn, make_job, tmp_path):
    repo_id, job_id = make_job()
    job = jobs.claim_next(conn)
    c = cfg(tmp_path)
    seen = []
    real = jobs.set_progress

    def spy(cn, jid, phase, files_done=None, files_total=None):
        seen.append((phase, files_done, files_total))
        real(cn, jid, phase, files_done, files_total)

    orig, jobs.set_progress = jobs.set_progress, spy
    try:
        report = pipeline.process(conn, job, c, stop_after="chunk", clone_fn=fake_clone_mixed)
    finally:
        jobs.set_progress = orig

    assert report.total_files == 3
    assert report.chunks_total == 6
    assert report.chunks_by_kind == {"doc": 2, "function": 4}
    assert report.max_chunk_lines == 3     # the README sections are 3 lines each
    assert ("chunking", 0, 3) in seen and ("chunking", 3, None) in seen
    assert work_dir_empty(c.work_dir)
    status, attempts = conn.execute("SELECT status, attempts FROM index_jobs WHERE id=%s", (job_id,)).fetchone()
    assert (status, attempts) == ("queued", 0)


def test_chunk_error_in_one_file_does_not_fail_the_job(conn, make_job, tmp_path, monkeypatch):
    make_job()
    job = jobs.claim_next(conn)
    real = pipeline.chunk_file

    def flaky(rel_path, *a, **k):
        if rel_path == "main.py":
            raise RuntimeError("boom")
        return real(rel_path, *a, **k)

    monkeypatch.setattr(pipeline, "chunk_file", flaky)
    report = pipeline.process(conn, job, cfg(tmp_path), stop_after="chunk", clone_fn=fake_clone_mixed)
    assert report is not None
    assert report.skipped == {"chunk_error": 1}
    assert report.chunks_total == 4        # a.go (2 functions) + README (2 sections)


def test_repository_with_no_indexable_content_fails(conn, make_job, tmp_path):
    repo_id, job_id = make_job()
    job = jobs.claim_next(conn)

    def blank_files(url, dest, timeout_s, max_mb):
        dest.mkdir(parents=True)
        (dest / "main.py").write_text("\n\n   \n")
        (dest / "empty.go").write_text("")
        return "f" * 40

    assert pipeline.process(conn, job, cfg(tmp_path), stop_after="chunk", clone_fn=blank_files) is None
    status, error = conn.execute("SELECT status, error FROM index_jobs WHERE id=%s", (job_id,)).fetchone()
    assert (status, error) == ("failed", "no indexable content found")
    assert conn.execute("SELECT status FROM repositories WHERE id=%s", (repo_id,)).fetchone()[0] == "failed"


# ---------------------------------------------------------------- embed stage and store

from repopilot_worker.embed import EmbedError, FakeEmbedder, sha256_hex


def full_run(conn, job, tmp_path, embedder, clone_fn=fake_clone_mixed, stop_after=None, **cfg_kw):
    return pipeline.process(conn, job, cfg(tmp_path, **cfg_kw), stop_after=stop_after, clone_fn=clone_fn, embedder=embedder)


def new_job(conn, repo_id):
    conn.execute("INSERT INTO index_jobs (repo_id) VALUES (%s)", (repo_id,))
    return jobs.claim_next(conn)


def count(conn, table, repo_id=None):
    if repo_id is None:
        return conn.execute(f"SELECT count(*) FROM {table}").fetchone()[0]
    return conn.execute(f"SELECT count(*) FROM {table} WHERE repo_id = %s", (repo_id,)).fetchone()[0]


def test_full_pipeline_stores_chunks_and_finishes(conn, make_job, tmp_path):
    repo_id, job_id = make_job()
    job = jobs.claim_next(conn)
    c = cfg(tmp_path)
    embedder = FakeEmbedder()

    report = pipeline.process(conn, job, c, clone_fn=fake_clone_mixed, embedder=embedder)

    assert report.stored and report.chunks_total == 6
    assert (report.embed_unique, report.embed_cached, report.embed_new) == (6, 0, 6)
    assert count(conn, "chunks", repo_id) == 6 and count(conn, "embedding_cache") == 6
    assert conn.execute("SELECT status, commit_sha FROM repositories WHERE id = %s", (repo_id,)).fetchone() == ("ready", "e" * 40)
    assert conn.execute("SELECT status FROM index_jobs WHERE id = %s", (job_id,)).fetchone()[0] == "succeeded"
    models = conn.execute("SELECT DISTINCT embed_model FROM chunks WHERE repo_id = %s", (repo_id,)).fetchall()
    assert models == [("fake-embed",)]
    assert conn.execute("SELECT min(vector_dims(embedding)), max(vector_dims(embedding)) FROM chunks").fetchone() == (2048, 2048)
    assert work_dir_empty(c.work_dir)


def test_rerun_makes_no_embedding_requests_and_does_not_duplicate(conn, make_job, tmp_path):
    repo_id, _ = make_job()
    first = jobs.claim_next(conn)
    full_run(conn, first, tmp_path, FakeEmbedder())

    second = new_job(conn, repo_id)
    quiet = FakeEmbedder()
    report = full_run(conn, second, tmp_path, quiet)

    assert quiet.calls == []                                   # everything came from the cache
    assert (report.embed_cached, report.embed_new) == (6, 0)
    assert count(conn, "chunks", repo_id) == 6                 # replaced, not duplicated
    assert conn.execute("SELECT status FROM index_jobs WHERE id = %s", (second.id,)).fetchone()[0] == "succeeded"


def test_embed_error_fails_job_and_keeps_previous_index(conn, make_job, tmp_path):
    repo_id, _ = make_job()
    full_run(conn, jobs.claim_next(conn), tmp_path, FakeEmbedder())

    def changed_clone(url, dest, timeout_s, max_mb):
        dest.mkdir(parents=True)
        (dest / "other.py").write_text("def other():\n    return 99\n")
        return "9" * 40

    job = new_job(conn, repo_id)
    result = full_run(conn, job, tmp_path, FakeEmbedder(fail_on_batch=0), clone_fn=changed_clone)

    assert result is None
    status, error = conn.execute("SELECT status, error FROM index_jobs WHERE id = %s", (job.id,)).fetchone()
    assert (status, error) == ("failed", "fake embedding failure")
    # the repository keeps its usable index, so it stays ready; the failure is on the job
    assert conn.execute("SELECT status FROM repositories WHERE id = %s", (repo_id,)).fetchone()[0] == "ready"
    assert conn.execute("SELECT DISTINCT commit_sha FROM chunks WHERE repo_id = %s", (repo_id,)).fetchall() == [("e" * 40,)]
    assert count(conn, "chunks", repo_id) == 6                 # old index untouched


def test_failed_run_resumes_from_the_cache(conn, make_job, tmp_path):
    repo_id, _ = make_job()
    job = jobs.claim_next(conn)
    assert full_run(conn, job, tmp_path, FakeEmbedder(batch_size=2, fail_on_batch=1)) is None
    assert count(conn, "embedding_cache") == 2                 # the first batch survived the failure
    assert count(conn, "chunks", repo_id) == 0

    retry = new_job(conn, repo_id)
    embedder = FakeEmbedder(batch_size=2)
    report = full_run(conn, retry, tmp_path, embedder)
    assert report.stored and (report.embed_cached, report.embed_new) == (2, 4)
    assert sum(len(t) for _, t in embedder.calls) == 4         # only the remaining texts were sent
    assert count(conn, "chunks", repo_id) == 6


def test_stage_embed_fills_cache_stores_nothing_and_releases_job(conn, make_job, tmp_path):
    repo_id, job_id = make_job()
    job = jobs.claim_next(conn)
    report = full_run(conn, job, tmp_path, FakeEmbedder(), stop_after="embed")
    assert not report.stored and report.embed_new == 6
    assert count(conn, "chunks", repo_id) == 0 and count(conn, "embedding_cache") == 6
    assert conn.execute("SELECT status, attempts FROM index_jobs WHERE id = %s", (job_id,)).fetchone() == ("queued", 0)
    assert conn.execute("SELECT status FROM repositories WHERE id = %s", (repo_id,)).fetchone()[0] == "queued"


def test_preflight_fails_before_any_request_when_budget_is_short(conn, make_job, tmp_path):
    repo_id, job_id = make_job()
    job = jobs.claim_next(conn)
    embedder = FakeEmbedder(remaining=5)                        # needs 1 request + 10 reserve > 5
    assert full_run(conn, job, tmp_path, embedder) is None
    error = conn.execute("SELECT error FROM index_jobs WHERE id = %s", (job_id,)).fetchone()[0]
    assert error.startswith("not enough free-tier requests left today")
    assert "needs 1, 5 remain" in error
    assert embedder.calls == [] and count(conn, "embedding_cache") == 0


def test_preflight_passes_with_enough_budget_or_unknown_budget(conn, make_job, tmp_path):
    make_job()
    assert full_run(conn, jobs.claim_next(conn), tmp_path, FakeEmbedder(remaining=11)) is not None
    repo_id, _ = make_job()
    assert full_run(conn, jobs.claim_next(conn), tmp_path, FakeEmbedder(remaining=None)) is not None


def test_preflight_is_skipped_when_everything_is_cached(conn, make_job, tmp_path):
    repo_id, _ = make_job()
    full_run(conn, jobs.claim_next(conn), tmp_path, FakeEmbedder())
    job = new_job(conn, repo_id)
    assert full_run(conn, job, tmp_path, FakeEmbedder(remaining=0)) is not None     # nothing to request


def test_missing_embedding_configuration_fails_the_job_safely(conn, make_job, tmp_path):
    _, job_id = make_job()
    job = jobs.claim_next(conn)
    assert pipeline.process(conn, job, cfg(tmp_path), clone_fn=fake_clone_mixed) is None      # no key, no embedder
    error = conn.execute("SELECT error FROM index_jobs WHERE id = %s", (job_id,)).fetchone()[0]
    assert error == "embedding is not configured: set EMBED_API_KEY"


def test_identical_texts_across_repositories_are_embedded_once(conn, make_job, tmp_path):
    make_job()
    full_run(conn, jobs.claim_next(conn), tmp_path, FakeEmbedder())
    make_job()
    second = FakeEmbedder()
    report = full_run(conn, jobs.claim_next(conn), tmp_path, second)
    assert second.calls == [] and report.embed_cached == 6


def test_embedding_progress_counts_chunks(conn, make_job, tmp_path):
    _, job_id = make_job()
    job = jobs.claim_next(conn)
    seen = []
    real = jobs.set_progress

    def spy(cn, jid, phase, files_done=None, files_total=None):
        if phase == "embedding":
            seen.append((files_done, files_total))
        real(cn, jid, phase, files_done, files_total)

    orig, jobs.set_progress = jobs.set_progress, spy
    try:
        full_run(conn, job, tmp_path, FakeEmbedder(batch_size=2))
    finally:
        jobs.set_progress = orig
    assert seen[0] == (0, 6) and seen[-1] == (6, None) and (2, None) in seen and (4, None) in seen


def test_truncated_texts_are_counted_and_hashed_as_sent(conn, make_job, tmp_path):
    _, _ = make_job()
    job = jobs.claim_next(conn)
    embedder = FakeEmbedder(max_chars=60)
    report = full_run(conn, job, tmp_path, embedder)
    assert report.embed_truncated > 0
    sent = [t for _, batch in embedder.calls for t in batch]
    assert all(len(t) <= 60 for t in sent)
    cached = {h for (h,) in conn.execute("SELECT text_hash FROM embedding_cache").fetchall()}
    assert cached == {sha256_hex(t) for t in sent}
