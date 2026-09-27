package retrieval

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Query is one retrieval request: a question about one repository, its vector, how many chunks to return, and
// an optional filter.
type Query struct {
	RepoID int64
	Text   string
	Vec    []float32
	K      int
	Filter Filter
}

// Filter narrows the chunks a question can retrieve. The zero value matches every chunk.
type Filter struct {
	Languages  []string // chunk language is one of these; empty means any
	PathPrefix string   // file path starts with this, literally ("api/" means the directory); empty means any
}

// Empty reports whether the filter matches every chunk.
func (f Filter) Empty() bool { return len(f.Languages) == 0 && f.PathPrefix == "" }

// sqlLanguages is the language list as a query parameter: never nil, because a NULL array would match nothing.
func (f Filter) sqlLanguages() []string {
	if f.Languages == nil {
		return []string{}
	}
	return f.Languages
}

// Languages are the values the worker writes to chunks.language (worker/repopilot_worker/scan.py LANGUAGES).
// testdata/languages.json holds the same list, checked by the Go and the Python tests.
var Languages = []string{"go", "javascript", "markdown", "python", "typescript"}

const (
	MaxFilterLanguages  = 10
	MaxPathPrefixLength = 200
)

var ErrInvalidFilter = errors.New("invalid filter")

// ValidateFilter checks a filter from a request. The error message is safe to show to the caller.
func ValidateFilter(f Filter) error {
	if len(f.Languages) > MaxFilterLanguages {
		return fmt.Errorf("%w: at most %d languages", ErrInvalidFilter, MaxFilterLanguages)
	}
	for _, l := range f.Languages {
		if !slices.Contains(Languages, l) {
			return fmt.Errorf("%w: unknown language %q (known: %s)", ErrInvalidFilter, truncate(l, 40), strings.Join(Languages, ", "))
		}
	}
	p := f.PathPrefix
	switch {
	case len(p) > MaxPathPrefixLength:
		return fmt.Errorf("%w: path prefix is longer than %d characters", ErrInvalidFilter, MaxPathPrefixLength)
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("%w: path prefix must be relative to the repository root", ErrInvalidFilter)
	case strings.Contains(p, `\`):
		return fmt.Errorf("%w: path prefix must use forward slashes", ErrInvalidFilter)
	case strings.IndexFunc(p, unicode.IsControl) >= 0:
		return fmt.Errorf("%w: path prefix contains a control character", ErrInvalidFilter)
	case slices.Contains(strings.Split(p, "/"), ".."):
		return fmt.Errorf("%w: path prefix must not contain ..", ErrInvalidFilter)
	}
	return nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
