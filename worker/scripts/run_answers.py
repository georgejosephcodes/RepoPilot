"""Ask every evaluation question through the running API in one retrieval mode, check the answers mechanically, and
compare the two modes.

    python3 worker/scripts/run_answers.py --mode hybrid_rerank --clones /tmp/demo --dry-run   # checks the setup only
    python3 worker/scripts/run_answers.py --mode hybrid_rerank --clones /tmp/demo             # asks the questions
    python3 worker/scripts/run_answers.py --report-only --clones /tmp/demo                    # rebuilds answers.md

The API must run in the mode given (RETRIEVAL_MODE); every answer's stats.retrieval_mode is checked, and the run stops
at the first answer from another mode. Question vectors and rerank rankings come from the caches filled by cmd/eval,
so a question costs one answer-model request. Answers are saved to docs/phase2/answers/<mode>.json after every question,
so a stopped run resumes without asking again.

The mechanical checks are run_demo.py's: citation snippets equal the real lines of the pinned clones, markers and
citations agree, the answer is not truncated, a labelled file is cited, and an unanswerable question is refused with
no citations. They never decide that an answer is correct; human verdicts are written separately. Standard library only.
"""

import argparse
import json
import math
import statistics
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from scripts import run_demo as demo  # noqa: E402

MODES = ("vector", "hybrid_rerank")
PACE_SECONDS = 9.0  # the API allows 7 questions per minute per client
LATENCY_GATE_MS = 2000  # gate: the median end-to-end time grows by at most 2 s over vector retrieval
SPOT_CHECKS = 10  # unchanged pairs given a human verdict, the first ones by id


# ---------------------------------------------------------------- questions

def load_items(data) -> list[dict]:
    """eval.json questions in run_demo's item shape. Expected files are the files of the labelled spans (any grade)."""
    commits = {r["name"]: r["commit"] for r in data["repositories"]}
    items = []
    for q in data["questions"]:
        files = []
        for span in q.get("relevant", []):
            if span["file"] not in files:
                files.append(span["file"])
        items.append({
            "id": q["id"], "repo": q["repo"], "kind": q["kind"], "split": q["split"], "question": q["question"],
            "expected_files": files, "expected_answer": q.get("expected_answer", ""), "commit": commits[q["repo"]],
        })
    return items


def mode_problem(body, mode: str) -> str | None:
    """Why an answer must not be stored under this mode, or None."""
    got = body.get("stats", {}).get("retrieval_mode") if isinstance(body, dict) else None
    if got != mode:
        return f"the API answered in retrieval mode {got!r}, not {mode!r}; restart it with RETRIEVAL_MODE={mode}"
    return None


def guarded(ask, mode: str):
    """Wraps an ask function so an answer from the wrong mode stops the run instead of being saved."""
    def inner(item):
        reply = ask(item)
        if reply.get("ok"):
            problem = mode_problem(reply["body"], mode)
            if problem:
                return {"ok": False, "message": problem}
        return reply
    return inner


# ---------------------------------------------------------------- comparison

def summarize(item: dict, rec: dict | None, root: Path) -> dict | None:
    if rec is None:
        return None
    chk = demo.check_response(item, rec["response"], root)
    body = rec["response"] if isinstance(rec["response"], dict) else {}
    return {"passed": chk["passed"], "problems": chk["problems"], "cited": sorted(chk["cited_files"]),
            "refused": body.get("refused") is True, "stats": body.get("stats", {}), "elapsed_ms": rec.get("elapsed_ms", 0)}


def changed(a: dict | None, b: dict | None) -> bool:
    """Two answers to one question differ in a way worth a human look: other files cited, refusal, or checks."""
    if a is None or b is None:
        return False
    return a["cited"] != b["cited"] or a["refused"] != b["refused"] or a["passed"] != b["passed"]


def judged_time_ms(s: dict) -> int:
    """End-to-end time counted for the latency gate: the measured time, plus the stored rerank time when the ranking
    came from the cache (so a cached ranking does not hide the rerank cost)."""
    extra = s["stats"].get("rerank_ms", 0) if s["stats"].get("rerank_cached") else 0
    return s["elapsed_ms"] + extra


def pct(values: list[int], p: float) -> int:
    v = sorted(values)
    return v[max(0, min(len(v) - 1, math.ceil(len(v) * p / 100) - 1))] if v else 0


