package main

import (
	"fmt"
	"sort"
	"strings"

	"repopilot/api/internal/evalmetrics"
)

// renderReport writes the Markdown report. base, when not nil, is an earlier run to compare against.
// The report never says a variant is "better"; it shows numbers with their counts and the per-question changes.
func renderReport(run Run, base *Run) string {
	var b strings.Builder
	s := run.Settings
	fmt.Fprintf(&b, "# Retrieval evaluation: %s\n\n", s.Variant)
	fmt.Fprintf(&b, "- Split: **%s**, %d questions (%d answerable, scored; unanswerable questions are listed separately)\n",
		s.Split, len(run.Questions), run.Overall.N)
	fmt.Fprintf(&b, "- Embedding model: `%s` (%d dimensions); %d chunks retrieved per question\n", s.Model, s.Dimension, s.Depth)
	if len(s.Params) > 0 {
		keys := make([]string, 0, len(s.Params))
		for k := range s.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("%s %g", k, s.Params[k])
		}
		fmt.Fprintf(&b, "- Settings: %s\n", strings.Join(parts, ", "))
	}
	if r := s.Rerank; r != nil {
		fmt.Fprintf(&b, "- Rerank: `%s`, depth %d; %d upstream call(s) this run, %d from cache; %d fallback(s)%s; "+
			"rerank time (measured when each ranking was made) median %d ms, p90 %d ms, max %d ms\n",
			r.Name, r.Depth, r.UpstreamCalls, r.CacheHits, r.FallbackCount, fallbackText(r.Fallbacks), r.MedianMS, r.P90MS, r.MaxMS)
	}
	if r := s.Rewrite; r != nil {
		fmt.Fprintf(&b, "- Rewrite: `%s`; %d upstream call(s) this run, %d from cache; %d fallback(s)%s; "+
			"rewrite time (measured when each rewrite was made) median %d ms, p90 %d ms, max %d ms\n",
			r.Name, r.UpstreamCalls, r.CacheHits, r.FallbackCount, fallbackText(r.Fallbacks), r.MedianMS, r.P90MS, r.MaxMS)
	}
	frozen := "not frozen"
	if s.LabelsFrozen {
		frozen = "frozen"
	}
	fmt.Fprintf(&b, "- Labels: sha256 `%s` (%s); code: `%s`; run at %s\n", short(s.LabelsSHA256, 12), frozen, s.CodeCommit, s.CreatedAt)
	pins := make([]string, len(s.Repositories))
	for i, p := range s.Repositories {
		pins[i] = fmt.Sprintf("`%s@%s`", p.Name, short(p.Commit, 7))
	}
	fmt.Fprintf(&b, "- Repositories: %s\n\n", strings.Join(pins, ", "))
	b.WriteString("A chunk hits a labelled span when the file is equal and the lines overlap. Recall@k: an answering (grade 2) span is in the top k. MRR@20: mean of 1/rank of the first answering hit. Coverage@8: the share of a question's answering spans found in the top 8. Small groups move a lot with one question; read every number with its n.\n\n")

	b.WriteString("## Results\n\n")
	writeTable(&b, run, nil)
	if base != nil {
		fmt.Fprintf(&b, "\n## Change against `%s` (%s)\n\n", base.Settings.Variant, base.Settings.CreatedAt)
		b.WriteString("Each cell is this run minus the earlier run, over the questions both runs scored.\n\n")
		writeTable(&b, run, base)
		writeWinsLosses(&b, run, *base)
	}

	b.WriteString("\n## Per question\n\n")
	b.WriteString("| id | repo | kind | split | first hit | R@8 | cov@8 | top result |\n|---|---|---|---|---|---|---|---|\n")
	for _, q := range run.Questions {
		if q.Score == nil {
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %.2f | %s |\n", q.ID, q.Repo, q.Kind, q.Split,
			rankText(q.Score.FirstHitRank), yes(q.Score.RecallAt8), q.Score.CoverageAt8, top(q))
	}

	b.WriteString("\n## Unanswerable questions\n\nNot scored. The distance of the nearest chunk is recorded as data for a possible refusal threshold.\n\n")
	b.WriteString("| id | repo | split | top distance | top result |\n|---|---|---|---|---|\n")
	for _, q := range run.Questions {
		if q.Score != nil {
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %.4f | %s |\n", q.ID, q.Repo, q.Split, q.TopDistance, top(q))
	}
	return b.String()
}

type row struct {
	name string
	now  evalmetrics.Summary
	then *evalmetrics.Summary
}

