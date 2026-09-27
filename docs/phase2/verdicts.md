# Answer verdicts: vector against hybrid_rerank

Human verdicts for the answers selected by `answers.md`: every question whose two answers differ in cited files, refusal or mechanical checks, every answer that failed a check, and the first 10 unchanged pairs by id. That is 33 of the 74 questions. The other 41 pairs cite the same files and pass every check in both modes. They were not read one by one.

Each answer was read against its cited lines in the pinned clones, the question's `expected_answer`, and the labelled spans. Claims that go beyond the expected answer were checked in the source: `Parameter.handle_parse_result` for cl-11, the "Parameter Source Priority" docs for cl-6, `ragserver-genkit/main.go` for go-2 and `template/main.go` for go-6.

Verdicts: **correct**; **partly correct** (right place but incomplete, or muddled); **wrong**; **correctly refused**; **wrongly refused** (the answer exists in the repository).

## Summary

| verdict | vector | hybrid_rerank |
|---|---|---|
| correct | 25 | 28 |
| partly correct | 2 | 2 |
| wrong | 0 | 0 |
| wrongly refused | 4 | 1 |
| correctly refused | 2 | 2 |
| total judged | 33 | 33 |

**Gate item 4:**
- **No answer that is correct in vector mode is wrong in hybrid_rerank mode.** Two answers dropped from correct to partly correct (cl-13, cl-18). Both still point at the right code; they are listed below.
- **All 8 unanswerable questions were refused with no citations,** in both modes (`answers.md`).
- **Five answers improved:**
  - vector refused wrongly, hybrid_rerank answered correctly: cl-5, ts-7, ts-12;
  - partly correct to correct: go-6, ts-9.

## Changed verdicts

| id | vector | hybrid_rerank | why |
|---|---|---|---|
| cl-5 | wrongly refused | **correct** | Vector retrieval missed `Group.invoke` (first hit at rank 11 in the baseline). Hybrid_rerank puts it first. The answer describes `resolve_command`, the sub-context and the chain-mode loop. |
| ts-7 | wrongly refused | **correct** | The answer is `Object.assign(detect, {...})` in an unnamed chunk (`source/index.ts:282-385`), which vector search ranked 19th. Short, but right. |
| ts-12 | wrongly refused | **correct** | A literal error string. Keyword search finds it: the error is thrown in `detect` when `isBoxedPrimitiveObject(value)` is true. |
| go-6 | partly correct | **correct** | Vector named 3 of the 4 server groups and missed `template/main.go`. Hybrid_rerank cites all six `main` functions, including `ListenAndServe("localhost:8080", nil)` in `template` (checked). |
| ts-9 | partly correct | **correct** | Vector cited only a test file and inferred the message format from test expectations. Hybrid_rerank cites `createAssertNot` and describes the `TypeError`, with the custom or default message. |
| cl-13 | correct | partly correct | Vector says `_echo_aborted` writes to stderr and `Command.main` calls it. Hybrid_rerank cites the same two places, but its text says only that the message "is printed when an `Abort` is raised or when `_echo_aborted` is called", so it does not clearly answer "where". |
| cl-18 | correct | partly correct | Both say files opened for writing are opened lazily, on the first IO operation. The hybrid_rerank wording is muddled ("unless lazy mode behavior is changed (or if it is opened lazily by default)"). Neither mentions that `-` is never lazy. |

## Unchanged verdicts

| id | both modes | notes |
|---|---|---|
| cl-4 | wrongly refused | Retrieval found `CliRunner.isolation`, but only the tail piece of the split method (508-594), which restores the streams. The replacement at lines 450-466 is in the other piece. See `results.md`, limits. |
| cl-1 | correct | `HelpFormatter.__init__`. Vector gives the full width rule; hybrid_rerank gives less detail, plus `Context.make_formatter`. |
| cl-2 | correct | `Option.resolve_envvar_value`, `{auto_envvar_prefix}_{NAME}`. |
| cl-3 | correct | `ProgressBar.render_progress` via `echo`. |
| cl-6 | correct | Command line, env, `default_map`, default. Vector also lists `PROMPT` first, which matches the docs' "Parameter Source Priority" section (checked). |
| cl-7 | correct | `_main_shell_completion`, `shell_complete`, `source` and `complete`. Hybrid_rerank adds the per-class `shell_complete` methods. |
| cl-10 | correct | `pass_obj`-like decorator, innermost context of the type, `ensure`. |
| cl-11 | correct | `IntEnum` of value sources. Hybrid_rerank adds the deprecation-warning and slot-arbitration uses in `Parameter.handle_parse_result`, both real (checked). |
| cl-12 | correct | `Command.parse_args`. |
| cl-14 | correct | Replace `-` and `.`, wrap, uppercase. |
| cl-15 | correct | A backwards-compatibility liability (`docs/why.md`). |
| cl-16 | correct | `CliRunner` and `runner.invoke`. Hybrid_rerank shows the docs example with `exit_code` and `output`. |
| cl-17 | correct | Both message forms, from `Choice.get_invalid_choice_message`. |
| cl-19 | correct | One fill character, placed with `cos(pos * time_per_iteration)`. |
| cl-20, cl-21 | correctly refused | No telemetry, no update check. |
| go-2 | correct | `initWeaviate`, called from `main`. Hybrid_rerank also notes `weaviate.Init` in `ragserver-genkit`, which is real (checked). |
| go-5 | correct | Type-checks, then prints `Defs` and `Uses`. |
| go-8 | correct | `-r` / `reverseFlag`, then `reverse.String`. |
| go-10 | correct | Writes unopened groups and raises the indent. |
| go-11 | correct | Interface skeleton with `panic("unimplemented")` bodies. |
| go-17 | correct | `bufPool` (`sync.Pool`), `allocBuf`, `freeBuf` up to 16 KB. |
| ts-1 | correct | `detect` in `source/index.ts`. |
| ts-2 | correct | `isEmptyStringOrWhitespace`. |
| ts-3 | correct | `predicateArray.every`. |
| ts-15 | correct | Guards narrow in `if`; assertions throw. Both add the branded-type detail from `AGENTS.md`. |
