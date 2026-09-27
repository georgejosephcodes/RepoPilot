import math

import pytest

from repopilot_worker import embed
from repopilot_worker.config import Config
from repopilot_worker.embed import EmbedError, FakeEmbedder, OpenAICompatEmbedder, Pacer

KEY = "sk-or-SECRETKEY123"
BASE = "https://provider.example/api/v1"
DIM = 8


class Clock:
    def __init__(self):
        self.now = 1000.0
        self.sleeps = []

    def time(self):
        return self.now

    def sleep(self, seconds):
        self.sleeps.append(seconds)
        self.now += seconds


def raw_vector(text, dim=DIM, scale=0.5):
    """Deterministic non-unit vector, so tests can see normalisation happen."""
    base = [(hash(("v", text, i)) % 1000) / 1000 + 0.1 for i in range(dim)]
    norm = math.sqrt(sum(x * x for x in base))
    return [x / norm * scale for x in base]


def ok_response(body, dim=DIM, **override):
    items = [{"index": i, "embedding": raw_vector(t, dim)} for i, t in enumerate(body["input"])]
    return 200, {"data": items, "usage": {"prompt_tokens": 1}}, {}


class Http:
    """Scripted stand-in for the HTTP layer. Each script item is a (status, data, headers) tuple,
    an exception to raise, or a callable(body) returning a tuple. Once the script is used up it answers 200."""

    def __init__(self, script=()):
        self.script = list(script)
        self.calls = []

    def __call__(self, url, body, headers):
        self.calls.append((url, body, headers))
        step = self.script.pop(0) if self.script else ok_response
        if isinstance(step, Exception):
            raise step
        if callable(step):
            return step(body)
        return step


def make(http=None, clock=None, **kw):
    clock = clock or Clock()
    kw.setdefault("dimension", DIM)
    embedder = OpenAICompatEmbedder(BASE, KEY, "test/model", post=http or Http(), sleep=clock.sleep,
                                    clock=clock.time, jitter=lambda: 0.0, **kw)
    embedder.clock = clock
    return embedder


def norm(v):
    return math.sqrt(sum(x * x for x in v))


# ---------------------------------------------------------------- request shape

def test_document_request_shape():
    http = Http()
    make(http).embed_documents(["a", "b"])
    (url, body, headers), = http.calls
    assert url == BASE + "/embeddings"
    assert headers["Authorization"] == f"Bearer {KEY}"
    assert body == {"model": "test/model", "input": ["a", "b"], "input_type": "search_document"}


def test_dimensions_only_when_configured_and_input_type_can_be_off():
    http = Http()
    make(http, send_dimensions=True, input_types=False).embed_documents(["a"])
    assert http.calls[0][1] == {"model": "test/model", "input": ["a"], "dimensions": DIM}


def test_base_url_trailing_slash_is_handled():
    http = Http()
    OpenAICompatEmbedder(BASE + "/", KEY, "m", DIM, post=http).embed_documents(["a"])
    assert http.calls[0][0] == BASE + "/embeddings"


def test_query_uses_search_query_and_one_request():
    http = Http()
    vec = make(http).embed_query("where is auth?")
    assert len(http.calls) == 1
    assert http.calls[0][1]["input_type"] == "search_query" and http.calls[0][1]["input"] == ["where is auth?"]
    assert len(vec) == DIM and norm(vec) == pytest.approx(1.0)


# ---------------------------------------------------------------- response handling

def test_vectors_are_normalised_to_unit_length():
    vecs = make().embed_documents(["a", "b", "c"])
    assert all(norm(v) == pytest.approx(1.0) for v in vecs)


def test_results_are_matched_by_index_not_arrival_order():
    def shuffled(body):
        status, data, h = ok_response(body)
        data["data"].reverse()
        return status, data, h

    texts = ["one", "two", "three"]
    got = make(Http([shuffled])).embed_documents(texts)
    want = make().embed_documents(texts)
    assert got == want


@pytest.mark.parametrize("label,data", [
    ("zero vector", {"data": [{"index": 0, "embedding": [0.0] * DIM}]}),
    ("nan", {"data": [{"index": 0, "embedding": [float("nan")] + [0.1] * (DIM - 1)}]}),
    ("infinity", {"data": [{"index": 0, "embedding": [float("inf")] + [0.1] * (DIM - 1)}]}),
    ("wrong length", {"data": [{"index": 0, "embedding": [0.1] * (DIM + 1)}]}),
    ("no embedding", {"data": [{"index": 0}]}),
    ("wrong count", {"data": []}),
    ("no data key", {"result": 1}),
    ("duplicate index", {"data": [{"index": 0, "embedding": [0.1] * DIM}, {"index": 0, "embedding": [0.1] * DIM}]}),
    ("index out of range", {"data": [{"index": 5, "embedding": [0.1] * DIM}]}),
])
def test_bad_responses_raise_embed_error(label, data):
    texts = ["a", "b"] if label == "duplicate index" else ["a"]
    with pytest.raises(EmbedError):
        make(Http([(200, data, {})])).embed_documents(texts)


