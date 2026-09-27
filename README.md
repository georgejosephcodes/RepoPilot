# RepoPilot

> AI-powered developer assistant for understanding and contributing to unfamiliar repositories.

**Ask questions about any public GitHub repository and get answers grounded in the actual code, with file and line citations you can verify.**

RepoPilot helps a developer go from "I don't understand this repository" to "I know where to look and what to change."

> Status: Phase 1 complete. All 9 steps are done and verified: scaffold and database, ingestion API, worker clone and scan, tree-sitter chunking, embeddings and storage, retrieval, answers with validated citations, a terminal UI, and a demo pass on three real repositories (15 of 15 questions correct or correctly refused, see [Phase 1 results and known gaps](#phase-1-results-and-known-gaps)). Docs: [ARCHITECTURE.md](ARCHITECTURE.md) (design), [RepoPilotPlan.md](RepoPilotPlan.md) (original outline), [RepoPilot.md](RepoPilot.md) (product spec).

---

## Table of contents

1. [What is RepoPilot](#what-is-repopilot)
2. [The problem](#the-problem)
3. [What it does](#what-it-does)
4. [What it does not do](#what-it-does-not-do)
5. [How it works in one minute](#how-it-works-in-one-minute)
6. [Tech stack and why each piece](#tech-stack-and-why-each-piece)
7. [Example session](#example-session)
8. [Roadmap](#roadmap)
9. [Repository layout](#repository-layout)
10. [Getting started](#getting-started)
11. [Configuration](#configuration)
12. [Phase 1 results and known gaps](#phase-1-results-and-known-gaps)
13. [Limits and known trade-offs](#limits-and-known-trade-offs)
14. [FAQ](#faq)

---

## What is RepoPilot

RepoPilot is a repository intelligence tool. You give it a GitHub URL. It downloads the code, breaks it into meaningful pieces (functions, classes, methods), turns each piece into a searchable representation, and stores it. When you ask a question in plain English, it finds the most relevant pieces, gives them to a language model, and returns an answer that cites the exact files and line ranges it used.

It is a **Retrieval-Augmented Generation (RAG)** system specialised for source code.

### Why not just paste code into ChatGPT or Claude?

- A real repository is far larger than any model's context window.
- A general model does not know the current state of an arbitrary repository. It guesses.
- Guesses look confident. RepoPilot forces answers to come from retrieved code and to say "not enough evidence" when the code does not answer the question.
- Citations let you check the answer in seconds instead of trusting it.

The language model is one component. The hard engineering is turning a large repository into an index that returns the right code for a question.

---

## The problem

Contributing to an unfamiliar open-source project costs hours before the first useful line of code:

| Pain | Example |
|---|---|
| Size | Thousands of files, no obvious starting point |
| Discoverability | You know the feature you want, not where it lives |
| Missing context | Issue says "add retry to HTTP client". Where is the client? Does retry already exist? Which tests cover it? |
| Context switching | Issues, PRs, source, docs, config, and tests live in different places |

RepoPilot attacks the first three in Phase 1 and the fourth in later phases.

---

## What it does

### Phase 1 (the MVP, first thing built)

- Accept a public GitHub repository URL.
- Clone it safely and index Python, TypeScript/JavaScript, and Go source plus Markdown docs.
- Split code along real syntax boundaries (functions, classes, methods) using tree-sitter, not arbitrary character counts.
- Answer natural-language questions about the repository.
- Return an answer plus citations: `path/to/file.go` lines `42-67`, with a snippet.
- Refuse to invent: if retrieved code does not support an answer, say so.
- Show indexing status: queued, indexing, ready, failed.
- Terminal-style web UI: type commands (`add`, `use`, `status`, `ask`, `show`) at a prompt and read text output, like a CLI. A real CLI with the same commands comes later.

### Later phases

| Phase | Adds |
|---|---|
| 2. Retrieval quality | Keyword (BM25-style) + vector hybrid search, reranking, query rewriting, an evaluation set with Recall@K and MRR, Redis query cache |
| 3. Contribution assistant | GitHub Issues and Pull Requests ingestion, "explain this issue in the context of this repo", relevant files and functions, similar past PRs |
| 4. Repo intelligence | Import/package dependency graph, call relationships where feasible, generated overview, suggested reading order |
| 5. Production | Incremental Git-diff indexing, real job queue, auth, rate limiting, Prometheus metrics, CI, performance tuning |

Details, scope, and verification gates for each phase are in [ARCHITECTURE.md](ARCHITECTURE.md#15-phased-delivery).

---

## What it does not do

Stating this up front keeps scope honest.

- It does not write or modify code in the target repository.
- It does not run the target repository's code. Ever. It only reads files.
- It does not claim an issue is "easy" or "hard". Later phases show evidence (files touched, existing related code) and let the contributor decide.
- It does not support private repositories in Phase 1.
- It does not guarantee correctness. It reduces the time to find and verify, and citations make verification cheap.
- It does not parse every language. Phase 1 supports Python, TypeScript/JavaScript, and Go. Other file types fall back to line-window chunks.

---

## How it works in one minute

Two separate paths share one database.

**Index path (slow, done once per repository):**

```
GitHub URL
  -> validate -> queue a job
  -> worker: clone (shallow) -> scan files -> parse with tree-sitter
  -> split into chunks (one function/class/method each)
  -> embed each chunk (text -> vector of 2048 numbers)
  -> store chunk text, metadata, and vector in Postgres (pgvector)
```

**Query path (fast, done per question):**

```
Question
  -> embed the question (same model as indexing)
  -> find the K closest chunk vectors in this repository (cosine similarity)
  -> build a prompt: numbered code blocks [1] [2] [3] ...
  -> LLM answers using only those blocks and cites [n]
  -> server validates every [n] against what was actually retrieved
  -> return answer + file:line citations + snippets
```

**Concept for RAG newcomers.** An embedding is a list of numbers that captures meaning. Text with similar meaning gets nearby vectors. "Where do we retry failed requests?" lands near a function called `retryWithBackoff` even though the words differ. Vector search finds nearby vectors quickly. The LLM then reads only those few chunks instead of the whole repository.

---

## Tech stack and why each piece

Full reasoning, alternatives considered, and rejection reasons are in [ARCHITECTURE.md](ARCHITECTURE.md#4-technology-decisions). Summary:

| Piece | Choice | Why |
|---|---|---|
| API server | **Go + Gin** | Typed, fast, easy concurrency, single static binary. Gin is the most widely used Go HTTP framework, so examples and middleware are plentiful. Handles the latency-sensitive query path. |
| Indexing worker | **Python** | Best ecosystem for parsing (tree-sitter bindings) and ML/embedding clients. Indexing is batch work where Python speed does not matter; the network calls to the embedding API dominate. |
| Database | **PostgreSQL + pgvector** | One database for relational data and vectors. Transactions, SQL filters (`WHERE repo_id = ...`), and no extra service to run. Search inside one repository is exact (every chunk of that repository is compared), which took 5 to 12 ms in the Phase 1 demo; an approximate HNSW index with a repository filter returned too few rows in testing. |
| Code parsing | **tree-sitter** | Produces a real syntax tree for many languages with one API. Lets us chunk at function/class boundaries and record exact line numbers. Tolerates broken code. |
| Embeddings | **OpenRouter (free model)**: `nvidia/nemotron-3-embed-1b:free`, 2048 dimensions | Free, OpenAI-compatible, large batches (up to 64 chunks per request). Limits: 20 requests per minute, 50 per day without purchased credit. Free models may retain requests, so Phase 1 handles public repositories only. |
| Answers | **Gemini**: `gemini-3.5-flash-lite` | Free tier, fast (1 to 2 s for most answers in the demo), and kept to the "cite only numbered blocks" rule in all 15 demo answers. |
| Job queue | **Postgres table + `FOR UPDATE SKIP LOCKED`** | Already have Postgres. Safe multi-worker claiming with no new infrastructure. Redis Streams arrive in Phase 5 only if load proves the need. |
| Cache | **Redis (Phase 2+)** | Not in Phase 1. Added when repeated queries and rate limits justify it. |
| Frontend | **Static terminal-style page (HTML/JS/CSS) served by Go** | Looks and works like a CLI: prompt, commands, scrolling text output. Plain files, no build step. It is a thin client over the REST API, so a future CLI reuses the same commands. React and xterm.js only if the UI grows enough to earn them. |
| Local infra | **Docker Compose** | One command to start Postgres with pgvector. |

Embeddings and answers sit behind interfaces (`Embedder`, `LLM`), so a provider swap touches one adapter file. Any OpenAI-compatible embeddings API works through the `EMBED_*` settings.

Guiding rule: **every technology must solve a problem we have now.** Redis, React, Prometheus, and RBAC from the original spec are deliberately deferred so Phase 1 stays finishable.

---

## Example session

The web UI is a terminal. This is what a session looks like:

```text
repopilot ~ (no repo) ▸ help
help [command]         list commands, or show usage for one
add <github-url>       add a repository and watch it being indexed
repos                  list repositories
use <id|owner/name>    select the active repository
status [id|owner/name] show status, commit, progress and errors
ask <question>         ask the active repository (the 'ask' word is optional)
show <n>               show the code behind citation [n] of the last answer
clear                  clear the screen (Ctrl+L too)

repopilot ~ (no repo) ▸ add https://github.com/pallets/itsdangerous
[queued] repo 203 pallets/itsdangerous
[cloning]
[embedding] 0/114 chunks
[embedding] 64/114 chunks
[ready] pallets/itsdangerous @ 672971d

repopilot ~ (no repo) ▸ use 203
using pallets/itsdangerous @ 672971d

repopilot ~ pallets/itsdangerous ▸ Where is the HMAC signature computed?
The HMAC signature is computed in the `HMACAlgorithm.get_signature` method using
`hmac.new` and returning `mac.digest()` [1].

sources:
  [1] src/itsdangerous/signer.py:48-64 HMACAlgorithm
type 'show <n>' to view a snippet
1.7s · 8 chunks · 2,391 in / 34 out tokens

repopilot ~ pallets/itsdangerous ▸ show 1
[1] src/itsdangerous/signer.py:48-64 (python, class HMACAlgorithm)
48 | class HMACAlgorithm(SigningAlgorithm):
49 |     """Provides signature generation using HMACs."""
   ...
61 |     def get_signature(self, key: bytes, value: bytes) -> bytes:
62 |         mac = hmac.new(key, msg=value, digestmod=self.digest_method)
63 |         return mac.digest()
```

The answer, citation and timing above are from the Phase 1 demo run (question `py-1` in [`docs/phase1/raw.json`](docs/phase1/raw.json)). Indexing this repository (19 files, 114 chunks) took about 10 seconds and 2 embedding requests.

Under the hood each command is a call to the REST API (`add` is `POST /api/repositories` plus status polling, an answer is `POST /api/repositories/:id/query`). The full command table and output formats are in [ARCHITECTURE.md](ARCHITECTURE.md#114-command-set-and-terminal-ui).

An unanswerable question (for example "How does the library connect to a Redis server to cache signatures?" against `itsdangerous`, which has no Redis code) returns `[refused]` and a sentence saying the retrieved code does not cover it, with no citations.

---

## Roadmap

Each phase ends with a verification gate. The next phase does not start until the gate passes.

| Phase | Goal | Gate |
|---|---|---|
| 1 | End-to-end Q&A with citations | URL in, status reaches `ready`, grounded answer, citations open the right lines, unanswerable question refused, tests green |
| 2 | Better retrieval, measured | Eval set exists first; hybrid + rerank beats Phase 1 baseline on Recall@K and MRR |
| 3 | Issues and PRs | Pick an issue, get relevant files and an explanation grounded in code |
| 4 | Architecture view | Dependency graph and reading order generated for a real repo |
| 5 | Production hardening | Incremental re-index, auth, metrics, CI |

---

## Repository layout

```
docker-compose.yml          Postgres 17 + pgvector; applies migrations/ on first start
migrations/001_init.sql     Schema
.env.example                Every setting, with defaults
api/                        Go API server
  cmd/server/               The API (serves the web UI too)
  cmd/search-debug/         Prints raw retrieval results for a question, for debugging
  internal/httpapi/         Gin router, handlers, rate limits, error shape
  internal/repos/           URL validation, repository records, job enqueue
  internal/retrieval/       Repository-scoped vector search (SQL)
  internal/rag/             Prompt builder, citation parser and validator, error mapping
  internal/embed/           Embedder interface, OpenAI-compatible adapter (OpenRouter), question cache
  internal/llm/             LLM interface, Gemini adapter
  web/                      Terminal-style UI embedded in the binary (index.html, terminal.css, core.js, terminal.js, selftest.js)
worker/                     Python indexing worker
  repopilot_worker/         main, jobs, clone, scan, parse, symbols, chunk, embed, store, pipeline
  scripts/                  run_demo.py, verify_citations.py, provider probes
  tests/
testdata/                   Fixtures shared by the Go and Python tests (URL cases, embedding contract)
docs/phase1/                Demo questions, raw answers, mechanical checks, verdicts
```

---

## Getting started

Prerequisites: Docker (with Compose), Go 1.26 or newer, Python 3 (developed and tested on 3.14), `git`, an
[OpenRouter](https://openrouter.ai) API key for embeddings, and a [Gemini](https://aistudio.google.com) API key for
answers. Both keys work on free tiers.

```bash
cp .env.example .env              # set EMBED_API_KEY (OpenRouter) and GEMINI_API_KEY
docker compose up -d              # Postgres + pgvector on port 5433; creates the schema on first start

# worker setup (once)
cd worker && python3 -m venv .venv && .venv/bin/pip install -r requirements.txt && cd ..
```

Run the API and the worker in two terminals, from the repository root. Each one loads `.env` first:

```bash
# terminal 1: API and web UI on http://localhost:8080
cd api && set -a && . ../.env && set +a && go run ./cmd/server

# terminal 2: worker, picks up indexing jobs (add --once to process one job and exit)
cd worker && set -a && . ../.env && set +a && .venv/bin/python -m repopilot_worker.main
```

Open http://localhost:8080, type `add https://github.com/<owner>/<repo>`, wait for `[ready]`, then ask a question.
A repository stays `queued` until the worker runs.

Tests, from the repository root (the database tests create and drop their own throwaway database):

```bash
(cd api && set -a && . ../.env && set +a && go vet ./... && TEST_DATABASE_URL=$DATABASE_URL go test -count=1 ./...)
(cd worker && set -a && . ../.env && set +a && TEST_DATABASE_URL=$DATABASE_URL .venv/bin/pytest -q)
```

---

## Configuration

All settings live in `.env` (copy `.env.example`, which lists every one with its default). The main ones:

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | Postgres connection string (`POSTGRES_*` configure the Compose container) |
| `API_ADDR` | API listen address, default `:8080` |
| `EMBED_BASE_URL`, `EMBED_API_KEY`, `EMBED_MODEL` | Embedding provider, any OpenAI-compatible API. Default: OpenRouter, `nvidia/nemotron-3-embed-1b:free` |
| `EMBED_DIM` | Vector size, `2048` for the default model. Must match the schema |
| `EMBED_RPM`, `EMBED_BATCH_SIZE`, `EMBED_RESERVE_REQUESTS` | Embedding pacing, batch size, and daily requests the worker leaves unused for questions |
| `LLM_API_KEY` (or `GEMINI_API_KEY`), `LLM_MODEL`, `LLM_BASE_URL` | Answer model. Default: Gemini `gemini-3.5-flash-lite` |
| `LLM_TEMPERATURE`, `LLM_MAX_OUTPUT_TOKENS`, `LLM_THINKING_LEVEL`, `LLM_TIMEOUT_SEC` | Answer model behaviour |
| `RETRIEVAL_TOP_K` | Chunks retrieved per question, default `8` |
| `CONTEXT_BUDGET_CHARS`, `MAX_QUESTION_CHARS` | Prompt size cap and question length cap |
| `RATE_LIMIT_PER_MIN`, `QUERY_RATE_LIMIT_PER_MIN` | Per-IP limits on adding repositories and on questions |
| `CLONE_TIMEOUT_SEC`, `MAX_REPO_MB`, `MAX_FILES`, `MAX_FILE_KB` | Indexing safety caps |
| `CHUNK_MAX_LINES`, `CHUNK_OVERLAP_LINES`, `CHUNK_MIN_GAP_LINES` | Chunking rules |

Without both keys the API still starts, but question answering is disabled and says so in its log.

---

## Phase 1 results and known gaps

Fifteen questions were run against three real, unmodified public repositories (Go, TypeScript, Python), one flow
question, two location questions, one exact-identifier question, and one deliberately unanswerable question each.
Every citation the system produced was checked against the real file before being counted, and every unanswerable
question was checked by searching the repository so a refusal was the only correct result.

**Result: 15 of 15 correct or correctly refused.** No wrong answer and no fabricated citation. Full question set,
raw responses, mechanical checks, and a human verdict with notes for every answer: [`docs/phase1/`](docs/phase1/).

This is a small, hand-picked question set, not a benchmark, and it should not be read as "retrieval is solved":

- All 15 answers were found within the top 8 retrieved chunks. Earlier testing (during development, not part of this
  run) found a case where the two best chunks ranked 6th and 7th and were only saved by using K=8; a smaller K, a
  larger repository, or a less distinctive identifier could still miss.
- Every answer here used the same embedding and generation model. A different provider could rank differently on the
  same questions.
- The three repositories are mid-size, well-documented, single-purpose libraries. A larger or messier codebase is
  untested.
- Retrieval is vector-only; no keyword search or reranking was used.

These 15 questions are the seed of the Phase 2 evaluation set, which is expected to grow and include cases chosen to
fail, not only cases expected to pass.

---

## Limits and known trade-offs

- **Free-tier quota.** Embeddings use OpenRouter's free tier: 50 requests per day and 20 per minute. Each question costs 1 request and each new repository about 1 per 64 chunks, so a large repository can use most of a day's budget. Before embedding, the worker checks the remaining budget and fails the job with a clear message when it would not fit; vectors already paid for are cached, so a retry after the daily reset does not pay for them again. Answers use Gemini's free tier, which has a separate, larger limit. Batching, pacing, and backoff keep usage inside both.
- **Pure vector search misses exact identifiers.** Searching for `ErrConfigMissing` may miss it. Phase 2 adds keyword search to fix this. Phase 1 accepts the gap and the eval set will quantify it.
- **Chunk boundaries matter.** A function split across chunks, or a chunk missing its callers, can produce a partial answer. Large-symbol splitting with overlap reduces but does not remove this.
- **Answers are model output.** Citation validation ensures cited chunks were really retrieved. It does not prove the model interpreted them correctly. That is why snippets and line ranges are returned.
- **Full re-index on change.** Phase 1 re-indexes the whole repository. Incremental indexing is Phase 5.
- **Public repositories only** in Phase 1.

---

## FAQ

**Why a separate Python worker if Go is the backend?**
Indexing needs tree-sitter and embedding clients, which are more mature in Python, and it is batch work where language speed is irrelevant. Go keeps the low-latency query path. They communicate only through the database.

**Why not a hosted vector database (Pinecone, Weaviate, Qdrant)?**
Another service to run and keep consistent with relational data. pgvector gives vectors, metadata, and transactions in the one database we already need. It handles this project's scale comfortably.

**Why not fixed-size text chunks?**
They cut functions in half and lose the symbol name and line range. Syntax-aware chunks embed better and produce exact citations.

**Why cite `[n]` markers instead of asking the model for file paths?**
Models invent paths. Numbered markers map to chunks the server actually retrieved, so the server can verify every citation mechanically.

**Can I swap OpenRouter or Gemini for another provider?**
Yes. Any OpenAI-compatible embeddings API works by changing the `EMBED_*` settings. Otherwise, implement the `Embedder` and/or `LLM` interface in one file. One rule: never mix embedding models within a repository. The model name is stored per chunk and checked at query time.

**Why does the web UI look like a terminal?**
The users are developers and a CLI is planned. Building the interaction as commands and text output now means the web UI and the later CLI share one command set, one output format, and one API. It also keeps Phase 1 simple: no frontend framework or build step.

**When does the CLI arrive?**
It is not scheduled yet. It will be a standalone client of the same REST API with the same commands. Its config, auth, and exit-code details are decided then.