func writeTable(b *strings.Builder, run Run, base *Run) {
	rows := []row{{name: "all", now: run.Overall}}
	add := func(prefix string, now map[string]evalmetrics.Summary) {
		for _, k := range sortedKeys(now) {
			rows = append(rows, row{name: prefix + k, now: now[k]})
		}
	}
	add("split: ", run.BySplit)
	add("repo: ", run.ByRepo)
	add("kind: ", run.ByKind)

	if base == nil {
		b.WriteString("| group | n | R@1 | R@3 | R@5 | R@8 | MRR@20 | cov@8 |\n|---|---|---|---|---|---|---|---|\n")
		for _, r := range rows {
			fmt.Fprintf(b, "| %s | %d | %.3f | %.3f | %.3f | %.3f | %.3f | %.3f |\n", r.name, r.now.N,
				r.now.RecallAt1, r.now.RecallAt3, r.now.RecallAt5, r.now.RecallAt8, r.now.MRR, r.now.CoverageAt8)
		}
		return
	}
	// Compare on the questions both runs scored, so a different split or a relabelled set cannot fake a change.
	now, then := pairedRuns(run, *base)
	summarize(&now)
	summarize(&then)
	pairs := []row{{name: "all", now: now.Overall, then: &then.Overall}}
	pair := func(prefix string, a, c map[string]evalmetrics.Summary) {
		for _, k := range sortedKeys(a) {
			t := c[k]
			pairs = append(pairs, row{name: prefix + k, now: a[k], then: &t})
		}
	}
	pair("split: ", now.BySplit, then.BySplit)
	pair("repo: ", now.ByRepo, then.ByRepo)
	pair("kind: ", now.ByKind, then.ByKind)
	b.WriteString("| group | n | ΔR@1 | ΔR@3 | ΔR@5 | ΔR@8 | ΔMRR@20 | Δcov@8 |\n|---|---|---|---|---|---|---|---|\n")
	for _, r := range pairs {
		t := *r.then
		fmt.Fprintf(b, "| %s | %d | %s | %s | %s | %s | %s | %s |\n", r.name, r.now.N,
			delta(r.now.RecallAt1-t.RecallAt1), delta(r.now.RecallAt3-t.RecallAt3), delta(r.now.RecallAt5-t.RecallAt5),
			delta(r.now.RecallAt8-t.RecallAt8), delta(r.now.MRR-t.MRR), delta(r.now.CoverageAt8-t.CoverageAt8))
	}
}

// pairedRuns keeps only the scored questions present in both runs, in this run's order.
func pairedRuns(run, base Run) (Run, Run) {
	baseByID := map[string]QuestionResult{}
	for _, q := range base.Questions {
		if q.Score != nil {
			baseByID[q.ID] = q
		}
	}
	var a, c Run
	for _, q := range run.Questions {
		if bq, ok := baseByID[q.ID]; ok && q.Score != nil {
			a.Questions = append(a.Questions, q)
			c.Questions = append(c.Questions, bq)
		}
	}
	return a, c
}

// writeWinsLosses lists questions whose first answering hit moved. A question found at any rank beats one not
// found in the top 20.
func writeWinsLosses(b *strings.Builder, run, base Run) {
	now, then := pairedRuns(run, base)
	var wins, losses []string
	same := 0
	for i := range now.Questions {
		a, c := now.Questions[i].Score.FirstHitRank, then.Questions[i].Score.FirstHitRank
		change := fmt.Sprintf("%s (%s → %s)", now.Questions[i].ID, rankText(c), rankText(a))
		switch {
		case rankKey(a) < rankKey(c):
			wins = append(wins, change)
		case rankKey(a) > rankKey(c):
			losses = append(losses, change)
		default:
			same++
		}
	}
	fmt.Fprintf(b, "\nFirst answering hit: **%d better, %d worse, %d unchanged**.\n\n", len(wins), len(losses), same)
	if len(wins) > 0 {
		fmt.Fprintf(b, "- Better: %s\n", strings.Join(wins, ", "))
	}
	if len(losses) > 0 {
		fmt.Fprintf(b, "- Worse: %s\n", strings.Join(losses, ", "))
	}
}

func rankKey(r int) int {
	if r == 0 {
		return 1 << 30
	}
	return r
}

func rankText(r int) string {
	if r == 0 {
		return "none"
	}
	return fmt.Sprint(r)
}

func delta(d float64) string {
	if d > -0.0005 && d < 0.0005 {
		return "0"
	}
	return fmt.Sprintf("%+.3f", d)
}

func yes(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func top(q QuestionResult) string {
	if len(q.Retrieved) == 0 {
		return "(nothing retrieved)"
	}
	r := q.Retrieved[0]
	return fmt.Sprintf("`%s:%d-%d`", r.File, r.Start, r.End)
}

func short(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func fallbackText(f map[string]int) string {
	if len(f) == 0 {
		return ""
	}
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, f[k])
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