# ---------------------------------------------------------------- batching, dedupe, truncation

def test_batches_of_64_keep_order():
    texts = [f"text {i}" for i in range(150)]
    http = Http()
    got = make(http, batch_size=64).embed_documents(texts)
    assert [len(c[1]["input"]) for c in http.calls] == [64, 64, 22]
    assert [t for c in http.calls for t in c[1]["input"]] == texts
    single = make().embed_documents([texts[100]])[0]
    assert got[100] == single


def test_token_cap_splits_batches_earlier():
    texts = ["x" * 4000 + str(i) for i in range(5)]          # about 2,000 estimated tokens each
    http = Http()
    make(http, batch_size=64, batch_tokens=5000).embed_documents(texts)
    assert [len(c[1]["input"]) for c in http.calls] == [2, 2, 1]


def test_single_text_over_token_cap_still_goes_out_alone():
    http = Http()
    make(http, batch_tokens=10, max_chars=100000).embed_documents(["y" * 500])
    assert [len(c[1]["input"]) for c in http.calls] == [1]


def test_duplicates_are_embedded_once_and_fanned_out():
    http = Http()
    got = make(http).embed_documents(["a", "b", "a", "a", "c", "b"])
    assert http.calls[0][1]["input"] == ["a", "b", "c"]
    assert got[0] == got[2] == got[3] and got[1] == got[5] and got[0] != got[1]


def test_long_text_is_cut_and_counted(caplog):
    http = Http()
    e = make(http, max_chars=100)
    with caplog.at_level("WARNING"):
        e.embed_documents(["z" * 20000, "short"])
    assert http.calls[0][1]["input"] == ["z" * 100, "short"]
    assert "1 of 2 texts were cut" in caplog.text
    assert e.prepare("z" * 500) == "z" * 100 and e.prepare("ok") == "ok"


def test_on_batch_receives_each_batch():
    seen = []
    make(batch_size=2).embed_documents(["a", "b", "c"], on_batch=lambda texts, vecs: seen.append((list(texts), len(vecs))))
    assert seen == [(["a", "b"], 2), (["c"], 1)]


# ---------------------------------------------------------------- adaptive splitting

def test_too_large_batch_is_split_in_half():
    http = Http([(422, {"error": {"message": "input length exceeds model maximum"}}, {})])
    seen = []
    texts = [f"t{i}" for i in range(8)]
    got = make(http).embed_documents(texts, on_batch=lambda t, v: seen.append(len(t)))
    assert [len(c[1]["input"]) for c in http.calls] == [8, 4, 4]
    assert seen == [4, 4] and len(got) == 8


def test_single_rejected_input_fails_with_safe_message():
    http = Http([(422, {"error": "too long"}, {})])
    with pytest.raises(EmbedError, match="embedding input rejected"):
        make(http).embed_documents(["only"])
    assert len(http.calls) == 1


def test_413_is_also_treated_as_too_large():
    http = Http([(413, {}, {})])
    make(http).embed_documents(["a", "b"])
    assert [len(c[1]["input"]) for c in http.calls] == [2, 1, 1]


# ---------------------------------------------------------------- pacing

def test_pacer_never_exceeds_limit_in_any_window():
    clock = Clock()
    pacer = Pacer(20, clock=clock.time, sleep=clock.sleep)          # limit 18
    times = []
    for _ in range(25):
        pacer.wait()
        times.append(clock.now)
    for t in times:
        assert sum(1 for x in times if t <= x < t + 60) <= 18
    assert clock.sleeps                                              # it did have to wait


def test_pacer_window_resets_after_idle_minute():
    clock = Clock()
    pacer = Pacer(20, clock=clock.time, sleep=clock.sleep)
    for _ in range(18):
        pacer.wait()
    assert clock.sleeps == []
    clock.now += 61
    for _ in range(18):
        pacer.wait()
    assert clock.sleeps == []                                        # window is empty again


def test_embedder_paces_its_requests():
    clock = Clock()
    e = make(clock=clock, batch_size=1, rpm=10)                      # limit 9 per minute
    e.embed_documents([f"t{i}" for i in range(12)])
    assert clock.sleeps and sum(clock.sleeps) >= 50


# ---------------------------------------------------------------- retries and errors

def test_retries_429_then_succeeds_with_backoff():
    http = Http([(429, {}, {}), (429, {}, {}), ok_response])
    e = make(http)
    e.embed_documents(["a"])
    assert len(http.calls) == 3
    assert e.clock.sleeps == [1, 2]


