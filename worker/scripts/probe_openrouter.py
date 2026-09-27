"""Check what OpenRouter's free embedding models really do.

Reads OPENROUTER_API_KEY from the environment and never prints it. Standard library only.
Uses about 15 requests, and free models allow 50 per day without purchased credit.
Run:  set -a && . ./.env && set +a && worker/.venv/bin/python worker/scripts/probe_openrouter.py > openrouter_probe.txt 2>&1
"""

import json
import math
import os
import sys
import time
import urllib.error
import urllib.request

BASE = "https://openrouter.ai/api/v1"
KEY = os.environ.get("OPENROUTER_API_KEY", "")
if not KEY:
    sys.exit("OPENROUTER_API_KEY is not set")

calls = 0


def call(path, body=None, auth=True, timeout=90):
    global calls
    headers = {"Content-Type": "application/json"}
    if auth:
        headers["Authorization"] = f"Bearer {KEY}"
    req = urllib.request.Request(f"{BASE}/{path}", data=json.dumps(body).encode() if body is not None else None,
                                 headers=headers, method="POST" if body is not None else "GET")
    if body is not None:
        calls += 1
    start = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            raw, status, hdrs = r.read(), r.status, dict(r.headers)
    except urllib.error.HTTPError as e:
        raw, status, hdrs = e.read(), e.code, dict(e.headers)
    except Exception as e:
        return 0, {"error": f"{type(e).__name__}: {e}"}, {}, 0.0
    ms = (time.monotonic() - start) * 1000
    try:
        return status, json.loads(raw), hdrs, ms
    except ValueError:
        return status, {"raw": raw[:300].decode("utf-8", "replace")}, hdrs, ms


def norm(v):
    return math.sqrt(sum(x * x for x in v))


def cos(a, b):
    return sum(x * y for x, y in zip(a, b)) / (norm(a) * norm(b))


def hdr(h):
    return {k: v for k, v in h.items() if any(p in k.lower() for p in ("ratelimit", "rate-limit", "retry", "remaining", "reset"))}


def short(d, n=350):
    return json.dumps(d)[:n]


def vectors(data):
    try:
        return [item["embedding"] for item in sorted(data["data"], key=lambda i: i.get("index", 0))]
    except Exception:
        return None


DOC_GO = "func (s *Scheduler) Run(ctx context.Context) error { for job := range s.queue { s.execute(job) } return nil }"
DOC_PY = "def parse_config(path):\n    with open(path) as f:\n        return yaml.safe_load(f)"
QUERY = "where does the scheduler execute queued jobs?"
FREE = ["nvidia/nemotron-3-embed-1b:free", "nvidia/llama-nemotron-embed-vl-1b-v2:free", "liquid/lfm-2.5-embedding-350m:free"]

print("=== key status (label redacted)")
status, data, h, _ = call("key")
if status == 200:
    d = data.get("data", data)
    print({k: v for k, v in d.items() if k != "label"})
else:
    print(status, short(data))
print("headers:", hdr(h))

print("\n=== embedding model catalogue (free entries, all fields)")
status, data, _, _ = call("embeddings/models", auth=False)
for m in (data.get("data") or []):
    if str(m.get("id", "")).endswith(":free"):
        print(short(m, 900))

print("\n=== free chat models (id, context, supports)")
status, data, _, _ = call("models", auth=False)
rows = []
for m in (data.get("data") or []):
    if str(m.get("id", "")).endswith(":free"):
        rows.append((m["id"], m.get("context_length"), [p for p in (m.get("supported_parameters") or []) if p in ("tools", "structured_outputs", "reasoning", "response_format", "temperature")]))
for r in sorted(rows, key=lambda r: -(r[1] or 0)):
    print(*r)

for model in FREE:
    print(f"\n=== {model}")
    docs = [DOC_GO, DOC_PY, QUERY]
    status, data, h, ms = call("embeddings", {"model": model, "input": docs})
    vecs = vectors(data) if status == 200 else None
    if vecs and len(vecs) == 3:
        print(f"batch of 3, default dims: ok len={len(vecs[0])} l2norm={norm(vecs[0]):.4f} {ms:.0f}ms usage={data.get('usage')} model={data.get('model')} provider={data.get('provider')}")
        print(f"relevance: cos(query, go scheduler)={cos(vecs[2], vecs[0]):.4f} vs cos(query, python yaml)={cos(vecs[2], vecs[1]):.4f} -> {'OK' if cos(vecs[2], vecs[0]) > cos(vecs[2], vecs[1]) else 'WRONG ORDER'}")
    else:
        print(f"batch of 3: HTTP {status} {short(data)}")
        continue
    print("headers:", hdr(h))

    status, data, _, ms = call("embeddings", {"model": model, "input": [DOC_GO], "dimensions": 768})
    v = vectors(data) if status == 200 else None
    print("dimensions=768:", f"ok len={len(v[0])} l2norm={norm(v[0]):.4f}" if v else f"HTTP {status} {short(data)}")
    if v and vecs and len(v[0]) == 768 and len(vecs[0]) > 768:
        print("   first values equal to truncation of default vector:", all(abs(a - b) < 1e-4 for a, b in zip(v[0][:5], vecs[0][:5])))

    if model == FREE[0]:
        for label, itype in (("input_type=search_document", "search_document"), ("input_type=search_query", "search_query")):
            status, data, _, _ = call("embeddings", {"model": model, "input": [DOC_GO], "input_type": itype})
            v = vectors(data) if status == 200 else None
            print(f"{label}:", f"ok first3={[round(x, 4) for x in v[0][:3]]}" if v else f"HTTP {status} {short(data)}")
        print(f"   default first3={[round(x, 4) for x in vecs[0][:3]]}")

        for n in (16, 64, 256):
            inputs = [f"def f{i}(x):\n    return x + {i}\n" for i in range(n)]
            status, data, _, ms = call("embeddings", {"model": model, "input": inputs})
            v = vectors(data) if status == 200 else None
            print(f"batch of {n}:", f"ok {len(v)} vectors {ms:.0f}ms usage={data.get('usage')}" if v else f"HTTP {status} {short(data)}")
            if not v:
                break

        long_text = "def f(x):\n    return x + 1\n" * 1200   # about 32,000 characters
        status, data, _, ms = call("embeddings", {"model": model, "input": [long_text]})
        v = vectors(data) if status == 200 else None
        print(f"long input ({len(long_text)} chars):", f"ok len={len(v[0])} usage={data.get('usage')} {ms:.0f}ms" if v else f"HTTP {status} {short(data)}")

print(f"\nPOST requests made: {calls}")
print("done")
