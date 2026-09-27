"""Check what the Gemini API really does before adapters are written (step 5 verify-first).

Reads GEMINI_API_KEY from the environment and never prints it. Standard library only.
Run:  set -a && . ./.env && set +a && worker/.venv/bin/python worker/scripts/probe_gemini.py > gemini_probe.txt
"""

import json
import math
import os
import sys
import time
import urllib.error
import urllib.request

BASE = "https://generativelanguage.googleapis.com/v1beta"
KEY = os.environ.get("GEMINI_API_KEY", "")
if not KEY:
    sys.exit("GEMINI_API_KEY is not set")


def call(path: str, body: dict | None = None, timeout: int = 60):
    req = urllib.request.Request(
        f"{BASE}/{path}",
        data=json.dumps(body).encode() if body is not None else None,
        headers={"x-goog-api-key": KEY, "Content-Type": "application/json"},
        method="POST" if body is not None else "GET",
    )
    start = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw, status, headers = resp.read(), resp.status, dict(resp.headers)
    except urllib.error.HTTPError as e:
        raw, status, headers = e.read(), e.code, dict(e.headers)
    except Exception as e:  # network trouble
        return 0, {"error": {"message": f"{type(e).__name__}: {e}"}}, {}, 0.0
    ms = (time.monotonic() - start) * 1000
    try:
        return status, json.loads(raw), headers, ms
    except ValueError:
        return status, {"raw": raw[:300].decode("utf-8", "replace")}, headers, ms


def err_text(data) -> str:
    e = data.get("error", data) if isinstance(data, dict) else data
    return json.dumps(e)[:400]


def norm(v):
    return math.sqrt(sum(x * x for x in v))


def cos(a, b):
    return sum(x * y for x, y in zip(a, b)) / (norm(a) * norm(b))


def interesting(headers):
    keep = ("retry-after", "x-ratelimit", "ratelimit", "quota")
    return {k: v for k, v in headers.items() if any(p in k.lower() for p in keep)}


def section(title):
    print(f"\n=== {title}")


DOC_GO = "func (s *Scheduler) Run(ctx context.Context) error { for job := range s.queue { s.execute(job) } return nil }"
DOC_PY = "def parse_config(path):\n    with open(path) as f:\n        return yaml.safe_load(f)"
QUERY = "where does the scheduler execute queued jobs?"


def embed(model, text, task=None, dims=None):
    body = {"content": {"parts": [{"text": text}]}}
    if task:
        body["taskType"] = task
    if dims:
        body["outputDimensionality"] = dims
    status, data, headers, ms = call(f"models/{model}:embedContent", body)
    vec = (data.get("embedding") or {}).get("values") if isinstance(data, dict) else None
    return status, vec, data, headers, ms


# ---------------------------------------------------------------- model metadata
section("model metadata (embedding models)")
for name in ("gemini-embedding-001", "gemini-embedding-2"):
    status, data, _, _ = call(f"models/{name}")
    keys = ("name", "version", "inputTokenLimit", "outputTokenLimit", "supportedGenerationMethods")
    print(name, status, {k: data.get(k) for k in keys} if status == 200 else err_text(data))
    if status == 200:
        print("   other fields:", sorted(set(data) - set(keys)))

# ---------------------------------------------------------------- embeddings
for model in ("gemini-embedding-001", "gemini-embedding-2"):
    section(f"{model}: single embedContent")
    for label, task, dims in [
        ("document, 768 dims", "RETRIEVAL_DOCUMENT", 768),
        ("query, 768 dims", "RETRIEVAL_QUERY", 768),
        ("document, default dims", "RETRIEVAL_DOCUMENT", None),
        ("no task type, 768 dims", None, 768),
    ]:
        status, vec, data, headers, ms = embed(model, DOC_GO, task, dims)
        if vec:
            print(f"{label}: ok len={len(vec)} l2norm={norm(vec):.4f} first3={[round(x, 4) for x in vec[:3]]} {ms:.0f}ms")
        else:
            print(f"{label}: HTTP {status} {err_text(data)}")
        if interesting(headers):
            print("   headers:", interesting(headers))

    d1 = embed(model, DOC_GO, "RETRIEVAL_DOCUMENT", 768)[1]
    d2 = embed(model, DOC_PY, "RETRIEVAL_DOCUMENT", 768)[1]
    q = embed(model, QUERY, "RETRIEVAL_QUERY", 768)[1]
    if d1 and d2 and q:
        print(f"relevance check: cos(query, go scheduler)={cos(q, d1):.4f} vs cos(query, python yaml)={cos(q, d2):.4f} "
              f"-> {'OK' if cos(q, d1) > cos(q, d2) else 'WRONG ORDER'}")
        qs = embed(model, QUERY, None, 768)[1]
        if qs:
            print(f"   without task types: cos(query, go)={cos(qs, embed(model, DOC_GO, None, 768)[1]):.4f}")

