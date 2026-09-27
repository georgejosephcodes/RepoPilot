package retrieval

import (
	"regexp"
	"strings"
	"unicode"
)

// KeywordQuery is a question turned into a PostgreSQL tsquery (the 'simple' configuration) plus the distinct
// lexemes in it. Every lexeme is [a-z0-9]+ by construction, so nothing the user typed can become a tsquery
// operator. An empty TSQuery means nothing searchable was left.
type KeywordQuery struct {
	TSQuery string
	Lexemes []string
}

// Question words that carry no search value. Fixed before any evaluation run (PHASE2.md 4.3); code words such
// as test, file or error are kept on purpose.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`where how what which why when does do did is are was the a an of to in on for
		with and or it its this that be can i my we there from by as at into if not no`) {
		stopwords[w] = true
	}
}

// The same character class migrations/003_chunk_search.sql normalises content with.
var nonWord = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// QueryTerms builds the keyword query for a question:
//   - a quoted part ('...', "..." or `...`) is a phrase of its words, stopwords kept;
//   - a snake_case token is a phrase of its parts (want_bytes -> want <-> bytes);
//   - a camelCase token matches whole or as its parts (isAbsoluteModule2 -> (isabsolutemodule2 | is <-> absolute <-> module2));
//   - other tokens are single terms, minus stopwords.
//
// Terms are deduplicated and joined with OR; ranking decides which chunks matter.
func QueryTerms(question string) KeywordQuery {
	quoted, rest := splitQuoted(question)
	var terms []string
	seenTerm := map[string]bool{}
	seenLex := map[string]bool{}
	var lexemes []string
	add := func(term string, lex ...string) {
		if term == "" || seenTerm[term] {
			return
		}
		seenTerm[term] = true
		terms = append(terms, term)
		for _, l := range lex {
			if !seenLex[l] {
				seenLex[l] = true
				lexemes = append(lexemes, l)
			}
		}
	}

	for _, q := range quoted {
		words := words(q)
		add(strings.Join(words, " <-> "), words...)
	}
	for _, tok := range nonWord.Split(rest, -1) {
		if tok == "" {
			continue
		}
		lower := strings.ToLower(tok)
		switch {
		case strings.Contains(tok, "_"):
			parts := words(tok)
			add(strings.Join(parts, " <-> "), parts...)
		case len(camelParts(tok)) > 1:
			parts := camelParts(tok)
			for i := range parts {
				parts[i] = strings.ToLower(parts[i])
			}
			add("("+lower+" | "+strings.Join(parts, " <-> ")+")", append([]string{lower}, parts...)...)
		case stopwords[lower]:
		default:
			add(lower, lower)
		}
	}
	return KeywordQuery{TSQuery: strings.Join(terms, " | "), Lexemes: lexemes}
}

// words lowercases s and splits it into [a-z0-9] runs. Underscores separate words here, as the parser does.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
}

// camelParts splits before an uppercase letter that follows a lowercase letter or digit, exactly like the
// migration's regexp_replace(x, '([a-z0-9])([A-Z])', '\1 \2'): HMACAlgorithm stays whole, getObjectType splits.
func camelParts(tok string) []string {
	var parts []string
	start := 0
	for i := 1; i < len(tok); i++ {
		prev, cur := tok[i-1], tok[i]
		if (prev >= 'a' && prev <= 'z' || prev >= '0' && prev <= '9') && cur >= 'A' && cur <= 'Z' {
			parts = append(parts, tok[start:i])
			start = i
		}
	}
	return append(parts, tok[start:])
}

// splitQuoted takes quoted parts out of s. A quote opens only at the start or after a non-alphanumeric
// character, and closes only before the end or a non-alphanumeric character, so the apostrophe in "don't"
// neither opens nor closes a quote. An unclosed quote is left in the rest as ordinary text.
func splitQuoted(s string) (quoted []string, rest string) {
	r := []rune(s)
	var out strings.Builder
	isWord := func(i int) bool { return i >= 0 && i < len(r) && (unicode.IsLetter(r[i]) || unicode.IsDigit(r[i])) }
	for i := 0; i < len(r); i++ {
		c := r[i]
		if (c == '\'' || c == '"' || c == '`') && !isWord(i-1) {
			end := -1
			for j := i + 1; j < len(r); j++ {
				if r[j] == c && !isWord(j+1) {
					end = j
					break
				}
			}
			if end > i+1 {
				quoted = append(quoted, string(r[i+1:end]))
				out.WriteRune(' ')
				i = end
				continue
			}
		}
		out.WriteRune(c)
	}
	return quoted, out.String()
}
