// Package evalmetrics scores a ranked list of retrieved chunks against labelled line spans.
//
// A retrieved chunk hits a label when the file paths are equal and the line ranges overlap (one shared line is
// enough). Labels are spans, not chunk ids, so they survive re-chunking. Grade 2 means the span answers the
// question; grade 1 means useful context. Recall and reciprocal rank count grade-2 hits only.
package evalmetrics

// Span is a file and a 1-based, inclusive line range.
type Span struct {
	File  string `json:"file"`
	Start int    `json:"start_line"`
	End   int    `json:"end_line"`
}

// Label is a relevant span with its grade (1 or 2).
type Label struct {
	Span
	Grade int `json:"grade"`
}

func overlaps(a, b Span) bool {
	return a.File == b.File && a.Start <= b.End && b.Start <= a.End
}

// Hits returns, for each retrieved span in rank order, the best grade it hits (0 = none).
func Hits(retrieved []Span, relevant []Label) []int {
	out := make([]int, len(retrieved))
	for i, r := range retrieved {
		for _, l := range relevant {
			if overlaps(r, l.Span) && l.Grade > out[i] {
				out[i] = l.Grade
			}
		}
	}
	return out
}

// FirstHitRank is the 1-based rank of the first grade-2 hit, or 0 when there is none.
func FirstHitRank(hits []int) int {
	for i, g := range hits {
		if g == 2 {
			return i + 1
		}
	}
	return 0
}

// RecallAt reports whether a grade-2 hit is in the top k.
func RecallAt(hits []int, k int) bool {
	r := FirstHitRank(hits)
	return r > 0 && r <= k
}

// ReciprocalRank is 1/rank of the first grade-2 hit within maxRank, else 0.
func ReciprocalRank(hits []int, maxRank int) float64 {
	r := FirstHitRank(hits)
	if r == 0 || r > maxRank {
		return 0
	}
	return 1 / float64(r)
}

// SpanCoverageAt is the fraction of grade-2 labels hit by at least one of the top k retrieved spans.
// It measures questions whose answer lives in several places, where one hit is not enough.
// A question with no grade-2 labels has coverage 0.
func SpanCoverageAt(retrieved []Span, relevant []Label, k int) float64 {
	if k > len(retrieved) {
		k = len(retrieved)
	}
	total, covered := 0, 0
	for _, l := range relevant {
		if l.Grade != 2 {
			continue
		}
		total++
		for _, r := range retrieved[:k] {
			if overlaps(r, l.Span) {
				covered++
				break
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(covered) / float64(total)
}

// Score holds one answerable question's numbers.
type Score struct {
	FirstHitRank int     `json:"first_hit_rank"` // 0 = not in the retrieved list
	RecallAt1    bool    `json:"recall_at_1"`
	RecallAt3    bool    `json:"recall_at_3"`
	RecallAt5    bool    `json:"recall_at_5"`
	RecallAt8    bool    `json:"recall_at_8"`
	RR           float64 `json:"reciprocal_rank_at_20"`
	CoverageAt8  float64 `json:"span_coverage_at_8"`
}

// MaxRank is how deep reciprocal rank looks; retrieval must return at least this many chunks to score it fully.
const MaxRank = 20

func ScoreQuestion(retrieved []Span, relevant []Label) Score {
	hits := Hits(retrieved, relevant)
	return Score{
		FirstHitRank: FirstHitRank(hits),
		RecallAt1:    RecallAt(hits, 1),
		RecallAt3:    RecallAt(hits, 3),
		RecallAt5:    RecallAt(hits, 5),
		RecallAt8:    RecallAt(hits, 8),
		RR:           ReciprocalRank(hits, MaxRank),
		CoverageAt8:  SpanCoverageAt(retrieved, relevant, 8),
	}
}

// Summary is the mean of a group of scores, with its size so a reader can judge it.
type Summary struct {
	N           int     `json:"n"`
	RecallAt1   float64 `json:"recall_at_1"`
	RecallAt3   float64 `json:"recall_at_3"`
	RecallAt5   float64 `json:"recall_at_5"`
	RecallAt8   float64 `json:"recall_at_8"`
	MRR         float64 `json:"mrr_at_20"`
	CoverageAt8 float64 `json:"span_coverage_at_8"`
}

func Summarize(scores []Score) Summary {
	s := Summary{N: len(scores)}
	if s.N == 0 {
		return s
	}
	b := func(v bool) float64 {
		if v {
			return 1
		}
		return 0
	}
	for _, x := range scores {
		s.RecallAt1 += b(x.RecallAt1)
		s.RecallAt3 += b(x.RecallAt3)
		s.RecallAt5 += b(x.RecallAt5)
		s.RecallAt8 += b(x.RecallAt8)
		s.MRR += x.RR
		s.CoverageAt8 += x.CoverageAt8
	}
	n := float64(s.N)
	s.RecallAt1 /= n
	s.RecallAt3 /= n
	s.RecallAt5 /= n
	s.RecallAt8 /= n
	s.MRR /= n
	s.CoverageAt8 /= n
	return s
}
