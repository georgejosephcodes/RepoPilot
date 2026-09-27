"""Turn a source file into chunks: the units that get embedded, retrieved, and cited.

Guarantees (asserted by tests):
- `content` is exactly the file's lines `start_line..end_line`, so a citation always
  matches the code it points at.
- Line numbers are 1-based and inclusive.
- No chunk is longer than `max_lines`, so embedding input size is bounded.
- The same input always gives the same output in the same order.
"""

import logging
import re
from dataclasses import dataclass

from . import parse, symbols

log = logging.getLogger(__name__)


@dataclass(frozen=True)
class Chunk:
    file_path: str
    language: str
    symbol: str | None
    kind: str
    start_line: int   # 1-based inclusive
    end_line: int
    content: str      # exactly "\n".join(lines[start_line - 1:end_line])
    embed_text: str   # header + content; used only for embedding, never shown as a snippet


@dataclass(frozen=True)
class _Piece:
    start: int
    end: int
    symbol: str | None
    kind: str


def split_lines(source: bytes) -> list[str]:
    """Decode and split on "\\n" only, so line numbers match editors and GitHub.

    `str.splitlines` also splits on form feed, U+2028 and others, which would shift them.
    """
    text = source.decode("utf-8", "replace").replace("\r\n", "\n")
    lines = text.split("\n")
    if lines and lines[-1] == "":
        lines.pop()
    return lines


def row_to_line(row: int) -> int:
    """tree-sitter rows are 0-based, our lines are 1-based."""
    return row + 1


def chunk_file(
    rel_path: str,
    language: str,
    source: bytes,
    max_lines: int = 120,
    overlap: int = 10,
    min_gap: int = 3,
) -> list[Chunk]:
    if max_lines < 1 or overlap < 0 or overlap >= max_lines:
        raise ValueError("need 0 <= overlap < max_lines")
    lines = split_lines(source)
    if not any(line.strip() for line in lines):
        return []

    if language == "markdown":
        pieces = _markdown_pieces(lines)
    else:
        pieces = _code_pieces(rel_path, language, lines, max_lines, min_gap)

    pieces = _split_oversized(pieces, lines, max_lines, overlap)
    pieces.sort(key=lambda p: (p[0].start, p[0].end, p[0].symbol or ""))
    return [_build(rel_path, language, lines, piece, part, parts) for piece, part, parts in pieces]


# ---------------------------------------------------------------- code files

def _code_pieces(rel_path: str, language: str, lines: list[str], max_lines: int, min_gap: int) -> list[_Piece]:
    pieces: list[_Piece] = []
    for sym in _extract(rel_path, language, lines):
        pieces.extend(_symbol_pieces(sym, lines, max_lines))

    # With no symbols at all (constants-only file, parse trouble), index any non-blank content.
    threshold = min_gap if pieces else 1
    pieces.extend(_gap_pieces(pieces, lines, threshold))
    return pieces


def _extract(rel_path: str, language: str, lines: list[str]) -> list[symbols.Symbol]:
    grammar = parse.grammar_for(language, rel_path)
    if grammar is None:
        return []
    try:
        tree = parse.parse(grammar, "\n".join(lines).encode("utf-8"))
        found = symbols.extract(grammar, tree.root_node)
    except Exception:
        log.exception("symbol extraction failed for %s", rel_path)
        return []

    # Guard against overlapping symbols: keep the first, trim or drop later ones.
    out: list[symbols.Symbol] = []
    last_end = 0
    for sym in found:
        if sym.end_line <= last_end:
            continue
        if sym.start_line <= last_end:
            sym = symbols.Symbol(sym.name, sym.kind, last_end + 1, sym.end_line, ())
        n = len(lines)
        if sym.start_line < 1 or sym.end_line > n or sym.start_line > sym.end_line:
            continue
        out.append(sym)
        last_end = sym.end_line
    return out


def _symbol_pieces(sym: symbols.Symbol, lines: list[str], max_lines: int) -> list[_Piece]:
    size = sym.end_line - sym.start_line + 1
    if sym.kind == "class" and sym.members and size > max_lines:
        pieces = []
        header_end = sym.members[0].start_line - 1
        while header_end >= sym.start_line and not lines[header_end - 1].strip():
            header_end -= 1
        if header_end >= sym.start_line:
            pieces.append(_Piece(sym.start_line, header_end, sym.name, "class"))
        last_end = sym.start_line - 1
        for m in sym.members:
            if m.end_line <= last_end or m.start_line < sym.start_line or m.end_line > sym.end_line:
                continue
            start = max(m.start_line, last_end + 1)
            pieces.append(_Piece(start, m.end_line, m.name, m.kind))
            last_end = m.end_line
        return pieces
    return [_Piece(sym.start_line, sym.end_line, sym.name, sym.kind)]