def test_retry_after_header_is_honoured():
    http = Http([(429, {}, {"Retry-After": "7"}), ok_response])
    e = make(http)
    e.embed_documents(["a"])
    assert e.clock.sleeps == [7.0]


def test_retry_after_is_capped():
    http = Http([(429, {}, {"retry-after": "9999"}), ok_response])
    e = make(http)
    e.embed_documents(["a"])
    assert e.clock.sleeps == [60.0]


def test_gives_up_after_six_attempts():
    http = Http([(503, {}, {})] * 6)
    e = make(http)
    with pytest.raises(EmbedError, match="unavailable, try again later"):
        e.embed_documents(["a"])
    assert len(http.calls) == 6
    assert e.clock.sleeps == [1, 2, 4, 8, 16]


@pytest.mark.parametrize("status,message", [
    (400, "embedding request rejected"),
    (401, "embedding API key rejected or expired"),
    (403, "embedding API key rejected or expired"),
    (404, "embedding model not found"),
    (418, "embedding service error"),
])
def test_permanent_errors_fail_at_once_with_mapped_message(status, message):
    http = Http([(status, {"error": {"message": f"details {KEY} {BASE}"}}, {})])
    with pytest.raises(EmbedError) as exc:
        make(http).embed_documents(["a"])
    assert str(exc.value) == message
    assert len(http.calls) == 1


def test_network_errors_are_retried():
    http = Http([ConnectionResetError("boom"), TimeoutError("slow"), ok_response])
    e = make(http)
    assert len(e.embed_documents(["a"])) == 1
    assert len(http.calls) == 3 and e.clock.sleeps == [1, 2]


def test_error_messages_never_contain_key_or_url():
    scripts = [
        [(503, {"detail": KEY}, {})] * 6,
        [(401, {"detail": KEY}, {})],
        [(200, {"data": []}, {})],
        [ConnectionResetError(f"{BASE} {KEY}")] * 6,
        [(422, {"detail": KEY}, {})],
    ]
    for script in scripts:
        with pytest.raises(EmbedError) as exc:
            make(Http(script)).embed_documents(["a"])
        text = str(exc.value)
        assert KEY not in text and "provider.example" not in text


def test_key_is_not_in_config_repr():
    assert "SECRET" not in repr(Config(embed_api_key="SECRET"))


# ---------------------------------------------------------------- remaining budget

def test_remaining_requests_reads_the_free_tier_counter():
    http = Http([(200, {"data": {"free_model_daily_requests": {"used": 12, "limit": 50, "remaining": 38}}}, {})])
    assert make(http).remaining_requests() == 38
    url, body, headers = http.calls[0]
    assert url == BASE + "/key" and body is None and headers["Authorization"] == f"Bearer {KEY}"


@pytest.mark.parametrize("script", [
    [(200, {"data": {"limit": None}}, {})],            # paid key: no free-tier counter
    [(401, {}, {})],
    [(200, {"unexpected": 1}, {})],
    [ConnectionResetError("x")],
])
def test_remaining_requests_is_none_when_unknown(script):
    assert make(Http(script)).remaining_requests() is None


# ---------------------------------------------------------------- helpers, fake, factory

def test_normalize_helper():
    assert norm(embed.normalize([3.0, 4.0])) == pytest.approx(1.0)
    with pytest.raises(EmbedError):
        embed.normalize([0.0, 0.0])


def test_estimate_tokens_is_pessimistic_and_positive():
    assert embed.estimate_tokens("") == 1
    assert embed.estimate_tokens("x" * 1000) == 500


def test_sha256_is_stable():
    assert embed.sha256_hex("abc") == "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"


def test_fake_embedder_is_deterministic_unit_length_and_batches():
    f = FakeEmbedder(dimension=16, batch_size=2)
    a = f.embed_documents(["x", "y", "z"])
    b = FakeEmbedder(dimension=16).embed_documents(["x", "y", "z"])
    assert a == b and all(norm(v) == pytest.approx(1.0) for v in a) and a[0] != a[1]
    assert [len(c[1]) for c in f.calls] == [2, 1]


def test_fake_embedder_can_fail_on_a_batch():
    f = FakeEmbedder(dimension=4, batch_size=2, fail_on_batch=1)
    seen = []
    with pytest.raises(EmbedError):
        f.embed_documents(["a", "b", "c", "d"], on_batch=lambda t, v: seen.append(t))
    assert seen == [["a", "b"]]


def test_make_embedder_requires_a_key_and_uses_config():
    with pytest.raises(EmbedError, match="EMBED_API_KEY"):
        embed.make_embedder(Config())
    e = embed.make_embedder(Config(embed_api_key="k", embed_model="m", embed_dim=4, embed_max_chars=10))
    assert (e.model_name, e.dimension, e.prepare("x" * 50)) == ("m", 4, "x" * 10)
