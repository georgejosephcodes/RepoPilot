# Phase 1 demo results

Fifteen questions over three real repositories, run 2026-09-27 against `nvidia/nemotron-3-embed-1b:free` embeddings and
`gemini-3.5-flash-lite` generation. Raw responses: `raw.json`. Mechanical checks (citation snippets equal the real
lines, markers and citations agree, an expected file is cited, unanswerable questions are refused with no citations):
`checks.md`, 15 of 15 passed. This page adds a human verdict for every answer: **correct**, **partly correct**, or
**correctly refused**. A verdict is not automated; it comes from reading the answer against the cited code.

Every citation was independently checked against the file it names before this page was written; none was found
wrong.

## golang/example (`7f05d21`)

| id | question | verdict | notes |
|---|---|---|---|
| go-1 | Where is the function that reverses a string implemented? | **correct** | `reverse.String`, cited and correct. |
| go-2 | Where is the Weaviate client initialized? | **correct** | `initWeaviate`, plus `main` where it is called. Both citations check out. |
| go-3 | How does the ragserver query handler answer a question? | **correct** | Names all five real steps (parse JSON, embed, search Weaviate, build the prompt from `ragTemplateStr`, call the model, render JSON) and cites all three `main.go` variants for each step. Verified against the tail of `queryHandler`, which does call `renderJSON`. |
| go-4 | How does the server authenticate users with OAuth tokens? | **correctly refused** | No OAuth code exists in the repository (checked by search before asking). No citations, no fabrication. |
| go-5 | What does `PrintDefsUses` do? | **correct** | Matches the real function body line for line (type-checks, then prints `Defs` and `Uses`). |

## sindresorhus/is (`e9c026c`)

| id | question | verdict | notes |
|---|---|---|---|
| ts-1 | Where is the function that detects the type name of a value? | **correct** | Names `detect` and cites it, plus a `readme.md` passage noting `detect` is the export name for `is(value)`. Both citations are accurate. |
| ts-2 | Where is the check that a string is empty or contains only whitespace? | **correct** | `isEmptyStringOrWhitespace`, matches the real one-line body. This question took 11.6 s for the model call, an outlier against 1-2 s elsewhere; the answer itself is unaffected. |
| ts-3 | How does `isAll` combine several predicates into one? | **correct** | Describes `predicateArray.every` and quotes the real combinator line. It stops short of the array/single-value branching in the surrounding code, which the question did not ask about. |
| ts-4 | How does the library connect to a database? | **correctly refused** | The library has no I/O; refused with no citations. |
| ts-5 | What does `isAbsoluteModule2` do? | **correct** | Matches the real two-line function exactly. |

## pallets/itsdangerous (`672971d`)

| id | question | verdict | notes |
|---|---|---|---|
| py-1 | Where is the HMAC signature computed? | **correct** | `HMACAlgorithm.get_signature`, matches `hmac.new(...).digest()`. |
| py-2 | Where does the code convert a timestamp into a datetime? | **correct** | `timestamp_to_datetime`, matches `datetime.fromtimestamp(ts, tz=timezone.utc)`. |
| py-3 | How does `TimestampSigner` check that a signed value has expired? | **correct** | Describes the real `unsign` logic: decode the timestamp, compute `age`, raise `SignatureExpired` when `age > max_age` or `age < 0`. The cited range (72-158) is the real implementation, after two `@overload` signatures the chunker correctly did not cite. |
| py-4 | How does the library connect to a Redis server to cache signatures? | **correctly refused** | No Redis or caching code exists; refused with no citations. |
| py-5 | What does `want_bytes` do? | **correct** | Matches the real body: encodes a `str` to bytes with the given encoding and errors, returns bytes unchanged. |

## Summary

- **15 of 15 correct or correctly refused.** No wrong answer and no fabricated citation in this run.
- **All three unanswerable questions were refused**, with no citations, using the exact refusal sentence from step 7.
- **Every exact-identifier question (`go-5`, `ts-5`, `py-5`) succeeded**, unlike the concern raised in step 6 (where an identifier question needed K=8 to surface a low-ranked chunk). That concern has not disappeared: it means retrieval got lucky here, not that the risk is gone. See "Known gaps" below.
- **Timing:** embedding 218 ms to 655 ms, search 5 ms to 12 ms, the model 1.0 s to 11.6 s (two calls, `ts-2` and `ts-4`, took over 8 s with no visible cause; both still answered correctly). End-to-end per question was mostly 1.5 s to 2.5 s.
- **Cost:** 15 embedding requests (1 per question), well inside the free-tier daily limit.

## Known gaps

This is a set of 15 questions, not a benchmark. It shows Phase 1 working, not that retrieval is solved. Carried over from step 6 and confirmed or left open here:

1. **Retrieval was not stress-tested by this run.** All 15 questions found their answer inside the top 8 chunks. Step 6 already showed a case (a different `golang/example` question, not repeated here) where the two best chunks ranked 6th and 7th, saved only because K=8 was used. A smaller K, a bigger repository, or a less distinctive identifier could still miss.
2. **Near-duplicate files can crowd the results** (seen in step 6 with four copies of `indent_handler.go`; not present in this question set, so not re-tested here).
3. **No hybrid or reranked retrieval.** Every result here came from vector search alone. `go-3` needed all three `ragserver` variants, which vector search returned; a repository where the right answer is lexically distinctive but semantically similar to a wrong one would be a harder case, and this run doesn't cover it.
4. **The 15 questions are correlated with the demo repositories chosen for step 9,** all mid-size, well-documented, single-purpose libraries. A larger or messier codebase (a monorepo, deep call chains, sparse comments) is untested.
5. **One provider pair only.** Every answer used `nvidia/nemotron-3-embed-1b:free` and `gemini-3.5-flash-lite`. A different embedding model could rank differently on the same questions.

These become the seed of the Phase 2 evaluation set, which is expected to grow past 15 questions and include cases chosen to fail, not only cases expected to pass.