# ---------------------------------------------------------------- batching
section("batch embedding (gemini-embedding-001)")
reqs = [
    {"model": "models/gemini-embedding-001", "content": {"parts": [{"text": t}]},
     "taskType": "RETRIEVAL_DOCUMENT", "outputDimensionality": 768}
    for t in (DOC_GO, DOC_PY, "x = 1")
]
status, data, headers, ms = call("models/gemini-embedding-001:batchEmbedContents", {"requests": reqs})
if status == 200 and data.get("embeddings"):
    print(f"batchEmbedContents: ok, {len(data['embeddings'])} embeddings, lens={[len(e['values']) for e in data['embeddings']]} {ms:.0f}ms")
else:
    print(f"batchEmbedContents: HTTP {status} {err_text(data)}")

body = {"requests": [dict(r) for r in reqs]}
big = {"requests": [dict(reqs[0]) for _ in range(101)]}
status, data, _, _ = call("models/gemini-embedding-001:batchEmbedContents", big)
print("batchEmbedContents with 101 requests:", status, "ok" if status == 200 else err_text(data))

# ---------------------------------------------------------------- input length
section("long input (gemini-embedding-001 documents limit 2048 tokens)")
long_text = "def f(x):\n    return x + 1\n" * 400   # about 4000+ tokens
status, vec, data, _, _ = embed("gemini-embedding-001", long_text, "RETRIEVAL_DOCUMENT", 768)
print(f"~{len(long_text)} chars:", f"ok len={len(vec)} (silently truncated?)" if vec else f"HTTP {status} {err_text(data)}")
status, data, _, _ = call("models/gemini-embedding-001:countTokens", {"contents": [{"parts": [{"text": long_text}]}]})
print("countTokens for that text:", data.get("totalTokens") if status == 200 else f"HTTP {status} {err_text(data)}")

# ---------------------------------------------------------------- burst (rate limit behaviour)
section("burst of 15 embedContent calls (gemini-embedding-001)")
codes = []
retry = None
for _ in range(15):
    status, vec, data, headers, _ = embed("gemini-embedding-001", "x = 1", "RETRIEVAL_DOCUMENT", 768)
    codes.append(status)
    if status == 429:
        retry = (interesting(headers), err_text(data))
print("statuses:", codes)
if retry:
    print("first 429:", retry)

# ---------------------------------------------------------------- generation
section("generateContent")
system = "Answer only from the context. Reply in one short sentence."
for model in ("gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-flash-latest"):
    for label, gen in [("default", {"temperature": 0.1}),
                       ("thinkingBudget=0", {"temperature": 0.1, "thinkingConfig": {"thinkingBudget": 0}})]:
        body = {
            "systemInstruction": {"parts": [{"text": system}]},
            "contents": [{"role": "user", "parts": [{"text": "Context: [1] main.go:1-3 starts the server.\nQuestion: Where is the server started? Cite like [1]."}]}],
            "generationConfig": gen,
        }
        status, data, headers, ms = call(f"models/{model}:generateContent", body)
        if status == 200:
            cand = (data.get("candidates") or [{}])[0]
            text = "".join(p.get("text", "") for p in cand.get("content", {}).get("parts", []))
            usage = data.get("usageMetadata", {})
            print(f"{model} [{label}]: ok {ms:.0f}ms finish={cand.get('finishReason')} text={text[:120]!r} usage={usage}")
        else:
            print(f"{model} [{label}]: HTTP {status} {err_text(data)}")
        if interesting(headers):
            print("   headers:", interesting(headers))

section("unknown model error shape")
status, data, _, _ = call("models/does-not-exist:generateContent", {"contents": [{"parts": [{"text": "hi"}]}]})
print(status, err_text(data))
print("\ndone")
