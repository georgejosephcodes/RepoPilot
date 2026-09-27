"""Runs one indexing job: clone, scan, chunk, embed, store. Grows by one stage per build step."""

import logging
import math
import os
import tempfile
from collections import Counter
from dataclasses import dataclass
from pathlib import Path
from typing import Callable

import psycopg

from . import embed, jobs, scan as scan_mod, store
from .chunk import Chunk, chunk_file
from .clone import clone
from .config import Config
from .errors import UserError

log = logging.getLogger(__name__)

STAGES = ("scan", "chunk", "embed")  # valid values for stop_after; None runs everything including store


@dataclass
class Report:
    commit_sha: str
    files_by_language: dict[str, int]
    total_files: int
    skipped: dict[str, int]
    chunks_total: int = 0
    chunks_by_kind: dict[str, int] | None = None
    max_chunk_lines: int = 0
    embed_unique: int = 0      # distinct texts among the chunks
    embed_cached: int = 0      # of those, already in the cache
    embed_new: int = 0         # of those, embedded in this run
    embed_truncated: int = 0   # texts cut to the character limit
    stored: bool = False


CloneFn = Callable[[str, Path, int, float], str]


def process(
    conn: psycopg.Connection,
    job: jobs.Job,
    cfg: Config,
    stop_after: str | None = None,
    clone_fn: CloneFn = clone,
    embedder: "embed.Embedder | None" = None,
) -> Report | None:
    """Run `job` through the stages up to `stop_after` (all stages when None).

    Returns a Report on success, or None if the job failed (already marked failed).
    With stop_after set, the job is released back to the queue and nothing is stored.
    """
    if stop_after is not None and stop_after not in STAGES:
        raise ValueError(f"unknown stage {stop_after!r}")

    os.makedirs(cfg.work_dir, mode=0o700, exist_ok=True)
    try:
        chunks: list[Chunk] = []
        # TemporaryDirectory removes the clone on success, failure, and timeout.
        with tempfile.TemporaryDirectory(dir=cfg.work_dir, prefix="job-") as tmp:
            dest = Path(tmp) / "repo"

            jobs.set_progress(conn, job.id, "cloning")
            sha = clone_fn(job.url, dest, cfg.clone_timeout_s, cfg.max_repo_mb)

            jobs.set_progress(conn, job.id, "scanning")
            result = scan_mod.scan(dest, cfg.max_files, cfg.max_file_kb)
            if not result.files:
                raise UserError("no supported source files found")
            jobs.set_progress(conn, job.id, "scanning", files_done=0, files_total=len(result.files))

            report = Report(
                commit_sha=sha,
                files_by_language=result.by_language(),
                total_files=len(result.files),
                skipped=dict(result.skipped),
            )
            if stop_after != "scan":
                chunks = _chunk_stage(conn, job, cfg, result, report)
            report.skipped = dict(sorted(report.skipped.items()))

        # The clone is gone by now; embedding can take minutes and needs only the chunks in memory.
        if stop_after in (None, "embed"):
            embedder = embedder or embed.make_embedder(cfg)
            vectors = _embed_stage(conn, job, cfg, chunks, embedder, report)
            if stop_after is None:
                jobs.set_progress(conn, job.id, "storing")
                store.replace_chunks(conn, job.id, job.repo_id, report.commit_sha, chunks, vectors,
                                     embedder.model_name)
                report.stored = True
    except UserError as e:
        log.warning("job %s failed: %s", job.id, e)
        jobs.fail(conn, job.id, str(e))
        return None
    except Exception:
        log.exception("job %s crashed", job.id)
        jobs.fail(conn, job.id, "internal error while indexing")
        return None

    if stop_after is not None:
        jobs.release(conn, job.id)
    return report


def _chunk_stage(conn: psycopg.Connection, job: jobs.Job, cfg: Config, result: scan_mod.ScanResult,
                 report: Report) -> list[Chunk]:
    """Chunk every scanned file. A file that cannot be chunked is counted and skipped."""
    total = len(result.files)
    jobs.set_progress(conn, job.id, "chunking", files_done=0, files_total=total)
    chunks: list[Chunk] = []
    errors = 0
    for i, f in enumerate(result.files, 1):
        try:
            chunks.extend(chunk_file(
                f.rel_path, f.language, f.abs_path.read_bytes(),
                max_lines=cfg.chunk_max_lines, overlap=cfg.chunk_overlap_lines, min_gap=cfg.chunk_min_gap_lines,
            ))
        except Exception:
            log.exception("job %s: could not chunk %s", job.id, f.rel_path)
            errors += 1
        if i % 50 == 0 or i == total:
            jobs.set_progress(conn, job.id, "chunking", files_done=i)
    if errors:
        report.skipped["chunk_error"] = errors
    if not chunks:
        raise UserError("no indexable content found")

    by_kind: dict[str, int] = {}
    for c in chunks:
        by_kind[c.kind] = by_kind.get(c.kind, 0) + 1
    report.chunks_total = len(chunks)
    report.chunks_by_kind = dict(sorted(by_kind.items()))
    report.max_chunk_lines = max(c.end_line - c.start_line + 1 for c in chunks)
    return chunks


def _embed_stage(conn: psycopg.Connection, job: jobs.Job, cfg: Config, chunks: list[Chunk],
                 embedder: "embed.Embedder", report: Report) -> list[list[float]]:
    """Return one vector per chunk, reading the cache first and caching every batch as it arrives.

    In this phase the job's files_done and files_total count chunks, not files.
    """
    total = len(chunks)
    prepared = [embedder.prepare(c.embed_text) for c in chunks]
    report.embed_truncated = sum(1 for c, p in zip(chunks, prepared) if p != c.embed_text)
    hashes = [embed.sha256_hex(p) for p in prepared]
    text_of = dict(zip(hashes, prepared))          # unique texts, first occurrence order
    uses = Counter(hashes)                          # how many chunks share each text

    jobs.set_progress(conn, job.id, "embedding", files_done=0, files_total=total)
    vectors = store.cache_get(conn, embedder.model_name, list(text_of))
    misses = [h for h in text_of if h not in vectors]
    done = sum(uses[h] for h in vectors)
    report.embed_unique, report.embed_cached, report.embed_new = len(text_of), len(vectors), len(misses)
    log.info("job %s: %d unique texts, %d cached, %d to embed", job.id, len(text_of), len(vectors), len(misses))
    jobs.set_progress(conn, job.id, "embedding", files_done=done, files_total=total)

    if misses:
        needed = math.ceil(len(misses) / cfg.embed_batch_size)
        remaining = embedder.remaining_requests()
        if remaining is not None and needed + cfg.embed_reserve_requests > remaining:
            raise UserError(
                f"not enough free-tier requests left today: this repository needs {needed}, "
                f"{remaining} remain. Try again after the daily reset"
            )

        def on_batch(texts: list[str], vecs: list[list[float]]) -> None:
            nonlocal done
            items = [(embed.sha256_hex(t), v) for t, v in zip(texts, vecs)]
            store.cache_put(conn, embedder.model_name, items)   # kept even if the job fails later
            for h, v in items:
                vectors[h] = v
                done += uses[h]
            jobs.set_progress(conn, job.id, "embedding", files_done=done)

        embedder.embed_documents([text_of[h] for h in misses], on_batch=on_batch)

    return [vectors[h] for h in hashes]
