// Package rewrite adds likely code identifiers and keywords to a question for the keyword list (PHASE2.md step 8,
// an eval-only experiment). The vector list and the reranker keep the original question.
//
// The model's reply is only ever used as extra keyword search text, and retrieval.QueryTerms reduces any text to
// plain [a-z0-9] lexemes, so a hostile or odd reply can at worst add search words.
package rewrite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"repopilot/api/internal/llm"
)

// PromptVersion changes whenever the prompt text changes, which invalidates every cached rewrite.
const PromptVersion = "rewrite-v1"

const (
	MaxTerms    = 12
	MaxTermLen  = 64 // characters
	maxQuestion = 1000
)

// ErrUnparseable means the model's reply held no usable term list.
var ErrUnparseable = errors.New("rewriter reply is not a term list")

// Rewriter returns search terms for a question.
type Rewriter interface {
	Rewrite(ctx context.Context, question string) ([]string, error)
	// Name identifies the model and prompt, so a cached rewrite is never reused across either.
	Name() string
}

const systemPrompt = `You help search the source code of one software repository.
Given a question about the repository, list identifiers and keywords that are likely to appear in the code that answers it: function, method, class, variable and file names in their usual spelling (snake_case, camelCase), and distinctive words.
Return JSON only, in this form: {"terms": ["term", ...]}, with at most 12 terms and no explanation.`

// LLMConfig adapts the answer model's settings for rewriting: deterministic and short.
func LLMConfig(base llm.Config) llm.Config {
	base.Temperature = 0
	base.MaxOutputTokens = 128
	return base
}

// LLMRewriter asks an LLM for the terms.
type LLMRewriter struct{ LLM llm.LLM }

func (r LLMRewriter) Name() string { return PromptVersion + "/" + r.LLM.ModelName() }

func (r LLMRewriter) Rewrite(ctx context.Context, question string) ([]string, error) {
	res, err := r.LLM.Generate(ctx, systemPrompt, BuildPrompt(question))
	if err != nil {
		return nil, err
	}
	return ParseTerms(res.Text)
}

// BuildPrompt is the user text: the trimmed question, cut to 1,000 characters.
func BuildPrompt(question string) string {
	q := strings.TrimSpace(question)
	if utf8.RuneCountInString(q) > maxQuestion {
		q = string([]rune(q)[:maxQuestion])
	}
	return "Question:\n" + q
}

// ParseTerms reads the first JSON object in the reply (text or code fences around it are ignored) and returns its
// "terms": strings only, trimmed, each cut to MaxTermLen characters, repeats dropped, at most MaxTerms. A reply with
// no object, or no "terms" array, is ErrUnparseable. An empty list is valid.
func ParseTerms(reply string) ([]string, error) {
	for start := strings.IndexByte(reply, '{'); start >= 0; {
		var obj struct {
			Terms *[]any `json:"terms"`
		}
		dec := json.NewDecoder(strings.NewReader(reply[start:]))
		if err := dec.Decode(&obj); err == nil && obj.Terms != nil {
			return clean(*obj.Terms), nil
		}
		next := strings.IndexByte(reply[start+1:], '{')
		if next < 0 {
			break
		}
		start += 1 + next
	}
	return nil, ErrUnparseable
}

func clean(items []any) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, it := range items {
		s, ok := it.(string)
		if !ok {
			continue
		}
		s = strings.Join(strings.Fields(s), " ")
		if utf8.RuneCountInString(s) > MaxTermLen {
			s = string([]rune(s)[:MaxTermLen])
		}
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) == MaxTerms {
			break
		}
	}
	return out
}

// KeywordText is the text the keyword list searches: the question, then the terms.
func KeywordText(question string, terms []string) string {
	if len(terms) == 0 {
		return ""
	}
	return strings.TrimSpace(question) + "\n" + strings.Join(terms, " ")
}
