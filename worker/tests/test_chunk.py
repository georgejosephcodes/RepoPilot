from pathlib import Path

import pytest

from repopilot_worker import chunk as chunk_mod, parse
from repopilot_worker.chunk import chunk_file, row_to_line, split_lines, windows

FIX = Path(__file__).parent / "fixtures"
LANG = {"py": "python", "go": "go", "ts": "typescript", "tsx": "typescript", "js": "javascript", "md": "markdown"}

# (symbol, kind, start_line, end_line), written by hand from the fixture files.
EXPECTED = {
    "sample.py": [
        (None, "window", 1, 6),
        ("add", "function", 9, 14),               # both comment lines attached, nested helper stays inside
        ("after_trailing", "function", 18, 19),   # `x = 1  # trailing note` on line 17 is not attached
        ("separated", "function", 24, 25),        # comment on line 22 is separated by a blank line
        ("decorated", "function", 28, 31),        # both decorators attached
        ("Foo", "class", 34, 47),
        ("coroutine", "function", 50, 51),
    ],
    "sample.go": [
        (None, "window", 1, 13),
        ("Scheduler", "struct", 15, 18),          # doc comment attached
        ("Runner", "interface", 20, 22),
        ("A", "type", 25, 25),                    # grouped type ( ... ): one symbol per spec
        ("B", "struct", 26, 27),                  # comment inside the group attached
        ("Alias", "type", 30, 30),
        ("Scheduler.Run", "method", 32, 36),      # pointer receiver, doc comment attached
        ("Scheduler.Value", "method", 38, 38),
        ("Scheduler.Anon", "method", 40, 40),     # receiver without a name
        ("Box", "struct", 42, 42),
        ("Box.Get", "method", 44, 44),            # generic receiver *Box[T] -> Box
        ("Top", "function", 46, 46),
        ("Generic", "function", 48, 48),
    ],
    "sample.ts": [
        (None, "window", 1, 4),
        ("exported", "function", 6, 9),           # export keyword and comment included
        ("plain", "function", 11, 11),
        ("default", "function", 13, 13),
        ("Widget", "class", 15, 34),              # class decorator included
        ("Abs", "class", 36, 38),
        ("arrow", "function", 40, 40),
        ("arrow2", "function", 41, 41),
        ("fnExpr", "function", 42, 42),
        ("Shape", "interface", 44, 46),
        ("Id", "type", 48, 48),
        ("Color", "enum", 50, 50),
        ("Util", "namespace", 52, 54),
    ],
    "sample.tsx": [
        ("Comp", "function", 3, 3),
        ("Other", "function", 5, 7),
    ],
    "sample.js": [
        (None, "window", 1, 3),
        ("top", "function", 5, 6),
        ("exported", "function", 8, 8),
        ("Foo", "class", 10, 17),
        ("arrow", "function", 19, 19),
        ("old", "function", 20, 20),
        ("exports.helper", "function", 21, 21),
        ("Foo.prototype.bar", "function", 22, 22),
    ],
    "sample.md": [
        (None, "doc", 1, 1),
        ("Title", "doc", 3, 5),
        ("Title > Install", "doc", 7, 14),        # `# not a heading` inside the fence is ignored
        ("Title > Install > Docker", "doc", 16, 18),
        ("Title > Empty parent > Child", "doc", 22, 29),   # "Empty parent" has no body of its own
    ],
}

FIXTURES = sorted(EXPECTED)


def load(name):
    return name, LANG[name.rsplit(".", 1)[1]], (FIX / name).read_bytes()


def summary(chunks):
    return [(c.symbol, c.kind, c.start_line, c.end_line) for c in chunks]


def check_invariants(chunks, source, max_lines, min_gap=None):
    lines = split_lines(source)
    seen = set()
    for c in chunks:
        assert 1 <= c.start_line <= c.end_line <= len(lines), c
        assert c.content == "\n".join(lines[c.start_line - 1:c.end_line]), c
        assert c.end_line - c.start_line + 1 <= max_lines, c
        key = (c.start_line, c.end_line, c.symbol, c.kind)
        assert key not in seen, f"duplicate {key}"
        seen.add(key)
        assert c.embed_text.endswith("\n---\n" + c.content)
    assert [(c.start_line, c.end_line) for c in chunks] == sorted((c.start_line, c.end_line) for c in chunks)


