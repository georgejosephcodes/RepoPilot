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
10. [Getting started (planned)](#getting-started-planned)
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
| Database | **PostgreSQL + pgvector** | One database for relational data and vectors. Transactions, SQL filters (`WHERE repo_id = ...`), and no extra service to run. HNSW index gives fast approximate nearest-neighbor search at this scale. |
| Code parsing | **tree-sitter** | Produces a real syntax tree for many languages with one API. Lets us chunk at function/class boundaries and record exact line numbers. Tolerates broken code. |
| Embeddings and LLM | **OpenRouter (free models)**: `nvidia/nemotron-3-embed-1b:free` for embeddings; generation model chosen at step 7 | Free, OpenAI-compatible, large embedding batches. Both sit behind interfaces (`Embedder`, `LLM`) so a provider swap touches one adapter file. Limits: 20 requests per minute, 50 per day without purchased credit. Free models may retain requests, so Phase 1 handles public repositories only. |
| Job queue | **Postgres table + `FOR UPDATE SKIP LOCKED`** | Already have Postgres. Safe multi-worker claiming with no new infrastructure. Redis Streams arrive in Phase 5 only if load proves the need. |
| Cache | **Redis (Phase 2+)** | Not in Phase 1. Added when repeated queries and rate limits justify it. |
| Frontend | **Static terminal-style page (HTML/JS/CSS) served by Go** | Looks and works like a CLI: prompt, commands, scrolling text output. Plain files, no build step. It is a thin client over the REST API, so a future CLI reuses the same commands. React and xterm.js only if the UI grows enough to earn them. |
| Local infra | **Docker Compose** | One command to start Postgres with pgvector. |

Guiding rule: **every technology must solve a problem we have now.** Redis, React, Prometheus, and RBAC from the original spec are deliberately deferred so Phase 1 stays finishable.

---

## Example session

The web UI is a terminal. This is what a session looks like:

```text
repopilot ~ (no repo) ▸ help
commands: help, add, repos, use, status, ask, show, clear

repopilot ~ (no repo) ▸ add https://github.com/example/project
[queued]     repo 7 example/project
[cloning]    repo 7
[parsing]    310 files
[embedding]  120/310 files
[ready]      example/project @ abc1234

repopilot ~ (no repo) ▸ use 7
using example/project @ abc1234

repopilot ~ example/project ▸ where is authentication middleware applied?
Authentication is a Gin middleware [1]. It is attached to the /api group in
the router setup [2]. Token parsing happens in ParseToken [3].

sources:
  [1] internal/auth/middleware.go:18-54   AuthMiddleware
  [2] internal/api/routes.go:12-40        RegisterRoutes
  [3] internal/auth/token.go:9-33         ParseToken
type 'show <n>' to view a snippet

repopilot ~ example/project ▸ show 1
[1] internal/auth/middleware.go:18-54 (go, function AuthMiddleware)
   18 | func AuthMiddleware(cfg Config) gin.HandlerFunc {
   19 |     return func(c *gin.Context) {
   ...
```

Under the hood each command is a call to the REST API (`add` is `POST /api/repositories` plus status polling, an answer is `POST /api/repositories/:id/query`). The full command table and output formats are in [ARCHITECTURE.md](ARCHITECTURE.md#114-command-set-and-terminal-ui).

An unanswerable question ("How does the billing system handle refunds?" in a repo with no billing) must return an answer stating the retrieved code does not cover it, with no fabricated citations.

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

Planned layout (Phase 1):

```
docker-compose.yml        Postgres + pgvector (later: api, worker)
migrations/001_init.sql   Schema
api/                      Go API server
  cmd/server/main.go
  internal/httpapi/       Gin router and handlers
  internal/repos/         URL validation, repo CRUD, job enqueue
  internal/retrieval/     Vector search (SQL)
  internal/rag/           Prompt builder, citation parser and validator
  internal/embed/         Embedder interface + Gemini adapter
  internal/llm/           LLM interface + Gemini adapter
worker/                   Python indexing worker
  repopilot_worker/        main, jobs, clone, scan, parse, chunk, embed, db
  tests/
api/web/                  Terminal-style UI embedded in the API binary (index.html, terminal.css, core.js, terminal.js, selftest.js)
.env.example
```

---

## Getting started (planned)

Not runnable yet. Target flow once Phase 1 step 1 is done:

```bash
cp .env.example .env            # add EMBED_API_KEY (OpenRouter key)
docker compose up -d            # Postgres + pgvector
# apply migrations/001_init.sql
cd api && go run ./cmd/server   # API on :8080
cd worker && python -m repopilot_worker.main
open http://localhost:8080
```

Prerequisites: Docker, Go (version pinned at scaffold time), Python 3.11+, `git`, a Gemini API key.

---

## Configuration

Planned environment variables (final list fixed at scaffold time):

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | Postgres connection string |
| `EMBED_BASE_URL`, `EMBED_API_KEY`, `EMBED_MODEL` | Embedding provider (OpenRouter by default). Free models may retain requests; Phase 1 indexes public repositories only |
| `GEMINI_API_KEY` | Optional, only if the Gemini adapter is used |
| `GEMINI_LLM_MODEL` | Generation model name (verify against current docs) |
| `GEMINI_EMBED_MODEL` | Embedding model name (verify against current docs) |
| `EMBED_DIM` | Vector size. 2048 for the Phase 1 model. Must match the schema |
| `API_ADDR` | API listen address |
| `CLONE_TIMEOUT_SEC` | Kill a hung `git clone` |
| `MAX_REPO_MB` / `MAX_FILES` / `MAX_FILE_KB` | Indexing size caps |
| `RETRIEVAL_TOP_K` | Chunks sent to the LLM per question |

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

- **Free-tier quota.** Embeddings and generation share Gemini free-tier limits. Phase 1 mitigates with batching, backoff, and small demo repos. Large repos may be slow or partially indexed until quota allows.
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

**Can I swap Gemini for another provider?**
Yes. Implement the `Embedder` and/or `LLM` interface in one file. One rule: never mix embedding models within a repository. The model name is stored per chunk and checked at query time.

**Why does the web UI look like a terminal?**
The users are developers and a CLI is planned. Building the interaction as commands and text output now means the web UI and the later CLI share one command set, one output format, and one API. It also keeps Phase 1 simple: no frontend framework or build step.

**When does the CLI arrive?**
After Phase 1 is verified. It will be a standalone client of the same REST API with the same commands. Its config, auth, and exit-code details are decided then.
