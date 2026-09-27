# RepoPilot

> **AI-powered developer assistant for understanding and contributing to unfamiliar repositories.**

## 1. Overview

Contributing to a large open-source project can be difficult for developers who are unfamiliar with the repository.

A typical contributor may need to spend hours understanding:

* Repository structure
* Architecture and module relationships
* Important entry points
* Request and data flows
* Existing implementations
* GitHub issues and pull requests
* Relevant documentation
* Tests and configuration
* Which files are likely relevant to an issue

**RepoPilot** addresses this problem by creating an AI-powered knowledge layer over an open-source repository.

A user provides a GitHub repository URL. RepoPilot analyzes the repository's source code, documentation, issues, pull requests, and repository metadata. It then allows the user to ask repository-specific questions and helps them understand potential contribution opportunities.

The system combines:

* Code parsing
* Repository indexing
* Lexical search
* Vector search
* Retrieval-Augmented Generation (RAG)
* GitHub API integration
* Code-aware chunking
* LLM-based reasoning
* Source-level citations

The goal is not to replace the developer's understanding of the codebase, but to **reduce the time required to navigate and understand an unfamiliar repository.**

---

# 2. Problem Statement

When developers want to contribute to a large open-source repository, they often encounter the following problems:

### Repository size

Large repositories can contain thousands of files and hundreds of thousands of lines of code.

### Poor discoverability

A contributor may know what they want to change but not know where the relevant implementation exists.

### Context switching

Understanding an issue may require jumping between:

* GitHub Issues
* Pull Requests
* Source code
* Documentation
* Commit history
* Configuration
* Tests

### Missing context

An issue such as:

> "Add retry support to the HTTP client"

does not necessarily tell a new contributor:

* Where HTTP requests are executed
* Whether retry logic already exists
* Which interfaces are involved
* Which configuration controls the client
* Which tests should be modified

### Existing AI limitations

General-purpose LLMs have broad programming knowledge, but they do not inherently know the current structure and implementation details of an arbitrary GitHub repository.

RepoPilot retrieves the relevant repository context before generating an answer.

---

# 3. Goals

RepoPilot aims to provide the following capabilities.

## 3.1 Repository Onboarding

Given a GitHub repository, generate an overview containing:

* Primary languages
* Repository structure
* Important directories
* Entry points
* Major modules
* Dependencies
* Configuration files
* Test structure
* CI/CD configuration
* High-level architecture

---

## 3.2 Repository Q&A

Allow developers to ask questions such as:

> Where does authentication happen?

> How does an HTTP request flow through this project?

> Where are database queries executed?

> Which module handles caching?

> What happens when a job fails?

Answers should be grounded in the repository and include source references.

---

## 3.3 Issue Understanding

Given a GitHub issue, RepoPilot should retrieve relevant:

* Issue description
* Comments
* Source files
* Functions/classes
* Documentation
* Related issues
* Pull requests
* Commits

The system should explain the issue in the context of the repository.

---

## 3.4 Contribution Discovery

Help users discover potentially suitable issues based on:

* GitHub issue labels
* Issue descriptions
* Repository areas involved
* Number of affected files
* Existing implementations
* Required technologies
* Historical contribution patterns

The system should present these as **estimated contribution characteristics**, not guaranteed difficulty ratings.

---

## 3.5 Code Navigation

For a natural-language question, identify relevant:

* Files
* Classes
* Functions
* Interfaces
* Modules
* Configuration
* Tests

Example:

> Where should I modify the code to add request retries?

Response:

```text
Relevant files:

1. client/client.go
   Handles HTTP request execution.

2. middleware/retry.go
   Contains existing retry abstraction.

3. config/client.go
   Contains client configuration.

Likely execution path:

Client.Do()
    ↓
Request.Execute()
    ↓
RetryMiddleware
    ↓
HTTP Transport
```

---

## 3.6 Pull Request Understanding

Given a pull request, explain:

* What changed
* Why it changed
* Which components are affected
* How the implementation works
* Related issues
* Important architectural implications

---

## 3.7 Source-Level Citations

Answers should provide references to the actual repository.

Example:

```text
src/client/client.go
Lines 42–67
```

This allows developers to verify the generated explanation.

---

# 4. Core User Flow

