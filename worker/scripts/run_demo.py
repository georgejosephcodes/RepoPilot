"""Ask the Phase 1 demo questions through the running API and check the answers mechanically.

    worker/.venv/bin/python worker/scripts/run_demo.py --clones /tmp/demo --dry-run   # free: checks the setup only
    worker/.venv/bin/python worker/scripts/run_demo.py --clones /tmp/demo             # asks the questions

Each question costs one embedding request on the free OpenRouter tier (50 per day). Answers are saved to
docs/phase1/raw.json after every question, so a stopped run resumes without asking again, and
docs/phase1/checks.md is generated from them. `--report-only` rebuilds checks.md from raw.json without the API.

The checks are mechanical and never decide that an answer is *correct*: they prove that citations point at the real
lines, that markers and citations agree, that an expected file was cited, and that unanswerable questions were
refused with no citations. A human verdict on the answer text is written separately. Standard library only.
"""

import argparse
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

PACE_SECONDS = 7.0  # the API allows 10 questions per minute per client
REQUEST_TIMEOUT = 120
ANSWERABLE = {"location", "flow", "identifier"}
KINDS = ["location", "flow", "unanswerable", "identifier"]
CHECK_NAMES = ["snippets_match", "markers_consistent", "not_truncated", "expected_file_cited", "not_refused", "grounded", "refusal_correct"]


# ---------------------------------------------------------------- questions

def validate_questions(data) -> list[str]:
    """Problems in the question file; an empty list means it is usable."""
    problems = []
    seen = set()
    repos = data.get("repositories") if isinstance(data, dict) else None
    if not isinstance(repos, list) or not repos:
        return ["no repositories"]
    for repo in repos:
        name = repo.get("name", "?")
        commit = repo.get("commit", "")
        if not re.fullmatch(r"[0-9a-f]{40}", commit):
            problems.append(f"{name}: commit must be 40 hex characters")
        for q in repo.get("questions", []):
            qid = q.get("id", "?")
            if qid in seen:
                problems.append(f"{qid}: duplicate id")
            seen.add(qid)
            if q.get("kind") not in KINDS:
                problems.append(f"{qid}: unknown kind {q.get('kind')!r}")
            if not q.get("question"):
                problems.append(f"{qid}: empty question")
            expected = q.get("expected_files")
            if not isinstance(expected, list):
                problems.append(f"{qid}: expected_files must be a list")
            elif q.get("kind") == "unanswerable" and expected:
                problems.append(f"{qid}: an unanswerable question must expect no files")
            elif q.get("kind") in ANSWERABLE and not expected:
                problems.append(f"{qid}: an answerable question needs expected files")
    return problems


def flatten(data) -> list[dict]:
    items = []
    for repo in data["repositories"]:
        for q in repo["questions"]:
            items.append({**q, "repo": repo["name"], "language": repo.get("language", ""), "commit": repo["commit"]})
    return items


def find_repo(repos: list[dict], name: str) -> dict | None:
    for r in repos:
        if f"{r.get('owner')}/{r.get('name')}".lower() == name.lower():
            return r
    return None


def same_commit(a: str | None, b: str | None) -> bool:
    if not a or not b or min(len(a), len(b)) < 7:
        return False
    return a.startswith(b) or b.startswith(a)


# ---------------------------------------------------------------- checks

def lines_of(path: Path) -> list[str]:
    text = path.read_bytes().decode("utf-8", "replace").replace("\r\n", "\n")
    lines = text.split("\n")
    if lines and lines[-1] == "":
        lines.pop()
    return lines


def citation_problems(citations: list[dict], root: Path) -> list[str]:
    """Every snippet must equal the real file lines start_line..end_line, and the file must be inside the repository."""
    problems = []
    base = root.resolve()
    for c in citations:
        label = f"[{c.get('n')}] {c.get('file')}:{c.get('start_line')}-{c.get('end_line')}"
        start, end = c.get("start_line"), c.get("end_line")
        if not isinstance(start, int) or not isinstance(end, int) or start < 1 or end < start:
            problems.append(f"{label}: bad line range")
            continue
        path = (base / str(c.get("file", ""))).resolve()
        if base not in path.parents:
            problems.append(f"{label}: path is outside the repository")
        elif not path.is_file():
            problems.append(f"{label}: file not found in the clone")
        elif "\n".join(lines_of(path)[start - 1:end]) != c.get("snippet"):
            problems.append(f"{label}: snippet differs from the real lines")
    return problems