@pytest.mark.parametrize("name", FIXTURES)
def test_fixture_exact_chunks(name):
    _, language, source = load(name)
    chunks = chunk_file(name, language, source)
    assert summary(chunks) == EXPECTED[name]
    check_invariants(chunks, source, 120)


@pytest.mark.parametrize("name", FIXTURES)
@pytest.mark.parametrize("max_lines,overlap,min_gap", [(8, 2, 3), (20, 5, 1), (5, 0, 3), (3, 1, 1)])
def test_fixture_invariants_with_small_windows(name, max_lines, overlap, min_gap):
    _, language, source = load(name)
    chunks = chunk_file(name, language, source, max_lines, overlap, min_gap)
    assert chunks
    check_invariants(chunks, source, max_lines)


@pytest.mark.parametrize("name", FIXTURES)
def test_fixture_is_deterministic(name):
    _, language, source = load(name)
    assert chunk_file(name, language, source) == chunk_file(name, language, source)


def test_no_uncovered_run_holds_min_gap_nonblank_lines():
    for name in FIXTURES:
        if name.endswith(".md"):
            continue
        _, language, source = load(name)
        lines = split_lines(source)
        chunks = chunk_file(name, language, source, min_gap=3)
        covered = {n for c in chunks for n in range(c.start_line, c.end_line + 1)}
        run = 0
        for n, line in enumerate(lines, 1):
            if n in covered:
                run = 0
            elif line.strip():
                run += 1
                assert run < 3, f"{name}: uncovered non-blank run ending at line {n}"


# ---------------------------------------------------------------- class splitting

@pytest.mark.parametrize(
    "name,max_lines,expected",
    [
        ("sample.py", 8, [
            ("Foo", "class", 34, 36),
            ("Foo.static_method", "method", 38, 40),   # decorator attached
            ("Foo.amethod", "method", 42, 43),
            ("Foo.method", "method", 45, 47),          # comment attached
        ]),
        ("sample.ts", 10, [
            ("Widget", "class", 15, 17),
            ("Widget.constructor", "method", 19, 19),
            ("Widget.method", "method", 21, 22),       # comment attached
            ("Widget.onClick", "method", 24, 25),      # decorator is a sibling node, attached
            ("Widget.create", "method", 27, 27),
            ("Widget.value", "method", 29, 29),
            ("Widget.handler", "method", 31, 31),      # arrow-function class property
        ]),
        ("sample.js", 5, [
            ("Foo", "class", 10, 10),
            ("Foo.constructor", "method", 11, 11),
            ("Foo.method", "method", 12, 12),
            ("Foo.s", "method", 13, 13),
            ("Foo.#priv", "method", 14, 14),
            ("Foo.v", "method", 15, 15),
            ("Foo.handler", "method", 16, 16),
        ]),
    ],
)
def test_large_class_becomes_header_plus_members(name, max_lines, expected):
    _, language, source = load(name)
    chunks = chunk_file(name, language, source, max_lines=max_lines, overlap=1, min_gap=99)
    first, last = expected[0][2], expected[-1][3]
    got = [t for t in summary(chunks) if t[2] >= first and t[3] <= last]
    assert got == expected
    check_invariants(chunks, source, max_lines)


def test_small_class_stays_one_chunk_without_member_chunks():
    _, language, source = load("sample.py")
    chunks = chunk_file("sample.py", language, source, max_lines=120)
    assert [c.symbol for c in chunks if c.symbol and c.symbol.startswith("Foo")] == ["Foo"]


def test_generated_class_with_thirty_methods():
    body = ["class Big:", '    """doc"""', ""]
    for i in range(30):
        body += [f"    def m{i}(self):", "        a = 1", "        b = 2", "        c = 3", "        d = 4",
                 "        e = 5", "        return a + b + c + d + e", ""]
    source = "\n".join(body).encode()
    chunks = chunk_file("big.py", "python", source, max_lines=120)
    check_invariants(chunks, source, 120)
    assert [c.symbol for c in chunks[:3]] == ["Big", "Big.m0", "Big.m1"]
    assert sum(1 for c in chunks if c.kind == "method") == 30
    header = chunks[0]
    assert (header.start_line, header.end_line) == (1, 2)   # ends before the first method, blank line trimmed


# ---------------------------------------------------------------- oversized symbols