```text
User
 │
 │ GitHub repository URL
 ▼
RepoPilot
 │
 ▼
Repository ingestion
 │
 ├── Source code
 ├── Documentation
 ├── Issues
 ├── Pull requests
 └── Repository metadata
 │
 ▼
Repository analysis
 │
 ├── Code parsing
 ├── Dependency analysis
 ├── Semantic chunking
 └── Metadata extraction
 │
 ▼
Indexing
 │
 ├── Lexical index
 └── Vector index
 │
 ▼
User asks question
 │
 ▼
Query processing
 │
 ▼
Hybrid retrieval
 │
 ├── BM25 / lexical search
 └── Vector search
 │
 ▼
Reranking
 │
 ▼
Context construction
 │
 ▼
LLM
 │
 ▼
Answer + citations
```

---

# 5. High-Level Architecture

```text
                         ┌─────────────────────┐
                         │    React Frontend   │
                         └──────────┬──────────┘
                                    │
                                    ▼
                         ┌─────────────────────┐
                         │      Go API         │
                         │      Server         │
                         └──────────┬──────────┘
                                    │
             ┌──────────────────────┼──────────────────────┐
             │                      │                      │
             ▼                      ▼                      ▼
       ┌───────────┐          ┌───────────┐          ┌───────────┐
       │   Auth    │          │ Repository│          │  Query    │
       │  Service  │          │  Service  │          │  Service  │
       └───────────┘          └─────┬─────┘          └─────┬─────┘
                                    │                      │
                                    ▼                      ▼
                             ┌─────────────┐       ┌─────────────┐
                             │   GitHub    │       │  Retrieval  │
                             │     API    │       │   Engine    │
                             └─────────────┘       └──────┬──────┘
                                                          │
                                             ┌────────────┴────────────┐
                                             │                         │
                                             ▼                         ▼
                                      Lexical Search             Vector Search
                                             │                         │
                                             └────────────┬────────────┘
                                                          ▼
                                                     Reranker
                                                          │
                                                          ▼
                                                 Context Builder
                                                          │
                                                          ▼
                                                         LLM
                                                          │
                                                          ▼
                                                  Answer + Sources


                     ┌──────────────────────────────────┐
                     │          Data Layer               │
                     │                                  │
                     │ PostgreSQL + pgvector            │
                     │ Redis                            │
                     └──────────────────────────────────┘
```

---

# 6. Technology Stack

## Frontend

* React
* TypeScript
* Tailwind CSS

> Phase 1 note: the UI is a static, terminal-style web page (commands like `add`, `use`, `ask`, `show`) in plain HTML/JS. A CLI with the same commands is planned later. React/TypeScript/Tailwind are deferred until a later phase needs them. See `ARCHITECTURE.md` section 11.4.

Responsibilities:

* Repository submission
* Repository dashboard
* Architecture visualization
* Chat interface
* Issue explorer
* PR explorer
* Source citations
* Repository file navigation

---

## Backend

* Go
* Gin or Chi
* REST APIs

Responsibilities:

* Authentication
* Repository management
* GitHub integration
* Search
* RAG orchestration
* Query processing
* LLM integration
* Citation generation

Go is used to provide a strongly typed, concurrent backend suitable for handling repository indexing and API workloads.

---

## Database

### PostgreSQL

Used for:

* Users
* Repositories
* Repository versions
* Files
* Code symbols
* Documents
* Issues
* Pull requests
* Conversations
* Queries
* Feedback
* Indexing jobs

### pgvector

Used for:

* Code embeddings
* Documentation embeddings
* Issue embeddings
* PR embeddings

---

## Cache

### Redis

Used for:

* Query caching
* Embedding caching
* Rate limiting
* Repository indexing state
* Temporary job state

---

## AI Layer

### LLM

Used for:

* Repository explanations
* Issue explanations
* Architecture summaries
* Code reasoning
* Contribution guidance

### Embedding Model

Used to convert code/documentation into vector representations for semantic retrieval.

### Reranker

Used to improve retrieval quality by ranking candidate code/document chunks according to the user's query.

---

## Repository Analysis

### GitHub API

Used for:

* Repository metadata
* Issues
* Pull requests
* Comments
* Commit information
* Repository contents

### Git

Used to clone repositories for local analysis.

### Tree-sitter

Used for syntax-aware code parsing.

Instead of splitting code arbitrarily by character count, RepoPilot identifies semantic structures such as:

* Functions
* Classes
* Methods
* Interfaces
* Structs
* Modules

This produces higher-quality retrieval chunks.

---

## Infrastructure