def _gap_pieces(pieces: list[_Piece], lines: list[str], min_nonblank: int) -> list[_Piece]:
    """Runs of lines not covered by any symbol chunk that hold enough non-blank lines."""
    covered = [False] * (len(lines) + 2)
    for p in pieces:
        for n in range(p.start, p.end + 1):
            covered[n] = True

    out: list[_Piece] = []
    n = 1
    while n <= len(lines):
        if covered[n]:
            n += 1
            continue
        start = n
        while n <= len(lines) and not covered[n]:
            n += 1
        end = n - 1
        while start <= end and not lines[start - 1].strip():
            start += 1
        while end >= start and not lines[end - 1].strip():
            end -= 1
        if start <= end and sum(1 for i in range(start, end + 1) if lines[i - 1].strip()) >= min_nonblank:
            out.append(_Piece(start, end, None, "window"))
    return out


# ---------------------------------------------------------------- Markdown

_HEADING = re.compile(r"^ {0,3}(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$")
_FENCE = re.compile(r"^ {0,3}(`{3,}|~{3,})")
_LINK = re.compile(r"\[([^\]]*)\]\([^)]*\)")   # [text](url) -> text


def _markdown_pieces(lines: list[str]) -> list[_Piece]:
    headings: list[tuple[int, int, str]] = []   # (line, level, title)
    fence: tuple[str, int] | None = None
    for i, line in enumerate(lines, 1):
        m = _FENCE.match(line)
        if fence is None:
            if m:
                fence = (m.group(1)[0], len(m.group(1)))
                continue
            h = _HEADING.match(line)
            if h:
                headings.append((i, len(h.group(1)), _LINK.sub(r"\1", h.group(2)).strip()))
        elif m and m.group(1)[0] == fence[0] and len(m.group(1)) >= fence[1] and line.strip() == m.group(1):
            fence = None

    def trimmed(start: int, end: int) -> tuple[int, int] | None:
        while start <= end and not lines[start - 1].strip():
            start += 1
        while end >= start and not lines[end - 1].strip():
            end -= 1
        return (start, end) if start <= end else None

    pieces: list[_Piece] = []
    first = headings[0][0] if headings else len(lines) + 1
    span = trimmed(1, first - 1)
    if span:
        pieces.append(_Piece(span[0], span[1], None, "doc"))

    stack: list[tuple[int, str]] = []
    for idx, (line, level, title) in enumerate(headings):
        while stack and stack[-1][0] >= level:
            stack.pop()
        stack.append((level, title))
        next_line = headings[idx + 1][0] if idx + 1 < len(headings) else len(lines) + 1
        span = trimmed(line, next_line - 1)
        if span is None:
            continue
        if sum(1 for i in range(span[0], span[1] + 1) if lines[i - 1].strip()) <= 1:
            continue  # heading with no body of its own
        pieces.append(_Piece(span[0], span[1], " > ".join(t for _, t in stack), "doc"))
    return pieces


# ---------------------------------------------------------------- windows and output

def windows(start: int, end: int, max_lines: int, overlap: int) -> list[tuple[int, int]]:
    """Split [start, end] into windows of at most max_lines that overlap by `overlap` lines."""
    out = []
    s = start
    while True:
        e = min(s + max_lines - 1, end)
        out.append((s, e))
        if e == end:
            return out
        s = e - overlap + 1


def _split_oversized(pieces: list[_Piece], lines: list[str], max_lines: int,
                     overlap: int) -> list[tuple[_Piece, int, int]]:
    """Return (piece, part, parts). Pieces over max_lines become overlapping windows."""
    out: list[tuple[_Piece, int, int]] = []
    for p in pieces:
        if p.end - p.start + 1 <= max_lines:
            out.append((p, 1, 1))
            continue
        spans = [
            (s, e) for s, e in windows(p.start, p.end, max_lines, overlap)
            if any(lines[i - 1].strip() for i in range(s, e + 1))
        ]
        for part, (s, e) in enumerate(spans, 1):
            out.append((_Piece(s, e, p.symbol, p.kind), part, len(spans)))
    return out


def _build(rel_path: str, language: str, lines: list[str], piece: _Piece, part: int, parts: int) -> Chunk:
    content = "\n".join(lines[piece.start - 1:piece.end])
    header = [
        f"File: {rel_path}",
        f"Language: {language}",
        f"Symbol: {piece.symbol or '(none)'}",
        f"Kind: {piece.kind}",
    ]
    if parts > 1:
        header.append(f"Part: {part}/{parts}")
    return Chunk(
        file_path=rel_path,
        language=language,
        symbol=piece.symbol,
        kind=piece.kind,
        start_line=piece.start,
        end_line=piece.end,
        content=content,
        embed_text="\n".join(header) + "\n---\n" + content,
    )
