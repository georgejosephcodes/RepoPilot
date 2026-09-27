# Retrieval evaluation: keyword-bm25

- Split: **dev**, 46 questions (42 answerable, scored; unanswerable questions are listed separately)
- Embedding model: `nvidia/nemotron-3-embed-1b:free` (2048 dimensions); 20 chunks retrieved per question
- Labels: sha256 `25b94b300dc8` (frozen); code: `1beee01-dirty`; run at 2026-09-27T08:57:41Z
- Repositories: `golang/example@7f05d21`, `sindresorhus/is@e9c026c`, `pallets/itsdangerous@672971d`, `pallets/click@06b2a67`

A chunk hits a labelled span when the file is equal and the lines overlap. Recall@k: an answering (grade 2) span is in the top k. MRR@20: mean of 1/rank of the first answering hit. Coverage@8: the share of a question's answering spans found in the top 8. Small groups move a lot with one question; read every number with its n.

## Results

| group | n | R@1 | R@3 | R@5 | R@8 | MRR@20 | cov@8 |
|---|---|---|---|---|---|---|---|
| all | 42 | 0.524 | 0.643 | 0.810 | 0.810 | 0.624 | 0.762 |
| split: dev | 42 | 0.524 | 0.643 | 0.810 | 0.810 | 0.624 | 0.762 |
| repo: golang/example | 10 | 0.700 | 0.700 | 0.900 | 0.900 | 0.750 | 0.850 |
| repo: pallets/click | 13 | 0.385 | 0.538 | 0.769 | 0.769 | 0.503 | 0.692 |
| repo: pallets/itsdangerous | 9 | 0.556 | 0.667 | 0.889 | 0.889 | 0.674 | 0.889 |
| repo: sindresorhus/is | 10 | 0.500 | 0.700 | 0.700 | 0.700 | 0.608 | 0.650 |
| kind: conceptual | 4 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |
| kind: flow | 9 | 0.444 | 0.556 | 0.889 | 0.889 | 0.572 | 0.778 |
| kind: identifier | 9 | 0.667 | 1.000 | 1.000 | 1.000 | 0.815 | 1.000 |
| kind: literal | 5 | 0.800 | 1.000 | 1.000 | 1.000 | 0.900 | 1.000 |
| kind: location | 10 | 0.300 | 0.300 | 0.400 | 0.400 | 0.338 | 0.350 |
| kind: trap | 5 | 0.200 | 0.200 | 0.800 | 0.800 | 0.367 | 0.700 |

## Change against `vector` (2026-09-27T08:48:14Z)

Each cell is this run minus the earlier run, over the questions both runs scored.

| group | n | ΔR@1 | ΔR@3 | ΔR@5 | ΔR@8 | ΔMRR@20 | Δcov@8 |
|---|---|---|---|---|---|---|---|
| all | 42 | -0.024 | -0.167 | -0.048 | -0.095 | -0.058 | -0.127 |
| split: dev | 42 | -0.024 | -0.167 | -0.048 | -0.095 | -0.058 | -0.127 |
| repo: golang/example | 10 | -0.100 | -0.200 | 0 | -0.100 | -0.100 | -0.133 |
| repo: pallets/click | 13 | 0 | -0.154 | -0.077 | -0.077 | -0.035 | -0.115 |
| repo: pallets/itsdangerous | 9 | -0.222 | -0.222 | 0 | -0.111 | -0.175 | -0.111 |
| repo: sindresorhus/is | 10 | +0.200 | -0.100 | -0.100 | -0.100 | +0.060 | -0.150 |
| kind: conceptual | 4 | 0 | 0 | 0 | 0 | 0 | 0 |
| kind: flow | 9 | +0.111 | -0.222 | +0.111 | 0 | +0.007 | -0.111 |
| kind: identifier | 9 | 0 | 0 | 0 | 0 | +0.019 | 0 |
| kind: literal | 5 | +0.200 | +0.200 | +0.200 | +0.200 | +0.213 | +0.200 |
| kind: location | 10 | -0.100 | -0.300 | -0.300 | -0.400 | -0.185 | -0.433 |
| kind: trap | 5 | -0.400 | -0.600 | -0.200 | -0.200 | -0.373 | -0.200 |

First answering hit: **11 better, 15 worse, 16 unchanged**.

- Better: go-6 (3 → 1), go-7 (6 → 1), ts-6 (2 → 1), ts-10 (2 → 1), ts-11 (3 → 2), ts-12 (10 → 1), py-3 (2 → 1), cl-4 (none → 17), cl-11 (3 → 1), cl-12 (3 → 2), cl-18 (5 → 4)
- Worse: go-1 (1 → none), go-3 (1 → 4), go-16 (1 → 4), ts-1 (1 → none), ts-7 (19 → none), ts-16 (2 → 12), py-1 (1 → 4), py-6 (7 → 15), py-10 (1 → 2), py-15 (1 → 4), cl-1 (5 → none), cl-5 (11 → none), cl-7 (3 → 5), cl-8 (2 → 5), cl-9 (1 → 3)

## Per question