* Docker
* Docker Compose
* GitHub Actions
* Linux
* Prometheus
* Grafana
* Structured logging

---

# 7. Repository Ingestion Pipeline

When a repository is added:

```text
GitHub URL
    │
    ▼
Validate repository
    │
    ▼
Clone repository
    │
    ▼
Scan repository
    │
    ├── Source files
    ├── Documentation
    ├── Configuration
    └── Tests
    │
    ▼
Parse source code
    │
    ▼
Extract symbols
    │
    ▼
Generate semantic chunks
    │
    ▼
Extract metadata
    │
    ▼
Generate embeddings
    │
    ▼
Store in PostgreSQL + pgvector
```

---

# 8. Code Chunking

Traditional RAG systems often split text into fixed-size chunks.

For example:

```text
Every 500 tokens
```

This is not ideal for source code.

RepoPilot uses semantic code chunking.

Example:

```go
func (s *Scheduler) Run(ctx context.Context) error {
    ...
}
```

is stored as a semantic unit:

```text
File:
scheduler/scheduler.go

Symbol:
Scheduler.Run

Type:
Function

Language:
Go

Start line:
42

End line:
91
```

Metadata:

```json
{
  "repository": "example/project",
  "file": "scheduler/scheduler.go",
  "symbol": "Scheduler.Run",
  "type": "function",
  "language": "go",
  "start_line": 42,
  "end_line": 91
}
```

---

# 9. Hybrid Retrieval

RepoPilot does not rely exclusively on vector search.

It combines:

```text
                    User Query
                         │
              ┌──────────┴──────────┐
              ▼                     ▼
        Lexical Search        Vector Search
           BM25                Embeddings
              │                     │
              └──────────┬──────────┘
                         ▼
                      Merge
                         │
                         ▼
                     Reranker
                         │
                         ▼
                   Top-K Results
```

### Why hybrid retrieval?

Lexical search is useful for exact technical terms such as:

```text
Scheduler
HTTPClient
POST /users
401
Redis
RetryPolicy
```

Vector search is useful for semantic queries such as:

> "Where does the application decide whether a failed job should be retried?"

Combining both improves retrieval robustness.

---

# 10. RAG Pipeline

For a user query:

> "Where should I implement retry support?"

RepoPilot performs:

```text
User Query
    │
    ▼
Query Analysis
    │
    ▼
Candidate Retrieval
    │
    ├── Code
    ├── Documentation
    ├── Issues
    └── Pull Requests
    │
    ▼
Hybrid Search
    │
    ▼
Reranking
    │
    ▼
Relevant Context
    │
    ▼
Prompt Construction
    │
    ▼
LLM
    │
    ▼
Answer
    │
    ▼
Source Citations
```

The LLM should be instructed to distinguish between:

* Information directly supported by repository evidence
* Reasonable inference
* Information that cannot be determined

If sufficient evidence is unavailable, the assistant should say so rather than inventing repository details.

---

# 11. Example Query

## User

> How does authentication work in this repository?

## Retrieval

The system may retrieve:

```text
auth/middleware.go
auth/token.go
config/auth.go
api/routes.go
tests/auth_test.go
```

## Generated answer

```text
Authentication is implemented as middleware.

The request first enters the HTTP router defined in
api/routes.go.

The authentication middleware in
auth/middleware.go validates the token.

Token parsing is implemented in
auth/token.go.

The authentication configuration is loaded from
config/auth.go.

Relevant flow:

HTTP Request
    ↓
Router
    ↓
Auth Middleware
    ↓
Token Validation
    ↓
Handler
```

Sources:

```text
api/routes.go
auth/middleware.go
auth/token.go
config/auth.go
```

---

# 12. Issue Analysis Pipeline

When a user selects an issue:

```text
GitHub Issue
      │
      ▼
Issue Description
      │
      ├── Issue comments
      ├── Related PRs
      └── Related commits
      │
      ▼
Extract technical concepts
      │
      ▼
Search repository
      │
      ├── Relevant files
      ├── Functions
      ├── Tests
      └── Documentation
      │
      ▼
RAG
      │
      ▼
Issue Explanation
```

---

# 13. Contribution Assistant

Example:

```text
Issue #482

Add retry support to HTTP requests.
```

RepoPilot analyzes the repository and presents:

```text
Issue summary

The project currently performs HTTP requests through
client/transport.go.

Relevant components:

client/client.go
client/transport.go
middleware/request.go

Existing retry-related code:

middleware/retry.go

Potential implementation area:

client/transport.go

Relevant tests:

client/transport_test.go
```

