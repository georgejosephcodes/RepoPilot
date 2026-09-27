"""Tests for scripts/run_answers.py. No network and no database; the checks themselves are run_demo.py's and are
tested in test_run_demo.py."""

from scripts import run_answers as ra

DATA = {
    "repositories": [{"name": "a/b", "commit": "1" * 40}],
    "questions": [
        {"id": "q1", "repo": "a/b", "kind": "location", "split": "dev", "question": "where is X",
         "relevant": [{"file": "x.py", "start_line": 1, "end_line": 2, "grade": 2},
                      {"file": "y.py", "start_line": 1, "end_line": 1, "grade": 1},
                      {"file": "x.py", "start_line": 5, "end_line": 6, "grade": 1}],
         "expected_answer": "X is in x.py"},
        {"id": "q2", "repo": "a/b", "kind": "unanswerable", "split": "test", "question": "how does billing work",
         "relevant": [], "expected_answer": ""},
    ],
}


def write(tmp_path, name, text):
    (tmp_path / name).write_text(text)


def answer(files, refused=False, mode="hybrid_rerank", **stats):
    cites = [{"n": i + 1, "file": f, "start_line": 1, "end_line": 1, "snippet": "one"} for i, f in enumerate(files)]
    text = "Refused." if refused else " ".join(f"see [{c['n']}]." for c in cites)
    return {"answer": text, "grounded": not refused and bool(cites), "refused": refused, "truncated": False,
            "citations": cites, "stats": {"retrieval_mode": mode, **stats}}


def rec(body, ms=1000):
    return {"response": body, "elapsed_ms": ms}


def test_load_items_takes_expected_files_from_the_spans_once_each_in_order():
    items = ra.load_items(DATA)
    assert items[0]["expected_files"] == ["x.py", "y.py"]
    assert items[0]["commit"] == "1" * 40 and items[0]["split"] == "dev"
    assert items[1]["expected_files"] == [] and items[1]["kind"] == "unanswerable"


def test_mode_guard_stops_an_answer_from_the_wrong_mode():
    asked = []

    def ask(item):
        asked.append(item["id"])
        return {"ok": True, "body": answer(["x.py"], mode="vector"), "elapsed_ms": 5}

    reply = ra.guarded(ask, "hybrid_rerank")({"id": "q1"})
    assert reply["ok"] is False and "RETRIEVAL_MODE=hybrid_rerank" in reply["message"]
    assert ra.mode_problem({"stats": {}}, "vector") is not None
    assert ra.mode_problem(answer([], mode="vector"), "vector") is None
    ok = ra.guarded(lambda i: {"ok": True, "body": answer(["x.py"]), "elapsed_ms": 5}, "hybrid_rerank")({"id": "q1"})
    assert ok["ok"] is True
    failed = ra.guarded(lambda i: {"ok": False, "message": "rate_limited"}, "vector")({"id": "q1"})
    assert failed == {"ok": False, "message": "rate_limited"}


def test_a_guarded_run_saves_nothing_from_the_wrong_mode():
    results = {}
    stopped = ra.demo.run_questions(
        ra.load_items(DATA), results,
        ra.guarded(lambda i: {"ok": True, "body": answer([], mode="vector"), "elapsed_ms": 1}, "hybrid_rerank"),
        lambda r: None, lambda s: None, 0, log=lambda m: None)
    assert stopped and results == {}


def test_changed_detects_other_files_refusal_or_check_result(tmp_path):
    write(tmp_path, "x.py", "one\n")
    write(tmp_path, "y.py", "one\n")
    item = ra.load_items(DATA)[0]
    a = ra.summarize(item, rec(answer(["x.py"])), tmp_path)
    assert a["passed"] and not ra.changed(a, ra.summarize(item, rec(answer(["x.py"])), tmp_path))
    assert ra.changed(a, ra.summarize(item, rec(answer(["x.py", "y.py"])), tmp_path))
    assert ra.changed(a, ra.summarize(item, rec(answer([], refused=True)), tmp_path))
    assert not ra.changed(a, None)


