"""Check a /query response against the real repository files.

    curl -s -X POST localhost:8080/api/repositories/1/query -d '{"question":"..."}' | \
        worker/.venv/bin/python worker/scripts/verify_citations.py /tmp/ex

For every citation: the snippet must equal the file's lines start_line..end_line (the same rule the chunker
guarantees), every [n] marker in the answer must have a citation, and every citation must be used in the answer.
Exit code 1 on any problem. Standard library only.
"""

import json
import re
import sys
from pathlib import Path


def lines_of(path: Path) -> list[str]:
    text = path.read_bytes().decode("utf-8", "replace").replace("\r\n", "\n")
    lines = text.split("\n")
    if lines and lines[-1] == "":
        lines.pop()
    return lines


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: verify_citations.py <repository directory>", file=sys.stderr)
        return 2
    root = Path(sys.argv[1])
    data = json.load(sys.stdin)
    if "error" in data:
        print("the response is an error:", json.dumps(data["error"]))
        return 1

    problems = 0
    print(f"grounded={data.get('grounded')} refused={data.get('refused')} truncated={data.get('truncated')} "
          f"citations={len(data.get('citations', []))}")
    print("stats:", json.dumps(data.get("stats")))
    print("\nanswer:\n" + data.get("answer", ""))

    for c in data.get("citations", []):
        path = root / c["file"]
        label = f"[{c['n']}] {c['file']}:{c['start_line']}-{c['end_line']} ({c.get('symbol') or '-'})"
        if not path.is_file():
            print(f"FAIL {label}: file not found under {root}")
            problems += 1
            continue
        real = "\n".join(lines_of(path)[c["start_line"] - 1:c["end_line"]])
        if real == c["snippet"]:
            print(f"ok   {label}: snippet equals the real lines")
        else:
            print(f"FAIL {label}: snippet differs from the real file lines")
            problems += 1

    answer = data.get("answer", "")
    cited = {c["n"] for c in data.get("citations", [])}
    # a chain of markers such as [7][8] counts as several; a marker glued to an identifier (items[1]) does not
    in_text = set()
    for chain in re.findall(r"(?<![A-Za-z0-9_\]])(?:\[\d+\])+", answer):
        in_text.update(int(n) for n in re.findall(r"\[(\d+)\]", chain))
    for n in sorted(in_text - cited):
        print(f"FAIL marker [{n}] is in the answer but has no citation")
        problems += 1
    for n in sorted(cited - in_text):
        print(f"FAIL citation [{n}] is not used in the answer")
        problems += 1

    print("\n" + ("all checks passed" if problems == 0 else f"{problems} problem(s)"))
    return 0 if problems == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
