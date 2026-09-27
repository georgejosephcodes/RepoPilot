package rag

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"repopilot/api/internal/retrieval"
)

// Citation is a validated reference. Everything except N comes from the database row of the chunk the
// server put in the prompt, never from the model's text.
type Citation struct {
	N         int
	File      string
	Language  string
	Symbol    string
	Kind      string
	StartLine int
	EndLine   int
	Snippet   string
}

type Validated struct {
	Text      string     // the answer with invalid citation numbers removed
	Citations []Citation // valid numbers, deduplicated, in order of first appearance
	Invalid   int        // citation numbers removed
	Refused   bool       // the answer is the refusal sentence
	Grounded  bool       // at least one valid citation, or a refusal
}

const maxRangeWidth = 50

var markerRe = regexp.MustCompile(`\[(\d+(?:\s*[,\-]\s*\d+)*)\]`)

// Validate checks every citation marker in `answer` against `used`, the chunks that were in the prompt.
//
// A marker counts only when it is not part of code: outside inline code and fenced blocks, and preceded by the
// start of the text, whitespace, sentence punctuation, or another counted marker. So `items[1]` in an answer is
// left alone. Numbers that do not refer to a chunk in the prompt (0, too large, huge, or a range wider than 50)
// are removed from the text and counted. A marker with no valid number is removed with one preceding space.
func Validate(answer string, used []retrieval.Chunk) Validated {
	mask := codeMask(answer)
	var out strings.Builder
	out.Grow(len(answer))
	seen := map[int]bool{}
	var order []int
	invalid, pos, lastEnd := 0, 0, -1

	for _, loc := range markerRe.FindAllStringSubmatchIndex(answer, -1) {
		start, end := loc[0], loc[1]
		if mask[start] || !markerStartOK(answer, start, lastEnd) {
			continue
		}
		valid, bad := expand(answer[loc[2]:loc[3]], len(used))
		invalid += bad

		out.WriteString(answer[pos:start])
		if len(valid) == 0 {
			if s := out.String(); strings.HasSuffix(s, " ") && (end == len(answer) || strings.ContainsRune(".,;:!?)\n", rune(answer[end])) || answer[end] == ' ') {
				out.Reset()
				out.WriteString(s[:len(s)-1])
			}
		}
		for _, n := range valid {
			out.WriteString("[" + strconv.Itoa(n) + "]")
			if !seen[n] {
				seen[n] = true
				order = append(order, n)
			}
		}
		pos, lastEnd = end, end
	}
	out.WriteString(answer[pos:])

	v := Validated{Text: out.String(), Invalid: invalid}
	for _, n := range order {
		c := used[n-1]
		v.Citations = append(v.Citations, Citation{
			N: n, File: c.FilePath, Language: c.Language, Symbol: c.Symbol, Kind: c.Kind,
			StartLine: c.StartLine, EndLine: c.EndLine, Snippet: c.Content,
		})
	}
	v.Refused = strings.HasPrefix(strings.ToLower(strings.TrimSpace(v.Text)), strings.ToLower(RefusalSentence))
	v.Grounded = len(v.Citations) > 0 || v.Refused
	return v
}

// markerStartOK reports whether a marker starting at byte `start` may be a citation.
func markerStartOK(text string, start, lastEnd int) bool {
	if start == 0 || start == lastEnd { // start of text, or glued to the previous counted marker: [1][2]
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:start])
	return unicode.IsSpace(r) || strings.ContainsRune("(.,;:!?\"'", r)
}

// expand turns "1", "1,2", "1, 3", or "2-4" into valid numbers (1..n, no duplicates) and a count of invalid ones.
func expand(spec string, n int) (valid []int, invalid int) {
	seen := map[int]bool{}
	add := func(v int) {
		if v < 1 || v > n {
			invalid++
			return
		}
		if !seen[v] {
			seen[v] = true
			valid = append(valid, v)
		}
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if lo, hi, isRange := strings.Cut(part, "-"); isRange {
			a, okA := parseSmallInt(strings.TrimSpace(lo))
			b, okB := parseSmallInt(strings.TrimSpace(hi))
			if !okA || !okB || a > b || b-a > maxRangeWidth {
				invalid++
				continue
			}
			for v := a; v <= b; v++ {
				add(v)
			}
			continue
		}
		v, ok := parseSmallInt(part)
		if !ok {
			invalid++
			continue
		}
		add(v)
	}
	return valid, invalid
}

// parseSmallInt parses up to 9 digits, so huge numbers are invalid instead of overflowing.
func parseSmallInt(s string) (int, bool) {
	if s == "" || len(s) > 9 {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	return v, err == nil
}

// codeMask marks bytes that are inside fenced code blocks or inline code spans. A run of three or more
// backticks toggles a fence; a shorter run toggles an inline span, which ends at a newline. An unclosed fence
// masks the rest of the text.
func codeMask(text string) []bool {
	mask := make([]bool, len(text))
	inFence, inInline := false, false
	for i := 0; i < len(text); {
		c := text[i]
		if c == '`' {
			j := i
			for j < len(text) && text[j] == '`' {
				j++
			}
			for k := i; k < j; k++ {
				mask[k] = true
			}
			if j-i >= 3 {
				inFence, inInline = !inFence, false
			} else if !inFence {
				inInline = !inInline
			}
			i = j
			continue
		}
		if c == '\n' {
			inInline = false
		}
		if inFence || inInline {
			mask[i] = true
		}
		i++
	}
	return mask
}