def marker_problems(answer: str, citations: list[dict]) -> list[str]:
    """Every [n] in the answer needs a citation and every citation must be used. A chain such as [7][8] counts as
    several markers; a marker glued to an identifier (items[1]) is not a marker."""
    cited = {c.get("n") for c in citations}
    in_text = set()
    for chain in re.findall(r"(?<![A-Za-z0-9_\]])(?:\[\d+\])+", answer):
        in_text.update(int(n) for n in re.findall(r"\[(\d+)\]", chain))
    problems = [f"marker [{n}] has no citation" for n in sorted(in_text - cited)]
    problems += [f"citation [{n}] is not used in the answer" for n in sorted(n for n in cited - in_text if n is not None)]
    return problems


def valid_body(body) -> bool:
    return (
        isinstance(body, dict)
        and isinstance(body.get("answer"), str)
        and isinstance(body.get("citations"), list)
        and all(isinstance(c, dict) for c in body["citations"])
    )


def check_response(item: dict, body, root: Path) -> dict:
    """Mechanical checks for one answer. A check that does not apply to the question kind is None."""
    checks = {name: None for name in CHECK_NAMES}
    if not valid_body(body):
        return {"checks": checks, "problems": ["the response is not a query answer"], "passed": False, "cited_files": []}

    citations = body["citations"]
    problems = citation_problems(citations, root)
    checks["snippets_match"] = not problems
    marker = marker_problems(body["answer"], citations)
    problems += marker
    checks["markers_consistent"] = not marker
    checks["not_truncated"] = body.get("truncated") is not True
    if not checks["not_truncated"]:
        problems.append("the answer was cut off at the output limit")

    cited_files = []
    for c in citations:
        if c.get("file") not in cited_files:
            cited_files.append(c.get("file"))

    if item["kind"] == "unanswerable":
        checks["refusal_correct"] = body.get("refused") is True and not citations
        if not checks["refusal_correct"]:
            problems.append("expected a refusal with no citations")
    else:
        checks["expected_file_cited"] = any(f in item["expected_files"] for f in cited_files)
        checks["not_refused"] = body.get("refused") is not True
        checks["grounded"] = body.get("grounded") is True
        if not checks["expected_file_cited"]:
            problems.append("no cited file is in the expected files")
        if not checks["not_refused"]:
            problems.append("the answer is a refusal")
        if not checks["grounded"]:
            problems.append("the answer has no verified citations")

    return {"checks": checks, "problems": problems, "passed": all(v for v in checks.values() if v is not None), "cited_files": cited_files}


# ---------------------------------------------------------------- report

def cell(text) -> str:
    return str(text).replace("|", "\\|").replace("\n", " ").strip()


def mark(value) -> str:
    return "-" if value is None else ("pass" if value else "FAIL")


def render_report(items: list[dict], results: dict, roots: dict[str, Path]) -> str:
    rows, failures, by_kind = [], [], {k: [0, 0] for k in KINDS}
    for item in items:
        rec = results.get(item["id"])
        if rec is None:
            rows.append(f"| {item['id']} | {item['kind']} | {cell(item['question'])} | not asked | | | | | | | | |")
            continue
        chk = check_response(item, rec["response"], roots[item["repo"]])
        c = chk["checks"]
        stats = rec["response"].get("stats", {}) if isinstance(rec["response"], dict) else {}
        by_kind[item["kind"]][1] += 1
        by_kind[item["kind"]][0] += 1 if chk["passed"] else 0
        rows.append(
            "| " + " | ".join([
                item["id"], item["kind"], cell(item["question"]),
                cell(", ".join(item["expected_files"]) or "(none: refusal expected)"),
                cell(", ".join(chk["cited_files"]) or "(none)"),
                mark(c["snippets_match"]), mark(c["markers_consistent"]), mark(c["expected_file_cited"]),
                mark(c["refusal_correct"] if item["kind"] == "unanswerable" else c["not_refused"]),
                mark(c["grounded"]), f"{rec.get('elapsed_ms', 0) / 1000:.1f}s",
                f"{stats.get('input_tokens', 0)}/{stats.get('output_tokens', 0)}",
            ]) + " |"
        )
        if not chk["passed"]:
            failures.append(f"- **{item['id']}** ({item['kind']}): " + "; ".join(chk["problems"]))
    total_ok = sum(v[0] for v in by_kind.values())
    total = sum(v[1] for v in by_kind.values())
    out = [
        "# Phase 1 mechanical checks",
        "",
        "Generated by `worker/scripts/run_demo.py` from `raw.json`. These checks prove that citations point at the real lines, that",
        "answer markers and citations agree, that an expected file was cited, and that unanswerable questions were refused with no",
        "citations. They do **not** judge whether an answer is correct; human verdicts are in `results.md`.",
        "",
        f"Questions asked: {total} of {len(items)}. Passed every applicable check: {total_ok} of {total}.",
        "",
        "| kind | passed | asked |",
        "|---|---|---|",
        *[f"| {k} | {v[0]} | {v[1]} |" for k, v in by_kind.items()],
        "",
        "| id | kind | question | expected files | cited files | snippets | markers | expected file cited | not refused / refused | grounded | time | tokens in/out |",
        "|---|---|---|---|---|---|---|---|---|---|---|---|",
        *rows,
        "",
        "## Failed checks",
        "",
        *(failures or ["None."]),
        "",
    ]
    return "\n".join(out)