def test_windows_helper():
    assert windows(1, 300, 100, 10) == [(1, 100), (91, 190), (181, 280), (271, 300)]
    assert windows(5, 9, 100, 10) == [(5, 9)]
    assert windows(1, 10, 10, 3) == [(1, 10)]
    assert windows(1, 11, 10, 3) == [(1, 10), (8, 11)]
    assert windows(1, 6, 3, 0) == [(1, 3), (4, 6)]


def test_oversized_function_is_split_with_overlap_and_part_labels():
    source = ("def big():\n" + "".join(f"    x{i} = {i}\n" for i in range(299))).encode()   # 300 lines
    chunks = chunk_file("big.py", "python", source, max_lines=100, overlap=10)
    check_invariants(chunks, source, 100)
    assert [(c.start_line, c.end_line) for c in chunks] == [(1, 100), (91, 190), (181, 280), (271, 300)]
    assert {c.symbol for c in chunks} == {"big"} and {c.kind for c in chunks} == {"function"}
    assert "Part: 2/4" in chunks[1].embed_text and "Part:" not in chunk_file("a.py", "python", b"def f():\n    pass\n")[0].embed_text
    union = {n for c in chunks for n in range(c.start_line, c.end_line + 1)}
    assert union == set(range(1, 301))
    assert chunks[0].end_line - chunks[1].start_line + 1 == 10   # overlap size


def test_chunk_file_validates_window_parameters():
    for bad in [(0, 0), (10, 10), (10, 11), (10, -1)]:
        with pytest.raises(ValueError):
            chunk_file("a.py", "python", b"x = 1\n", max_lines=bad[0], overlap=bad[1])


# ---------------------------------------------------------------- edge inputs

def test_empty_and_blank_files_have_no_chunks():
    for src in [b"", b"\n", b"   \n\t\n\n"]:
        assert chunk_file("a.py", "python", src) == []
        assert chunk_file("a.md", "markdown", src) == []


def test_no_trailing_newline():
    chunks = chunk_file("a.py", "python", b"def f():\n    return 1")
    assert summary(chunks) == [("f", "function", 1, 2)]
    assert chunks[0].content == "def f():\n    return 1"


def test_crlf_gives_same_lines_as_lf():
    lf = (FIX / "sample.py").read_bytes()
    crlf = lf.replace(b"\n", b"\r\n")
    a = chunk_file("s.py", "python", lf)
    b = chunk_file("s.py", "python", crlf)
    assert summary(a) == summary(b)
    assert all("\r" not in c.content for c in b)


def test_lines_split_on_newline_only():
    assert split_lines(b"a\x0cb\nc\n") == ["a\x0cb", "c"]
    assert split_lines("x y\nz".encode()) == ["x y", "z"]
    assert split_lines(b"a\r\nb\r\n") == ["a", "b"]
    assert split_lines(b"a\n\n") == ["a", ""]
    assert split_lines(b"") == []


def test_row_to_line():
    assert row_to_line(0) == 1
    src = b"a = 1\n\ndef f():\n    pass\n"
    tree = parse.parse("python", src)
    fn = tree.root_node.children[-1]
    (start_row, _), (end_row, _) = fn.start_point, fn.end_point   # unpack: see symbols._pos
    assert (row_to_line(start_row), row_to_line(end_row)) == (3, 4)


def test_non_utf8_bytes_do_not_crash():
    src = b"def f():\n    return '\xff\xfe'\n"
    chunks = chunk_file("a.py", "python", src)
    assert summary(chunks) == [("f", "function", 1, 2)]
    assert "�" in chunks[0].content


def test_syntax_error_file_still_yields_chunks():
    src = b"def ok():\n    return 1\n\ndef broken(:\n    ((((\n    x = = 2\n"
    chunks = chunk_file("bad.py", "python", src)
    assert chunks
    check_invariants(chunks, src, 120)
    covered = {n for c in chunks for n in range(c.start_line, c.end_line + 1)}
    assert {1, 2} <= covered


def test_constants_only_file_falls_back_to_a_window():
    chunks = chunk_file("consts.py", "python", b"A = 1\nB = 2\n")
    assert summary(chunks) == [(None, "window", 1, 2)]


def test_one_line_file():
    assert summary(chunk_file("a.py", "python", b"print(1)\n")) == [(None, "window", 1, 1)]