The system should clearly distinguish **repository evidence** from AI-generated suggestions.

---

# 14. Repository Architecture Analysis

RepoPilot can build a dependency graph using:

* Import relationships
* Package relationships
* Function calls where available
* Configuration
* Framework conventions

Example:

```text
                 API
                  │
                  ▼
             Controller
                  │
                  ▼
              Service
             /       \
            ▼         ▼
      Repository     Cache
            │
            ▼
        PostgreSQL
```

This graph can be displayed in the frontend.

---

# 15. GitHub Issue Discovery

RepoPilot can retrieve open issues using the GitHub API.

Potential filters:

```text
good first issue
help wanted
documentation
bug
enhancement
```

The system can then analyze each issue against the repository.

For example:

```text
Issue #812

Improve error message when configuration is missing.

Repository areas:
config/
errors/

Relevant technologies:
Go

Potential scope:
2–4 files

Existing related implementation:
config/loader.go
```

The system should not claim that an issue is objectively "easy" or "hard." Instead, it should expose the evidence and estimated scope used to help the contributor decide.

---

# 16. Pull Request Analysis

Given a PR:

```text
PR #1024
```

RepoPilot retrieves:

```text
PR description
+
changed files
+
diff
+
related issue
+
surrounding code
```

Then generates:

```text
What changed?

The PR introduces retry support to the HTTP client.

Main changes:

1. Added RetryPolicy interface.
2. Added exponential backoff implementation.
3. Updated HTTP client to use RetryPolicy.
4. Added unit tests.

Affected components:

client/
middleware/
tests/
```

---

# 17. API Design

Example endpoints:

```text
POST   /api/repositories
GET    /api/repositories
GET    /api/repositories/:id

POST   /api/repositories/:id/index
GET    /api/repositories/:id/status

POST   /api/repositories/:id/query

GET    /api/repositories/:id/issues
GET    /api/repositories/:id/issues/:issueId

GET    /api/repositories/:id/pulls
GET    /api/repositories/:id/pulls/:pullId

GET    /api/repositories/:id/files
GET    /api/repositories/:id/architecture

GET    /api/repositories/:id/symbols
```

---

# 18. Database Model

Core entities:

```text
User
 │
 ├── Repository
 │       │
 │       ├── RepositoryVersion
 │       ├── File
 │       │     └── Symbol
 │       │
 │       ├── Document
 │       ├── Issue
 │       ├── PullRequest
 │       └── IndexJob
 │
 └── Conversation
```

Potential tables:

```text
users
repositories
repository_versions
files
symbols
documents
chunks
issues
issue_comments
pull_requests
pull_request_files
commits
embeddings
index_jobs
conversations
messages
query_feedback
```

---

# 19. Background Indexing

Repository indexing should not happen synchronously inside an HTTP request.

Instead:

```text
POST /repositories/:id/index
          │
          ▼
       Create Job
          │
          ▼
      Job Queue
          │
          ▼
      Index Worker
          │
          ├── Clone
          ├── Parse
          ├── Chunk
          ├── Embed
          └── Store
```

Possible implementation:

```text
Go API
   ↓
Redis Streams / JobMQ
   ↓
Indexing Workers
   ↓
PostgreSQL + pgvector
```

The indexing system should support:

* Job status
* Retries
* Failure handling
* Progress tracking
* Idempotent indexing

---

# 20. Incremental Indexing

A full repository should not need to be re-indexed after every change.

RepoPilot should detect changed files using Git.

Example:

```text
Previous commit:
abc123

New commit:
def456
```

Changed:

```text
client/http.go
middleware/retry.go
```

Only those files need to be reprocessed.

This reduces:

* Embedding cost
* Processing time
* Database writes

---

# 21. Caching

Redis can cache frequently repeated queries.

Example key:

```text
repo:{repo_id}:query:{query_hash}
```

Cache:

```text
Retrieved chunks
Generated answer
Citation metadata
```

The cache should be invalidated when the indexed repository version changes.

---

# 22. Security

The system should:

* Validate GitHub URLs
* Restrict repository clone targets
* Enforce authentication
* Apply rate limits
* Prevent arbitrary command execution
* Sanitize rendered content
* Protect GitHub tokens
* Avoid exposing private repository data
* Use read-only GitHub permissions where possible

For public repositories, GitHub access can initially be implemented without requiring repository write permissions.

