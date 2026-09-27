package retrieval

import (
	"regexp"
	"strings"
	"testing"
)

func TestQueryTerms(t *testing.T) {
	cases := []struct{ question, want string }{
		{"What does want_bytes do?", "want <-> bytes"},
		{"What does isAbsoluteModule2 do?", "(isabsolutemodule2 | is <-> absolute <-> module2)"},
		{"Where is HMACAlgorithm.get_signature defined?", "hmacalgorithm | get <-> signature | defined"},
		{"How does getObjectType work", "(getobjecttype | get <-> object <-> type) | work"},
		{"Where is the error 'Got unexpected extra argument' raised?",
			"got <-> unexpected <-> extra <-> argument | error | raised"},
		{"Where is the error 'Please don't use object wrappers for primitive types' thrown?",
			"please <-> don <-> t <-> use <-> object <-> wrappers <-> for <-> primitive <-> types | error | thrown"},
		{"What happens to a line that begins with %toc in the weave tool?", "happens | line | begins | toc | weave | tool"},
		{"How is the _{PROG_NAME}_COMPLETE variable built?", "prog <-> name | complete | variable | built"},
		{"What does _loads_unsafe_impl return?", "loads <-> unsafe <-> impl | return"},
		{"Where is the Weaviate client initialized?", "weaviate | client | initialized"},
		{"test test TEST Test", "test"},
		{"where is the", ""},
		{"", ""},
		{"Where is `make_pass_decorator` used and `isAll`?", "make <-> pass <-> decorator | isall | used"},
	}
	for _, c := range cases {
		if got := QueryTerms(c.question).TSQuery; got != c.want {
			t.Errorf("QueryTerms(%q)\n got  %q\n want %q", c.question, got, c.want)
		}
	}
}

func TestQueryTermsLexemes(t *testing.T) {
	got := QueryTerms("What does isAbsoluteModule2 do with want_bytes and want?").Lexemes
	if strings.Join(got, ",") != "isabsolutemodule2,is,absolute,module2,want,bytes" {
		t.Fatalf("lexemes = %v", got)
	}
}

func TestQueryTermsNeverPassesOperators(t *testing.T) {
	lexeme := regexp.MustCompile(`^[a-z0-9]+$`)
	for _, q := range []string{
		`a & b | c ! (d) : e * f <-> g`, `x\y 'z`, `ünïcödé wörds café`, `'); DROP TABLE chunks; --`,
		"'unclosed quote", `"" '' ` + "``", `____`, `%%%`, `a:*`, `'a'b'`,
	} {
		kq := QueryTerms(q)
		for _, l := range kq.Lexemes {
			if !lexeme.MatchString(l) {
				t.Errorf("%q gave lexeme %q", q, l)
			}
		}
		rest := strings.NewReplacer("<->", " ", "|", " ", "(", " ", ")", " ").Replace(kq.TSQuery)
		for _, w := range strings.Fields(rest) {
			if !lexeme.MatchString(w) {
				t.Errorf("%q gave tsquery %q with %q", q, kq.TSQuery, w)
			}
		}
	}
}

func TestCamelPartsMatchesTheMigrationRule(t *testing.T) {
	cases := map[string]string{
		"isAbsoluteModule2":      "is absolute module2",
		"HMACAlgorithm":          "hmacalgorithm",
		"URLSafeSerializerMixin": "urlsafe serializer mixin",
		"getObjectType":          "get object type",
		"module2Type":            "module2 type",
		"plain":                  "plain",
	}
	for in, want := range cases {
		parts := camelParts(in)
		if got := strings.ToLower(strings.Join(parts, " ")); got != want {
			t.Errorf("camelParts(%q) = %q, want %q", in, got, want)
		}
	}
}