def latency(vec: list[dict], hyb: list[dict]) -> dict | None:
    if not vec or not hyb:
        return None
    v = [s["elapsed_ms"] for s in vec]
    h = [judged_time_ms(s) for s in hyb]
    diff = statistics.median(h) - statistics.median(v)
    return {"n_vector": len(v), "n_hybrid": len(h), "vector_median": statistics.median(v), "vector_p90": pct(v, 90),
            "hybrid_median": statistics.median(h), "hybrid_p90": pct(h, 90), "diff": diff, "passed": diff <= LATENCY_GATE_MS,
            "rerank_cached": sum(1 for s in hyb if s["stats"].get("rerank_cached")),
            "rerank_fallbacks": sum(1 for s in hyb if s["stats"].get("rerank_fallback"))}


def to_judge(items: list[dict], vec: dict, hyb: dict) -> list[str]:
    """Question ids that get a human verdict: every changed pair, every answer failing a check, and the first
    SPOT_CHECKS unchanged pairs by id."""
    picked, unchanged = [], []
    for item in sorted(items, key=lambda i: i["id"]):
        a, b = vec.get(item["id"]), hyb.get(item["id"])
        if a is None or b is None:
            continue
        if changed(a, b) or not a["passed"] or not b["passed"]:
            picked.append(item["id"])
        else:
            unchanged.append(item["id"])
    return sorted(picked + unchanged[:SPOT_CHECKS])


# ---------------------------------------------------------------- report

def mark(s: dict | None) -> str:
    if s is None:
        return "not asked"
    return ("pass" if s["passed"] else "FAIL") + (" (refused)" if s["refused"] else "")


def render_report(items: list[dict], results: dict[str, dict], roots: dict[str, Path]) -> str:
    sums = {m: {i["id"]: summarize(i, results[m].get(i["id"]), roots[i["repo"]]) for i in items} for m in MODES}
    out = [
        "# End-to-end answers: vector against hybrid_rerank",
        "",
        "Generated by `worker/scripts/run_answers.py` from `answers/vector.json` and `answers/hybrid_rerank.json`. Every",
        "question of `eval.json` was asked through the running API once per retrieval mode. `vector` is Phase 1 retrieval",
        "(the same code, proven identical to the baseline); `hybrid_rerank` is the Phase 2 pipeline. The checks are",
        "mechanical: citation snippets equal the real lines, markers and citations agree, the answer is not truncated, a",
        "labelled file is cited, and unanswerable questions are refused with no citations. They do **not** judge whether an",
        "answer is correct; see `verdicts.md`.",
        "",
        "## Mechanical checks",
        "",
        "| group | vector passed | hybrid_rerank passed |",
        "|---|---|---|",
    ]
    groups = [("all", lambda i: True)]
    groups += [(f"split: {s}", (lambda s: lambda i: i["split"] == s)(s)) for s in ("dev", "test")]
    groups += [(f"kind: {k}", (lambda k: lambda i: i["kind"] == k)(k)) for k in sorted({i["kind"] for i in items})]
    for name, keep in groups:
        cells = []
        for m in MODES:
            asked = [sums[m][i["id"]] for i in items if keep(i) and sums[m][i["id"]] is not None]
            cells.append(f"{sum(1 for s in asked if s['passed'])} of {len(asked)}")
        out.append(f"| {name} | {cells[0]} | {cells[1]} |")

    lat = latency([s for s in sums["vector"].values() if s], [s for s in sums["hybrid_rerank"].values() if s])
    out += ["", "## Latency (gate item 5)", ""]
    if lat is None:
        out.append("Not available until both modes have answers.")
    else:
        out += [
            "Both modes used cached question vectors, so embedding time is near zero in both. For hybrid_rerank the time",
            "counted is the measured time plus the stored rerank time whenever the ranking came from the cache.",
            "",
            "| mode | n | median | p90 |",
            "|---|---|---|---|",
            f"| vector (measured) | {lat['n_vector']} | {lat['vector_median'] / 1000:.2f} s | {lat['vector_p90'] / 1000:.2f} s |",
            f"| hybrid_rerank (measured + cached rerank time) | {lat['n_hybrid']} | {lat['hybrid_median'] / 1000:.2f} s | {lat['hybrid_p90'] / 1000:.2f} s |",
            "",
            f"Median difference: **{lat['diff'] / 1000:+.2f} s** against a limit of +{LATENCY_GATE_MS / 1000:.1f} s: "
            f"**{'pass' if lat['passed'] else 'FAIL'}**. Rankings from the cache: {lat['rerank_cached']} of {lat['n_hybrid']}; "
            f"rerank fallbacks: {lat['rerank_fallbacks']}.",
        ]

    out += ["", "## Per question", "",
            "`changed` marks a pair whose cited files, refusal or check result differ between the modes.", "",
            "| id | split | kind | vector | hybrid_rerank | changed | vector cites | hybrid_rerank cites |",
            "|---|---|---|---|---|---|---|---|"]
    failures = []
    for item in sorted(items, key=lambda i: i["id"]):
        a, b = sums["vector"][item["id"]], sums["hybrid_rerank"][item["id"]]
        out.append("| " + " | ".join([
            item["id"], item["split"], item["kind"], mark(a), mark(b), "yes" if changed(a, b) else "",
            demo.cell(", ".join(a["cited"]) if a else "") or "(none)", demo.cell(", ".join(b["cited"]) if b else "") or "(none)",
        ]) + " |")
        for m, s in (("vector", a), ("hybrid_rerank", b)):
            if s is not None and not s["passed"]:
                failures.append(f"- **{item['id']}** ({m}, {item['kind']}): " + "; ".join(s["problems"]))
    judge = to_judge(items, {k: v for k, v in sums["vector"].items() if v}, {k: v for k, v in sums["hybrid_rerank"].items() if v})
    out += ["", "## Failed checks", "", *(failures or ["None."]), "",
            "## Given a human verdict", "",
            f"Every changed pair, every answer failing a check, and the first {SPOT_CHECKS} unchanged pairs by id "
            f"({len(judge)} questions): " + (", ".join(judge) or "none yet") + ".", ""]
    return "\n".join(out)


