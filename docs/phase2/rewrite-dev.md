# Query rewriting on the dev split: tried, not kept

Phase 2, step 8. An experiment that ran in the eval harness only; it was never wired into the API.

## What was tried

One Gemini call (`gemini-3.5-flash-lite`, prompt `rewrite-v1`, temperature 0, 128 output tokens, 10 s timeout) turned a question into up to 12 identifiers and keywords likely to appear in the code. These terms were added to the text that the keyword list searches. The vector list and the reranker kept the original question. The code is commit `4b1de53`. It was removed in the commit after this report and can be restored from history.

## Keep rule (fixed before any run)

The experiment would be kept only if `hybrid-rerank-rewrite` on dev reached all of these:
- MRR@20 of at least 0.970, against 0.960 without rewriting;
- R@3 and R@8 still at 1.000;
- no question leaving the top 8;
- a median rewrite time of at most **1.5 s**. The measured live latency was already about 2 s above Phase 1's, the most the gate allows, so a rewrite call could not add much time.

## What happened

The first dev run (`hybrid-rewrite`, 46 questions) was stopped after 22 rewrite calls:

| outcome | calls | time |
|---|---|---|
| terms returned | 13 | 1,114 to 9,807 ms, median 6,028 ms |
| timed out (10 s) | 8 | 10,004 to 10,009 ms |
| reply not parseable | 1 | 1,881 ms |

- **Median over all 22 calls:** about 7.9 s. Only 2 calls were at or under 1.5 s.
- **Not a rate limit:** no 429 or 503 status was returned. The calls themselves were slow.
- **Comparison:** rerank calls to the same model, with the same key, took 1.4 to 2.3 s about 30 minutes earlier. It is not known whether the model was slow in general at that time or on this prompt.
- **The terms looked plausible.** Examples: `ErrNoBuildInfo`, `ReadBuildInfo` for a question about a Go build-info error; `isPlainObject`, `getPrototypeOf` for one about plain objects; `CheckNilFuncComparison` for a checker question. The 13 cached rewrites are in [`runs/rewrites-dev.json`](runs/rewrites-dev.json).

## Decision

**Not kept.**
- **Latency:** the latency condition failed by a wide margin with the key the API uses. A run with a different key could show that the prompt can be fast, but not that it would be fast for this deployment.
- **Headroom:** there is little room to gain. With rewriting absent, reranking already gives dev R@3 1.000 and MRR@20 0.960, so passing needed about one question to move up.

## Not measured

Retrieval quality with rewriting was not measured. There is no MRR or recall number for it, and none should be inferred from this report.
