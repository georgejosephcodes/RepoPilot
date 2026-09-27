# Retrieval evaluation: vector

- Split: **all**, 3 questions (2 answerable, scored; unanswerable questions are listed separately)
- Embedding model: `m` (4 dimensions); 20 chunks retrieved per question
- Labels: sha256 `abababababab` (not frozen); code: `abc1234`; run at 2026-09-27T00:00:00Z
- Repositories: `o/r@7f05d21`

A chunk hits a labelled span when the file is equal and the lines overlap. Recall@k: an answering (grade 2) span is in the top k. MRR@20: mean of 1/rank of the first answering hit. Coverage@8: the share of a question's answering spans found in the top 8. Small groups move a lot with one question; read every number with its n.

## Results

| group | n | R@1 | R@3 | R@5 | R@8 | MRR@20 | cov@8 |
|---|---|---|---|---|---|---|---|
| all | 2 | 0.000 | 1.000 | 1.000 | 1.000 | 0.417 | 0.750 |
| split: dev | 1 | 0.000 | 1.000 | 1.000 | 1.000 | 0.500 | 1.000 |
| split: test | 1 | 0.000 | 1.000 | 1.000 | 1.000 | 0.333 | 0.500 |
| repo: o/r | 2 | 0.000 | 1.000 | 1.000 | 1.000 | 0.417 | 0.750 |
| kind: flow | 1 | 0.000 | 1.000 | 1.000 | 1.000 | 0.333 | 0.500 |
| kind: location | 1 | 0.000 | 1.000 | 1.000 | 1.000 | 0.500 | 1.000 |

## Per question

| id | repo | kind | split | first hit | R@8 | cov@8 | top result |
|---|---|---|---|---|---|---|---|
| q1 | o/r | location | dev | 2 | yes | 1.00 | `x.go:1-9` |
| q2 | o/r | flow | test | 3 | yes | 0.50 | `x.go:1-9` |

## Unanswerable questions

Not scored. The distance of the nearest chunk is recorded as data for a possible refusal threshold.

| id | repo | split | top distance | top result |
|---|---|---|---|---|
| q3 | o/r | dev | 0.3000 | `x.go:1-9` |

---

# Retrieval evaluation: vector

- Split: **all**, 3 questions (2 answerable, scored; unanswerable questions are listed separately)
- Embedding model: `m` (4 dimensions); 20 chunks retrieved per question
- Labels: sha256 `abababababab` (not frozen); code: `abc1234`; run at 2026-09-27T00:00:00Z
- Repositories: `o/r@7f05d21`

A chunk hits a labelled span when the file is equal and the lines overlap. Recall@k: an answering (grade 2) span is in the top k. MRR@20: mean of 1/rank of the first answering hit. Coverage@8: the share of a question's answering spans found in the top 8. Small groups move a lot with one question; read every number with its n.

## Results

| group | n | R@1 | R@3 | R@5 | R@8 | MRR@20 | cov@8 |
|---|---|---|---|---|---|---|---|
| all | 2 | 0.000 | 1.000 | 1.000 | 1.000 | 0.417 | 0.750 |
| split: dev | 1 | 0.000 | 1.000 | 1.000 | 1.000 | 0.500 | 1.000 |
| split: test | 1 | 0.000 | 1.000 | 1.000 | 1.000 | 0.333 | 0.500 |
| repo: o/r | 2 | 0.000 | 1.000 | 1.000 | 1.000 | 0.417 | 0.750 |
| kind: flow | 1 | 0.000 | 1.000 | 1.000 | 1.000 | 0.333 | 0.500 |
| kind: location | 1 | 0.000 | 1.000 | 1.000 | 1.000 | 0.500 | 1.000 |

## Change against `baseline` (2026-09-27T00:00:00Z)

Each cell is this run minus the earlier run, over the questions both runs scored.

| group | n | ΔR@1 | ΔR@3 | ΔR@5 | ΔR@8 | ΔMRR@20 | Δcov@8 |
|---|---|---|---|---|---|---|---|
| all | 2 | -0.500 | +0.500 | 0 | 0 | -0.208 | 0 |
| split: dev | 1 | 0 | +1.000 | 0 | 0 | +0.250 | 0 |
| split: test | 1 | -1.000 | 0 | 0 | 0 | -0.667 | 0 |
| repo: o/r | 2 | -0.500 | +0.500 | 0 | 0 | -0.208 | 0 |
| kind: flow | 1 | -1.000 | 0 | 0 | 0 | -0.667 | 0 |
| kind: location | 1 | 0 | +1.000 | 0 | 0 | +0.250 | 0 |

First answering hit: **1 better, 1 worse, 0 unchanged**.

- Better: q1 (4 → 2)
- Worse: q2 (1 → 3)

## Per question

| id | repo | kind | split | first hit | R@8 | cov@8 | top result |
|---|---|---|---|---|---|---|---|
| q1 | o/r | location | dev | 2 | yes | 1.00 | `x.go:1-9` |
| q2 | o/r | flow | test | 3 | yes | 0.50 | `x.go:1-9` |

## Unanswerable questions

Not scored. The distance of the nearest chunk is recorded as data for a possible refusal threshold.

| id | repo | split | top distance | top result |
|---|---|---|---|---|
| q3 | o/r | dev | 0.3000 | `x.go:1-9` |
