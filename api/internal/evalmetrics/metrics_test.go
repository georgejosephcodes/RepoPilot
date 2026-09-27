package evalmetrics

import (
	"math"
	"testing"
)

func sp(file string, a, b int) Span { return Span{File: file, Start: a, End: b} }
func lb(file string, a, b, g int) Label {
	return Label{Span: sp(file, a, b), Grade: g}
}

func TestHitsOverlapRules(t *testing.T) {
	relevant := []Label{lb("a.go", 10, 20, 2)}
	cases := []struct {
		name string
		r    Span
		want int
	}{
		{"inside", sp("a.go", 12, 15), 2},
		{"covers", sp("a.go", 1, 100), 2},
		{"shares the first line", sp("a.go", 5, 10), 2},
		{"shares the last line", sp("a.go", 20, 30), 2},
		{"adjacent before", sp("a.go", 1, 9), 0},
		{"adjacent after", sp("a.go", 21, 30), 0},
		{"same lines, other file", sp("b.go", 10, 20), 0},
		{"path differs only in case", sp("A.go", 10, 20), 0},
	}
	for _, c := range cases {
		if got := Hits([]Span{c.r}, relevant)[0]; got != c.want {
			t.Errorf("%s: grade %d, want %d", c.name, got, c.want)
		}
	}
}

func TestHitsTakesTheBestGrade(t *testing.T) {
	relevant := []Label{lb("a.go", 1, 10, 1), lb("a.go", 8, 12, 2)}
	if got := Hits([]Span{sp("a.go", 1, 9)}, relevant)[0]; got != 2 {
		t.Fatalf("grade %d, want 2", got)
	}
	if got := Hits([]Span{sp("a.go", 1, 5)}, relevant)[0]; got != 1 {
		t.Fatalf("grade %d, want 1", got)
	}
}

func TestRecallAndReciprocalRankCountGrade2Only(t *testing.T) {
	hits := []int{0, 1, 0, 2, 2}
	if FirstHitRank(hits) != 4 {
		t.Fatal("first grade-2 hit is at rank 4")
	}
	if RecallAt(hits, 3) || !RecallAt(hits, 4) || !RecallAt(hits, 8) {
		t.Fatal("recall boundaries wrong")
	}
	if ReciprocalRank(hits, 20) != 0.25 || ReciprocalRank(hits, 3) != 0 {
		t.Fatal("reciprocal rank wrong")
	}
	onlyContext := []int{1, 1, 1}
	if FirstHitRank(onlyContext) != 0 || RecallAt(onlyContext, 8) || ReciprocalRank(onlyContext, 20) != 0 {
		t.Fatal("grade-1 hits must not count")
	}
	if FirstHitRank(nil) != 0 || RecallAt(nil, 8) {
		t.Fatal("empty list has no hits")
	}
}

func TestSpanCoverage(t *testing.T) {
	relevant := []Label{lb("a.go", 1, 5, 2), lb("b.go", 1, 5, 2), lb("c.go", 1, 5, 1)}
	retrieved := []Span{sp("a.go", 3, 4), sp("a.go", 1, 2), sp("c.go", 1, 1), sp("b.go", 5, 9)}
	if got := SpanCoverageAt(retrieved, relevant, 3); got != 0.5 {
		t.Fatalf("coverage@3 = %v, want 0.5 (two chunks on one span count once; grade 1 is not counted)", got)
	}
	if got := SpanCoverageAt(retrieved, relevant, 8); got != 1 {
		t.Fatalf("coverage@8 = %v, want 1 (k larger than the list)", got)
	}
	if got := SpanCoverageAt(retrieved, []Label{lb("a.go", 1, 5, 1)}, 8); got != 0 {
		t.Fatal("no grade-2 labels means coverage 0")
	}
}

func TestScoreAndSummarize(t *testing.T) {
	relevant := []Label{lb("a.go", 10, 20, 2)}
	miss := sp("z.go", 1, 1)
	hit := sp("a.go", 15, 15)
	atRank := func(r int) []Span {
		s := make([]Span, 20)
		for i := range s {
			s[i] = miss
		}
		if r > 0 {
			s[r-1] = hit
		}
		return s
	}
	a := ScoreQuestion(atRank(1), relevant)
	b := ScoreQuestion(atRank(4), relevant)
	c := ScoreQuestion(atRank(0), relevant)
	if !a.RecallAt1 || b.RecallAt3 || !b.RecallAt5 || c.RecallAt8 || c.FirstHitRank != 0 {
		t.Fatalf("scores wrong: %+v %+v %+v", a, b, c)
	}
	s := Summarize([]Score{a, b, c})
	if s.N != 3 || math.Abs(s.RecallAt1-1.0/3) > 1e-12 || math.Abs(s.RecallAt5-2.0/3) > 1e-12 || math.Abs(s.MRR-(1+0.25)/3) > 1e-12 {
		t.Fatalf("summary wrong: %+v", s)
	}
	if Summarize(nil).N != 0 {
		t.Fatal("empty summary")
	}
}
