"""Compare two eval runs question by question: same retrieved chunk ids, in the same order?

Used for the step 7 rollback proof (PHASE2.md 7.10): a run made after a refactor must retrieve exactly what the
committed run retrieved. Only questions present in both runs are compared. Exit status 1 on any difference.
Standard library only; reads two JSON files, sends nothing.

Run:  python3 worker/scripts/compare_runs.py docs/phase2/runs/baseline.json /tmp/s7-vector.json
"""

import json
import sys


def ranks(path):
    run = json.load(open(path, encoding="utf-8"))
    return {q["id"]: [r["chunk_id"] for r in q["retrieved"]] for q in run["questions"]}


def main():
    if len(sys.argv) != 3:
        sys.exit("usage: compare_runs.py <committed run.json> <new run.json>")
    old, new = ranks(sys.argv[1]), ranks(sys.argv[2])
    common = sorted(set(old) & set(new))
    if not common:
        sys.exit("no questions in common")
    diff = [q for q in common if old[q] != new[q]]
    for q in diff:
        first = next((i for i, (a, b) in enumerate(zip(old[q], new[q])) if a != b), min(len(old[q]), len(new[q])))
        print(f"  {q}: differs from rank {first + 1} (lengths {len(old[q])} and {len(new[q])})")
    print(f"{len(common)} questions compared, {len(common) - len(diff)} identical, {len(diff)} different")
    sys.exit(1 if diff else 0)


if __name__ == "__main__":
    main()