| id | repo | kind | split | first hit | R@8 | cov@8 | top result |
|---|---|---|---|---|---|---|---|
| go-1 | golang/example | location | dev | none | no | 0.00 | `slog-handler-guide/guide.md:12-20` |
| go-3 | golang/example | flow | dev | 4 | yes | 1.00 | `ragserver/ragserver/main.go:194-213` |
| go-5 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/defsuses/main.go:22-43` |
| go-6 | golang/example | location | dev | 1 | yes | 0.50 | `ragserver/ragserver/main.go:29-61` |
| go-7 | golang/example | flow | dev | 1 | yes | 1.00 | `outyet/main.go:65-75` |
| go-9 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/nilfunc/main.go:53-92` |
| go-11 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/skeleton/main.go:28-59` |
| go-12 | golang/example | literal | dev | 1 | yes | 1.00 | `helloserver/server.go:56-65` |
| go-14 | golang/example | conceptual | dev | 1 | yes | 1.00 | `ragserver/README.md:30-37` |
| go-16 | golang/example | trap | dev | 4 | yes | 1.00 | `slog-handler-guide/guide.md:23-72` |
| ts-1 | sindresorhus/is | location | dev | none | no | 0.00 | `source/index.ts:103-105` |
| ts-3 | sindresorhus/is | flow | dev | 2 | yes | 1.00 | `source/index.ts:416-425` |
| ts-6 | sindresorhus/is | location | dev | 1 | yes | 1.00 | `source/index.ts:666-672` |
| ts-7 | sindresorhus/is | location | dev | none | no | 0.00 | `test/test.ts:1124-1243` |
| ts-8 | sindresorhus/is | flow | dev | 1 | yes | 0.50 | `source/index.ts:1404-1414` |
| ts-10 | sindresorhus/is | identifier | dev | 1 | yes | 1.00 | `source/index.ts:831-841` |
| ts-11 | sindresorhus/is | identifier | dev | 2 | yes | 1.00 | `test/test.ts:1564-1683` |
| ts-12 | sindresorhus/is | literal | dev | 1 | yes | 1.00 | `source/index.ts:195-259` |
| ts-14 | sindresorhus/is | conceptual | dev | 1 | yes | 1.00 | `readme.md:903-905` |
| ts-16 | sindresorhus/is | trap | dev | 12 | no | 0.00 | `source/types.ts:83-88` |
| py-1 | pallets/itsdangerous | location | dev | 4 | yes | 1.00 | `src/itsdangerous/signer.py:76-127` |
| py-3 | pallets/itsdangerous | flow | dev | 1 | yes | 1.00 | `src/itsdangerous/timed.py:72-158` |
| py-6 | pallets/itsdangerous | location | dev | 15 | no | 0.00 | `src/itsdangerous/serializer.py:1-21` |
| py-7 | pallets/itsdangerous | flow | dev | 1 | yes | 1.00 | `src/itsdangerous/url_safe.py:15-69` |
| py-9 | pallets/itsdangerous | identifier | dev | 1 | yes | 1.00 | `src/itsdangerous/serializer.py:367-395` |
| py-10 | pallets/itsdangerous | identifier | dev | 2 | yes | 1.00 | `src/itsdangerous/signer.py:76-127` |
| py-12 | pallets/itsdangerous | literal | dev | 1 | yes | 1.00 | `src/itsdangerous/url_safe.py:15-69` |
| py-13 | pallets/itsdangerous | conceptual | dev | 1 | yes | 1.00 | `README.md:16-32` |
| py-15 | pallets/itsdangerous | trap | dev | 4 | yes | 1.00 | `src/itsdangerous/exc.py:66-89` |
| cl-1 | pallets/click | location | dev | none | no | 0.00 | `tests/test_utils/test_echo_via_pager.py:55-174` |
| cl-3 | pallets/click | location | dev | 1 | yes | 1.00 | `src/click/_termui_impl.py:250-294` |
| cl-4 | pallets/click | location | dev | 17 | no | 0.00 | `tests/test_testing.py:740-757` |
| cl-5 | pallets/click | flow | dev | none | no | 0.00 | `docs/commands.md:137-175` |
| cl-7 | pallets/click | flow | dev | 5 | yes | 0.50 | `docs/shell-completion.md:21-137` |
| cl-8 | pallets/click | flow | dev | 5 | yes | 1.00 | `tests/test_termui.py:1945-1963` |
| cl-9 | pallets/click | identifier | dev | 3 | yes | 1.00 | `tests/test_utils/test_make_default_short_help.py:6-63` |
| cl-11 | pallets/click | identifier | dev | 1 | yes | 1.00 | `src/click/core.py:195-231` |
| cl-12 | pallets/click | literal | dev | 2 | yes | 1.00 | `tests/test_arguments.py:71-83` |
| cl-14 | pallets/click | literal | dev | 1 | yes | 1.00 | `src/click/core.py:1647-1677` |
| cl-15 | pallets/click | conceptual | dev | 1 | yes | 1.00 | `docs/why.md:98-106` |
| cl-17 | pallets/click | trap | dev | 1 | yes | 1.00 | `src/click/types.py:478-490` |
| cl-18 | pallets/click | trap | dev | 4 | yes | 0.50 | `src/click/types.py:915-952` |

## Unanswerable questions

Not scored. The distance of the nearest chunk is recorded as data for a possible refusal threshold.

| id | repo | split | top distance | top result |
|---|---|---|---|---|
| go-4 | golang/example | dev | 0.0000 | `gotypes/go-types.md:20-103` |
| ts-4 | sindresorhus/is | dev | 0.0000 | (nothing retrieved) |
| py-4 | pallets/itsdangerous | dev | 0.0000 | `src/itsdangerous/timed.py:22-27` |
| cl-20 | pallets/click | dev | 0.0000 | `src/click/shell_completion.py:278-386` |