# ---------------------------------------------------------------- main

def main(argv=None) -> int:
    root_dir = Path(__file__).resolve().parents[2]
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("--mode", choices=MODES, help="the retrieval mode the API runs in (required unless --report-only)")
    p.add_argument("--api", default="http://localhost:8080")
    p.add_argument("--clones", required=True, type=Path, help="directory holding one clone per repository, named after the repository")
    p.add_argument("--questions", type=Path, default=root_dir / "docs/phase2/eval.json")
    p.add_argument("--out", type=Path, default=root_dir / "docs/phase2")
    p.add_argument("--only", help="ask only this question id")
    p.add_argument("--pace", type=float, default=PACE_SECONDS, help="seconds between questions")
    p.add_argument("--dry-run", action="store_true", help="check the setup and count what would be asked; spends nothing")
    p.add_argument("--report-only", action="store_true", help="rebuild answers.md from the saved answers without the API")
    args = p.parse_args(argv)
    if not args.report_only and not args.mode:
        p.error("--mode is required unless --report-only")

    data = json.loads(args.questions.read_text(encoding="utf-8"))
    items = load_items(data)
    answers_dir = args.out / "answers"
    results = {m: demo.load_results(answers_dir / f"{m}.json") for m in MODES}

    api_repos = None
    if not args.report_only:
        got = demo.http_json("GET", args.api + "/api/repositories")
        if not got["ok"]:
            print(f"cannot list repositories: {got['message']}", file=sys.stderr)
            return 2
        api_repos = got["body"]
    problems, ids, roots = demo.setup_problems(data, args.clones, api_repos)
    if problems:
        print("setup problems (nothing was asked):\n  " + "\n  ".join(problems), file=sys.stderr)
        return 2

    stopped = None
    if not args.report_only:
        todo = [i for i in items if not args.only or i["id"] == args.only]
        if not todo:
            print(f"no question with id {args.only}", file=sys.stderr)
            return 2
        mine = results[args.mode]
        pending = [i for i in todo if i["id"] not in mine]
        print(f"mode {args.mode}: {len(todo)} questions, {len(todo) - len(pending)} already answered, {len(pending)} to ask "
              f"(about {len(pending)} answer-model requests, {args.pace:g}s apart, about {len(pending) * args.pace / 60:.0f} min)")
        if args.dry_run:
            print("dry run: setup ok, nothing sent")
            return 0
        answers_dir.mkdir(parents=True, exist_ok=True)

        def ask(item):
            return demo.http_json("POST", f"{args.api}/api/repositories/{ids[item['repo']]}/query", {"question": item["question"]})

        stopped = demo.run_questions(todo, mine, guarded(ask, args.mode),
                                     lambda r: demo.save_results(answers_dir / f"{args.mode}.json", r, args.api), time.sleep, args.pace)
        if stopped:
            print(stopped, file=sys.stderr)

    report = args.out / "answers.md"
    args.out.mkdir(parents=True, exist_ok=True)
    report.write_text(render_report(items, results, roots), encoding="utf-8")
    for m in MODES:
        done = [i for i in items if i["id"] in results[m]]
        passed = sum(1 for i in done if demo.check_response(i, results[m][i["id"]]["response"], roots[i["repo"]])["passed"])
        print(f"{m}: {len(done)} of {len(items)} answered, {passed} passed every applicable check")
    print(f"wrote {report}")
    return 1 if stopped else 0


if __name__ == "__main__":
    sys.exit(main())
