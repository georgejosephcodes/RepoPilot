// Package retrieval finds the chunks of one repository nearest to a question vector.
package retrieval

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var (
	ErrModelMismatch     = errors.New("repository was indexed with a different embedding model, re-index required")
	ErrDimensionMismatch = errors.New("vector dimension does not match the configured embedding dimension")
	ErrInvalidK          = errors.New("k must be between 1 and 50")
	ErrInvalidVector     = errors.New("vector contains a non-finite value")
)

const MaxK = 50

// VectorLiteral renders a query vector as the text form PostgreSQL parses into a halfvec: [v1,v2,...].
// It checks the length first, so a mismatched embedder is caught here instead of as a database error.
func VectorLiteral(v []float32, dim int) (string, error) {
	if len(v) != dim {
		return "", fmt.Errorf("%w: got %d, want %d", ErrDimensionMismatch, len(v), dim)
	}
	var b strings.Builder
	b.Grow(len(v) * 10)
	b.WriteByte('[')
	for i, x := range v {
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", ErrInvalidVector
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(f, 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String(), nil
}

// versionAtLeast reports whether a pgvector version like "0.8.6" is at least "major.minor.patch".
func versionAtLeast(version string, major, minor, patch int) bool {
	parts := strings.SplitN(strings.TrimSpace(version), ".", 3)
	want := [3]int{major, minor, patch}
	for i := 0; i < 3; i++ {
		got := 0
		if i < len(parts) {
			n, err := strconv.Atoi(strings.TrimFunc(parts[i], func(r rune) bool { return r < '0' || r > '9' }))
			if err != nil {
				return false
			}
			got = n
		}
		if got != want[i] {
			return got > want[i]
		}
	}
	return true
}