def test_judged_time_adds_the_stored_rerank_time_only_for_cached_rankings(tmp_path):
    write(tmp_path, "x.py", "one\n")
    item = ra.load_items(DATA)[0]
    cached = ra.summarize(item, rec(answer(["x.py"], rerank_ms=1700, rerank_cached=True), 2000), tmp_path)
    upstream = ra.summarize(item, rec(answer(["x.py"], rerank_ms=1700, rerank_cached=False), 3700), tmp_path)
    assert ra.judged_time_ms(cached) == 3700
    assert ra.judged_time_ms(upstream) == 3700


def test_latency_gate(tmp_path):
    write(tmp_path, "x.py", "one\n")
    item = ra.load_items(DATA)[0]

    def s(ms, mode, **st):
        return ra.summarize(item, rec(answer(["x.py"], mode=mode, **st), ms), tmp_path)

    vec = [s(1000, "vector"), s(2000, "vector"), s(3000, "vector")]
    ok = ra.latency(vec, [s(1500, "hybrid_rerank", rerank_ms=1500, rerank_cached=True)] * 3)
    assert ok["diff"] == 1000 and ok["passed"] and ok["rerank_cached"] == 3
    bad = ra.latency(vec, [s(3000, "hybrid_rerank", rerank_ms=1500, rerank_cached=True, rerank_fallback="timeout")] * 3)
    assert bad["diff"] == 2500 and not bad["passed"] and bad["rerank_fallbacks"] == 3
    assert ra.latency([], vec) is None


def test_pct_nearest_rank():
    assert ra.pct([5, 1, 3, 2, 4], 90) == 5
    assert ra.pct([5, 1, 3, 2, 4], 50) == 3
    assert ra.pct([], 50) == 0


def test_to_judge_takes_changed_failed_and_first_unchanged(monkeypatch):
    monkeypatch.setattr(ra, "SPOT_CHECKS", 2)
    items = [{"id": f"q{i}"} for i in range(1, 7)]
    same = {"cited": ["x.py"], "refused": False, "passed": True}
    vec = {f"q{i}": dict(same) for i in range(1, 7)}
    hyb = {f"q{i}": dict(same) for i in range(1, 7)}
    hyb["q5"] = {"cited": ["y.py"], "refused": False, "passed": True}  # changed
    vec["q6"] = {**same, "passed": False}  # a failed check without a change in files
    assert ra.to_judge(items, vec, hyb) == ["q1", "q2", "q5", "q6"]


def test_render_report_has_checks_latency_pairs_and_the_judge_list(tmp_path):
    write(tmp_path, "x.py", "one\n")
    items = ra.load_items(DATA)
    results = {
        "vector": {"q1": rec(answer(["x.py"], mode="vector"), 1000), "q2": rec(answer([], refused=True, mode="vector"), 900)},
        "hybrid_rerank": {"q1": rec(answer(["x.py"], rerank_ms=1200, rerank_cached=True), 1100),
                          "q2": rec(answer(["x.py"]), 1000)},  # an unanswerable question answered: fails
    }
    out = ra.render_report(items, results, {"a/b": tmp_path})
    assert "| all | 2 of 2 | 1 of 2 |" in out
    assert "| split: test | 1 of 1 | 0 of 1 |" in out
    assert "Median difference" in out and "pass" in out
    assert "| q2 | test | unanswerable | pass (refused) | FAIL | yes |" in out
    assert "**q2** (hybrid_rerank, unanswerable): expected a refusal with no citations" in out
    assert "(2 questions): q1, q2." in out


def test_render_report_before_any_answer(tmp_path):
    out = ra.render_report(ra.load_items(DATA), {"vector": {}, "hybrid_rerank": {}}, {"a/b": tmp_path})
    assert "Not available until both modes have answers." in out and "| q1 | dev | location | not asked | not asked |" in out