---

# 23. Observability

Track:

```text
Request latency
Retrieval latency
Embedding latency
LLM latency
Cache hit rate
Indexing duration
Indexing failures
Token usage
Retrieval result count
User feedback
```

Example:

```text
Query latency:
420 ms

Retrieval:
120 ms

Reranking:
80 ms

LLM:
220 ms
```

This makes it possible to identify performance bottlenecks.

---

# 24. Evaluation

RAG quality should be evaluated rather than judged only by manually asking questions.

Create an evaluation dataset:

```text
Question
Expected files
Expected concepts
Expected answer characteristics
```

Example:

```text
Question:
Where is retry behavior implemented?

Expected files:
middleware/retry.go
client/client.go
```

Measure:

* Retrieval Recall@K
* Precision@K
* Citation accuracy
* Answer groundedness
* Response latency

---

# 25. MVP

The first version should remain focused.

### Phase 1

Implement:

* GitHub repository URL input
* Repository cloning
* Code parsing
* Semantic chunking
* Embeddings
* PostgreSQL + pgvector
* Basic vector retrieval
* LLM answers
* Source citations

### Phase 2

Add:

* BM25
* Hybrid retrieval
* Reranking
* GitHub Issues
* Pull Requests
* Repository summaries

### Phase 3

Add:

* Architecture graph
* Issue analysis
* Contribution discovery
* Incremental indexing
* Redis caching
* Background workers

### Phase 4

Add:

* Authentication
* RBAC
* Observability
* Evaluation framework
* Production deployment

---

# 26. Example End-to-End Scenario

A developer wants to contribute to a large Go repository.

They enter:

```text
https://github.com/example/project
```

RepoPilot performs:

```text
Clone repository
      ↓
Parse Go source
      ↓
Extract functions/packages/interfaces
      ↓
Index documentation
      ↓
Fetch GitHub issues/PRs
      ↓
Generate embeddings
      ↓
Store searchable repository representation
```

The developer asks:

> I'm new to this repository. How should I understand it?

RepoPilot responds with:

```text
Repository Overview

The project is organized into four major layers:

API
 ↓
Service
 ↓
Repository
 ↓
Database

Recommended reading order:

1. README.md
2. cmd/server/main.go
3. internal/api/
4. internal/service/
5. internal/repository/

Main entry point:
cmd/server/main.go

Database layer:
internal/repository/

Tests:
internal/*/*_test.go
```

The developer then asks:

> Find me a good starting issue.

RepoPilot retrieves GitHub issues and identifies relevant candidates based on repository evidence.

The developer chooses an issue.

They ask:

> What do I need to understand before implementing this?

RepoPilot retrieves:

```text
Issue
+
Related files
+
Existing implementation
+
Tests
+
Related PR
+
Documentation
```

and produces a repository-grounded explanation.

The developer can then navigate directly to the cited files and begin contributing.

---

# 27. Future Features

Potential future improvements:

* GitHub PR draft generation
* Automated test discovery
* Dependency graph visualization
* Call graph visualization
* Commit history analysis
* "Explain this function" mode
* "Explain this PR" mode
* "Find similar implementations" mode
* Contributor onboarding guides
* Repository change summaries
* Automated documentation generation
* Multi-repository knowledge bases
* IDE extension
* VS Code integration
* GitHub App integration

---

# 28. Project Success Criteria

The project should demonstrate that RepoPilot can:

1. Ingest a real open-source repository.
2. Understand its source structure.
3. Retrieve relevant code for natural-language queries.
4. Combine code, issues, PRs, and documentation.
5. Generate grounded explanations.
6. Provide accurate source citations.
7. Incrementally update the index.
8. Handle asynchronous indexing.
9. Cache repeated queries.
10. Measure retrieval and response quality.

---

# 29. Core Differentiator

RepoPilot is **not simply an LLM chatbot**.

The core system is:

```text
GitHub Repository
       ↓
Code Understanding
       ↓
Repository Index
       ↓
Hybrid Search
       ↓
Reranking
       ↓
Repository Context
       ↓
LLM Reasoning
       ↓
Actionable Developer Guidance
```

The LLM is only one component.

The primary engineering challenge is building a system that can efficiently transform a large, constantly changing repository into a searchable representation and retrieve the right context for a developer's question.

**The product goal is simple:**

> **Help a developer go from "I don't understand this repository" to "I know where to look and what to change."**

