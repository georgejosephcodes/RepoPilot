import argparse
import logging
import sys
import time

from . import config, db, jobs, pipeline

POLL_SECONDS = 2.0


def _check_db() -> int:
    with db.connect() as conn:
        info = db.check(conn)
    print(f"connected: {info['server']}")
    print(f"pgvector: {info['pgvector']}")
    print(f"tables: {', '.join(info['tables'])}")
    if info["pgvector"] is None:
        print("error: pgvector extension missing", file=sys.stderr)
        return 1
    return 0


def _startup_checks(conn, cfg, needs_embedding: bool) -> str | None:
    """Return an error message when the worker must not run, else None."""
    if needs_embedding and not cfg.embed_api_key:
        return "EMBED_API_KEY is not set"
    if needs_embedding:
        dim = db.embedding_dimension(conn)
        if dim != cfg.embed_dim:
            return f"EMBED_DIM is {cfg.embed_dim} but the database column is {dim}; fix .env or the schema"
    return None


def _print_report(job, report, stop_after) -> None:
    if report is None:
        print(f"job {job.id}: failed (see the repository status and error via the API)")
        return
    langs = ", ".join(f"{k} {v}" for k, v in report.files_by_language.items())
    skipped = ", ".join(f"{k} {v}" for k, v in report.skipped.items()) or "none"
    print(f"commit: {report.commit_sha}")
    print(f"files: {report.total_files} ({langs})")
    print(f"skipped: {skipped}")
    if report.chunks_total:
        kinds = ", ".join(f"{k} {v}" for k, v in (report.chunks_by_kind or {}).items())
        print(f"chunks: {report.chunks_total} ({kinds}); longest {report.max_chunk_lines} lines")
    if report.embed_unique:
        print(f"embeddings: {report.embed_unique} distinct texts, {report.embed_cached} from cache, "
              f"{report.embed_new} embedded now, {report.embed_truncated} cut to the character limit")
    if report.stored:
        print(f"job {job.id}: stored {report.chunks_total} chunks, repository is ready")
    else:
        print(f"stopped after '{stop_after}'; job released back to queued, nothing stored")


def _run(once: bool, stop_after: str | None) -> int:
    cfg = config.load()
    needs_embedding = stop_after in (None, "embed")
    with db.connect() as conn:
        problem = _startup_checks(conn, cfg, needs_embedding)
        if problem:
            print(f"error: {problem}", file=sys.stderr)
            return 2
        try:
            while True:
                requeued, failed = jobs.requeue_stale(conn, cfg.stale_after_s, cfg.max_attempts)
                if requeued or failed:
                    print(f"recovered stale jobs: {requeued} requeued, {failed} failed")

                job = jobs.claim_next(conn)
                if job is None:
                    if once:
                        print("no queued jobs")
                        return 0
                    time.sleep(POLL_SECONDS)
                    continue

                print(f"job {job.id}: {job.owner}/{job.name} (attempt {job.attempts})")
                report = pipeline.process(conn, job, cfg, stop_after=stop_after)
                _print_report(job, report, stop_after)
                if once:
                    return 0 if report is not None else 1
        except KeyboardInterrupt:
            print("stopped")   # a running job is recovered later by requeue_stale
            return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="repopilot_worker")
    parser.add_argument("--check", action="store_true", help="print database facts and exit")
    parser.add_argument("--once", action="store_true", help="claim and process one job, then exit")
    parser.add_argument("--stop-after", choices=pipeline.STAGES,
                        help="stop after this stage, release the job back to the queue, store nothing")
    args = parser.parse_args(argv)

    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")

    if args.check:
        return _check_db()
    return _run(args.once, args.stop_after)


if __name__ == "__main__":
    sys.exit(main())
