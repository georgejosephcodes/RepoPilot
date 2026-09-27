"""Check the Phase 2 evaluation set mechanically: its schema, and every labelled span against the real clones.

    worker/.venv/bin/python worker/scripts/eval_labels.py docs/phase2/eval.json --clones /tmp/demo

A span is a file and a 1-based, inclusive line range. The checks prove that each span exists at the pinned commit
(the file is there, the lines are inside it, the named symbol appears inside the span); they do not prove that the
span answers the question. Exit code 1 on any problem. Standard library only.
"""

import argparse
import json
import sys
from collections import Counter
from pathlib import Path

try:
    from scripts.run_demo import git_head, lines_of, same_commit
except ImportError:  # run as a script: scripts/ is on sys.path, not its parent
    from run_demo import git_head, lines_of, same_commit

KINDS = ["location", "flow", "identifier", "literal", "conceptual", "trap", "unanswerable"]
SPLITS = ["dev", "test"]
ORIGINS = ["phase1", "phase2"]
QUESTION_FIELDS = ["id", "repo", "kind", "split", "question", "relevant", "expected_answer", "why_hard", "origin"]


def _is_line(v) -> bool:
    return isinstance(v, int) and not isinstance(v, bool) and v >= 1


def schema_problems(data) -> list[str]:
    """Structure only; nothing is read from disk."""
    if not isinstance(data, dict):
        return ["top level is not an object"]
    problems = []
    repos = data.get("repositories")
    if not isinstance(repos, list) or not repos:
        return ["no repositories"]
    names = set()
    for r in repos:
        name = r.get("name") if isinstance(r, dict) else None
        if not isinstance(name, str) or "/" not in name:
            problems.append(f"repository {r!r}: name must be owner/name")
            continue
        if name in names:
            problems.append(f"repository {name}: listed twice")
        names.add(name)
        commit = r.get("commit")
        if not isinstance(commit, str) or len(commit) != 40 or any(c not in "0123456789abcdef" for c in commit):
            problems.append(f"repository {name}: commit must be the full 40-character lowercase hash")

    questions = data.get("questions")
    if not isinstance(questions, list) or not questions:
        return problems + ["no questions"]
    seen = set()
    for q in questions:
        if not isinstance(q, dict):
            problems.append(f"question {q!r}: not an object")
            continue
        qid = q.get("id", "?")
        missing = [f for f in QUESTION_FIELDS if f not in q]
        if missing:
            problems.append(f"{qid}: missing {', '.join(missing)}")
            continue
        if qid in seen:
            problems.append(f"{qid}: duplicate id")
        seen.add(qid)
        if q["repo"] not in names:
            problems.append(f"{qid}: unknown repository {q['repo']!r}")
        if q["kind"] not in KINDS:
            problems.append(f"{qid}: unknown kind {q['kind']!r}")
        if q["split"] not in SPLITS:
            problems.append(f"{qid}: split must be dev or test")
        if q["origin"] not in ORIGINS:
            problems.append(f"{qid}: origin must be phase1 or phase2")
        if not isinstance(q["question"], str) or not q["question"].strip():
            problems.append(f"{qid}: empty question")
        if q["kind"] == "trap" and not str(q["why_hard"]).strip():
            problems.append(f"{qid}: a trap question needs why_hard")
        relevant = q["relevant"]
        if not isinstance(relevant, list):
            problems.append(f"{qid}: relevant must be a list")
            continue
        for i, s in enumerate(relevant):
            label = f"{qid} span {i + 1}"
            if not isinstance(s, dict) or not isinstance(s.get("file"), str) or not s["file"]:
                problems.append(f"{label}: needs a file")
                continue
            if s["file"].startswith("/") or "\\" in s["file"] or ".." in s["file"].split("/"):
                problems.append(f"{label}: file must be a clean relative path")
            if not _is_line(s.get("start_line")) or not _is_line(s.get("end_line")) or s["end_line"] < s["start_line"]:
                problems.append(f"{label}: bad line range")
            if s.get("grade") not in (1, 2):
                problems.append(f"{label}: grade must be 1 or 2")
            if "symbol" in s and (not isinstance(s["symbol"], str) or not s["symbol"]):
                problems.append(f"{label}: symbol must be a non-empty string when given")
        has_answer = any(isinstance(s, dict) and s.get("grade") == 2 for s in relevant)
        if q["kind"] == "unanswerable" and relevant:
            problems.append(f"{qid}: an unanswerable question must have no spans")
        if q["kind"] != "unanswerable" and not has_answer:
            problems.append(f"{qid}: an answerable question needs a grade-2 span")
    return problems


def span_problems(data, clones: Path) -> list[str]:
    """Every clone at its pinned commit; every span inside a real file; every named symbol inside its span."""
    problems = []
    roots = {}
    for r in data["repositories"]:
        root = clones / r["name"].split("/")[-1]
        head = git_head(root)
        if head is None:
            problems.append(f"{r['name']}: no git clone at {root}")
        elif not same_commit(head, r["commit"]):
            problems.append(f"{r['name']}: clone is at {head[:7]} but the file pins {r['commit'][:7]}")
        else:
            roots[r["name"]] = root.resolve()
    cache: dict[Path, list[str]] = {}
    for q in data["questions"]:
        base = roots.get(q["repo"])
        if base is None:
            continue
        for i, s in enumerate(q["relevant"]):
            label = f"{q['id']} span {i + 1} {s['file']}:{s['start_line']}-{s['end_line']}"
            path = (base / s["file"]).resolve()
            if base not in path.parents:
                problems.append(f"{label}: path is outside the repository")
                continue
            if not path.is_file():
                problems.append(f"{label}: file not found")
                continue
            if path not in cache:
                cache[path] = lines_of(path)
            lines = cache[path]
            if s["end_line"] > len(lines):
                problems.append(f"{label}: the file has only {len(lines)} lines")
                continue
            symbol = s.get("symbol")
            if symbol:
                name = symbol.split(".")[-1]
                if name not in "\n".join(lines[s["start_line"] - 1:s["end_line"]]):
                    problems.append(f"{label}: {name!r} does not appear inside the span")
    return problems


def summary(data) -> str:
    qs = data["questions"]
    by = Counter((q["repo"], q["kind"], q["split"]) for q in qs)
    kinds = [k for k in KINDS if any(q["kind"] == k for q in qs)]
    rows = [f"{len(qs)} questions, {sum(len(q['relevant']) for q in qs)} spans; "
            f"dev {sum(q['split'] == 'dev' for q in qs)}, test {sum(q['split'] == 'test' for q in qs)}"]
    rows.append("repository".ljust(22) + "".join(k[:6].rjust(8) for k in kinds) + "  (dev/test)")
    for r in data["repositories"]:
        cells = "".join(f"{by[(r['name'], k, 'dev')]}/{by[(r['name'], k, 'test')]}".rjust(8) for k in kinds)
        rows.append(r["name"].ljust(22) + cells)
    return "\n".join(rows)


def main(argv=None) -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("questions", type=Path)
    p.add_argument("--clones", type=Path, default=Path("/tmp/demo"), help="directory holding one clone per repository, named after the repository")
    args = p.parse_args(argv)
    data = json.loads(args.questions.read_text(encoding="utf-8"))
    problems = schema_problems(data)
    if not problems:
        problems = span_problems(data, args.clones)
        print(summary(data))
    for line in problems:
        print("problem:", line)
    print("ok" if not problems else f"{len(problems)} problem(s)")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