def test_small_gap_is_dropped_when_symbols_exist():
    chunks = chunk_file("a.py", "python", b"X = 1\n\ndef f():\n    pass\n")
    assert summary(chunks) == [("f", "function", 3, 4)]


def test_extractor_failure_falls_back_to_windows(monkeypatch):
    def boom(*a, **k):
        raise RuntimeError("grammar exploded")

    monkeypatch.setattr(chunk_mod.symbols, "extract", boom)
    chunks = chunk_file("a.py", "python", b"def f():\n    pass\n")
    assert summary(chunks) == [(None, "window", 1, 2)]


def test_unknown_language_uses_windows():
    chunks = chunk_file("a.rb", "ruby", b"puts 1\nputs 2\n")
    assert summary(chunks) == [(None, "window", 1, 2)]


def test_tsx_uses_the_tsx_grammar():
    assert parse.grammar_for("typescript", "src/App.tsx") == "tsx"
    assert parse.grammar_for("typescript", "src/app.ts") == "typescript"
    assert parse.grammar_for("javascript", "a.jsx") == "javascript"
    assert parse.grammar_for("markdown", "README.md") is None
    src = b"export const A = () => <div>{1}</div>;\n"
    assert summary(chunk_file("A.tsx", "typescript", src)) == [("A", "function", 1, 1)]


# ---------------------------------------------------------------- embed text

def test_embed_text_header():
    src = b"def add(a, b):\n    return a + b\n"
    (c,) = chunk_file("pkg/math.py", "python", src)
    assert c.embed_text == (
        "File: pkg/math.py\nLanguage: python\nSymbol: add\nKind: function\n---\n" + src.decode().rstrip("\n")
    )
    (w,) = chunk_file("k.py", "python", b"A = 1\n")
    assert "Symbol: (none)" in w.embed_text and "Kind: window" in w.embed_text


# ---------------------------------------------------------------- markdown

def test_markdown_without_headings_is_one_doc_chunk():
    assert summary(chunk_file("a.md", "markdown", b"just text\nmore text\n")) == [(None, "doc", 1, 2)]


def test_markdown_tilde_fence_and_longer_fence():
    src = b"# A\n\n~~~\n# no\n~~~\n\n````\n```\n# still code\n```\n````\n\n# B\ntext\n"
    got = summary(chunk_file("a.md", "markdown", src))
    assert got == [("A", "doc", 1, 11), ("B", "doc", 13, 14)]


def test_markdown_heading_forms():
    src = b"## Title ##\nbody\n\n    # indented code, not a heading\n\n#NoSpace\n\n###### Deep\nx\n"
    got = summary(chunk_file("a.md", "markdown", src))
    assert got == [("Title", "doc", 1, 6), ("Title > Deep", "doc", 8, 9)]


def test_markdown_sibling_headings_reset_the_path():
    src = b"# A\n## B\nx\n## C\ny\n# D\nz\n"
    assert [c.symbol for c in chunk_file("a.md", "markdown", src)] == ["A > B", "A > C", "D"]


def test_markdown_oversized_section_is_windowed():
    src = ("# Big\n" + "line\n" * 50).encode()
    chunks = chunk_file("a.md", "markdown", src, max_lines=20, overlap=2)
    check_invariants(chunks, src, 20)
    assert len(chunks) > 2 and {c.symbol for c in chunks} == {"Big"}


def test_large_file_does_not_crash_tree_sitter():
    # Regression: tree-sitter 0.26.0 on Python 3.14 segfaulted reading Point.row on big trees.
    parts = ["def huge():\n"] + [f"    v{i} = {i}\n" for i in range(1500)] + ["\n"]
    for i in range(400):
        parts.append(f"def f{i}():\n    return {i}\n\n")
    source = "".join(parts).encode()
    for _ in range(3):
        chunks = chunk_file("big.py", "python", source, max_lines=120, overlap=10)
    check_invariants(chunks, source, 120)
    assert sum(1 for c in chunks if c.symbol and c.symbol.startswith("f")) == 400


def test_markdown_heading_links_are_reduced_to_their_text():
    src = b"# Tools\n\n## [hello](hello/) and [hello/reverse](hello/reverse/)\n\ntext\n"
    assert [c.symbol for c in chunk_file("a.md", "markdown", src)] == ["Tools > hello and hello/reverse"]
