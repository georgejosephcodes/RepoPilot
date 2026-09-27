"""Dev helper: show how a directory would be chunked.

    python -m repopilot_worker.chunk_report <dir> [-v]
"""

import argparse
import sys
from pathlib import Path

from . import config, scan
from .chunk import chunk_file


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="chunk_report")
    parser.add_argument("directory", type=Path)
    parser.add_argument("-v", "--verbose", action="store_true", help="print every chunk")
    args = parser.parse_args(argv)

    cfg = config.load()
    result = scan.scan(args.directory, cfg.max_files, cfg.max_file_kb)

    by_kind: dict[str, int] = {}
    total = longest = empty = 0
    for f in result.files:
        chunks = chunk_file(f.rel_path, f.language, f.abs_path.read_bytes(),
                            cfg.chunk_max_lines, cfg.chunk_overlap_lines, cfg.chunk_min_gap_lines)
        print(f"{f.rel_path}  ({f.language}, {f.lines} lines, {len(chunks)} chunks)")
        if not chunks:
            empty += 1
        for c in chunks:
            total += 1
            size = c.end_line - c.start_line + 1
            longest = max(longest, size)
            by_kind[c.kind] = by_kind.get(c.kind, 0) + 1
            if args.verbose:
                print(f"  {c.start_line:>5}-{c.end_line:<5} {c.kind:<9} {c.symbol or '-'}")

    kinds = ", ".join(f"{k} {v}" for k, v in sorted(by_kind.items()))
    print(f"\n{len(result.files)} files, {total} chunks ({kinds}); longest {longest} lines; "
          f"{empty} files with no chunks")
    return 0


if __name__ == "__main__":
    sys.exit(main())