# ---------------------------------------------------------------- storage

def load_results(path: Path) -> dict:
    if not path.is_file():
        return {}
    data = json.loads(path.read_text(encoding="utf-8"))
    return data.get("results", {}) if isinstance(data, dict) else {}


def save_results(path: Path, results: dict, api: str) -> None:
    tmp = path.with_suffix(".tmp")
    tmp.write_text(json.dumps({"version": 1, "api": api, "results": results}, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    os.replace(tmp, path)


# ---------------------------------------------------------------- running

def run_questions(items, results, ask, save, sleep, pace=PACE_SECONDS, log=print) -> str | None:
    """Ask every question that has no saved answer. Returns None when done, or a message when it stopped early.
    `ask(item)` returns {"ok": True, "body": ..., "elapsed_ms": ...} or {"ok": False, "message": ...}."""
    asked_before = False
    for item in items:
        if item["id"] in results:
            log(f"skip {item['id']}: already answered")
            continue
        if asked_before:
            sleep(pace)
        asked_before = True
        reply = ask(item)
        if not reply.get("ok"):
            return f"stopped at {item['id']}: {reply.get('message', 'unknown error')}. Answers so far are saved; run again to resume."
        results[item["id"]] = {
            "repo": item["repo"],
            "kind": item["kind"],
            "question": item["question"],
            "asked_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
            "elapsed_ms": reply.get("elapsed_ms", 0),
            "response": reply["body"],
        }
        save(results)
        log(f"asked {item['id']} ({item['kind']}): {len(reply['body'].get('citations', [])) if isinstance(reply['body'], dict) else '?'} citations")
    return None


def http_json(method: str, url: str, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method=method, headers={"Content-Type": "application/json", "Accept": "application/json"})
    start = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=REQUEST_TIMEOUT) as res:
            payload, status = res.read(), res.status
    except urllib.error.HTTPError as e:
        payload, status = e.read(), e.code
    except (urllib.error.URLError, TimeoutError, ConnectionError) as e:
        return {"ok": False, "status": 0, "message": f"cannot reach the API ({type(e).__name__})"}
    elapsed = int((time.monotonic() - start) * 1000)
    try:
        parsed = json.loads(payload)
    except ValueError:
        return {"ok": False, "status": status, "message": f"non-JSON response (HTTP {status})"}
    if status != 200:
        err = parsed.get("error", {}) if isinstance(parsed, dict) else {}
        return {"ok": False, "status": status, "message": f"{err.get('code', 'error')}: {err.get('message', 'HTTP ' + str(status))}"}
    return {"ok": True, "status": status, "body": parsed, "elapsed_ms": elapsed}


def git_head(directory: Path) -> str | None:
    try:
        out = subprocess.run(["git", "-C", str(directory), "rev-parse", "HEAD"], capture_output=True, text=True, timeout=20)
    except (OSError, subprocess.TimeoutExpired):
        return None
    return out.stdout.strip() if out.returncode == 0 else None


def setup_problems(data, clones: Path, api_repos: list[dict] | None) -> tuple[list[str], dict[str, int], dict[str, Path]]:
    """Check every repository before any question is asked: clone present at the pinned commit; when the API list is
    given, the repository is indexed, ready, and indexed at that same commit (otherwise citations would be checked
    against the wrong lines)."""
    problems, ids, roots = [], {}, {}
    for repo in data["repositories"]:
        name = repo["name"]
        root = clones / name.split("/")[-1]
        roots[name] = root
        head = git_head(root)
        if head is None:
            problems.append(f"{name}: no git clone at {root}")
        elif not same_commit(head, repo["commit"]):
            problems.append(f"{name}: clone is at {head[:7]} but the questions pin {repo['commit'][:7]} (git -C {root} checkout {repo['commit'][:7]})")
        if api_repos is not None:
            r = find_repo(api_repos, name)
            if r is None:
                problems.append(f"{name}: not indexed (use 'add' in the UI)")
                continue
            ids[name] = r["id"]
            if r.get("status") != "ready":
                problems.append(f"{name}: status is {r.get('status')}, not ready")
            if not same_commit(r.get("commit_sha"), repo["commit"]):
                problems.append(f"{name}: indexed at {str(r.get('commit_sha'))[:7]} but the questions pin {repo['commit'][:7]}")
    return problems, ids, roots


def main(argv=None) -> int:
    root_dir = Path(__file__).resolve().parents[2]
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("--api", default="http://localhost:8080")
    p.add_argument("--clones", required=True, type=Path, help="directory holding one clone per repository, named after the repository")
    p.add_argument("--questions", type=Path, default=root_dir / "docs/phase1/questions.json")
    p.add_argument("--out", type=Path, default=root_dir / "docs/phase1")
    p.add_argument("--only", help="ask only this question id")
    p.add_argument("--pace", type=float, default=PACE_SECONDS, help="seconds between questions")
    p.add_argument("--dry-run", action="store_true", help="check the setup and list what would be asked; spends nothing")
    p.add_argument("--report-only", action="store_true", help="rebuild checks.md from raw.json without the API")
    args = p.parse_args(argv)

    data = json.loads(args.questions.read_text(encoding="utf-8"))
    bad = validate_questions(data)
    if bad:
        print("question file problems:\n  " + "\n  ".join(bad), file=sys.stderr)
        return 2
    items = flatten(data)
    if args.only:
        items = [i for i in items if i["id"] == args.only]
        if not items:
            print(f"no question with id {args.only}", file=sys.stderr)
            return 2

    raw_path = args.out / "raw.json"
    results = load_results(raw_path)

    api_repos = None
    if not args.report_only:
        got = http_json("GET", args.api + "/api/repositories")
        if not got["ok"]:
            print(f"cannot list repositories: {got['message']}", file=sys.stderr)
            return 2
        api_repos = got["body"]
    problems, ids, roots = setup_problems(data, args.clones, api_repos)
    if problems:
        print("setup problems (nothing was asked):\n  " + "\n  ".join(problems), file=sys.stderr)
        return 2

    pending = [i for i in items if i["id"] not in results]
    if args.dry_run:
        print(f"setup ok. {len(items)} questions, {len(items) - len(pending)} already answered, {len(pending)} would be asked "
              f"(about {len(pending)} embedding requests, {args.pace:g}s apart).")
        return 0

    stopped = None
    if not args.report_only and pending:
        print(f"asking {len(pending)} questions, about {len(pending)} embedding requests")

        def ask(item):
            return http_json("POST", f"{args.api}/api/repositories/{ids[item['repo']]}/query", {"question": item["question"]})

        stopped = run_questions(items, results, ask, lambda r: save_results(raw_path, r, args.api), time.sleep, args.pace)
        if stopped:
            print(stopped, file=sys.stderr)

    all_items = flatten(data)
    (args.out / "checks.md").write_text(render_report(all_items, results, roots), encoding="utf-8")
    done = [i for i in all_items if i["id"] in results]
    passed = sum(1 for i in done if check_response(i, results[i["id"]]["response"], roots[i["repo"]])["passed"])
    print(f"{len(done)} of {len(all_items)} answered; {passed} passed every applicable check. Wrote {args.out / 'checks.md'}")
    return 1 if stopped else 0


if __name__ == "__main__":
    sys.exit(main())
