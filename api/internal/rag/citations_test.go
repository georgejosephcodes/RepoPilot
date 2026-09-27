package rag

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"repopilot/api/internal/retrieval"
)

func five() []retrieval.Chunk {
	var out []retrieval.Chunk
	for i := 1; i <= 5; i++ {
		out = append(out, mk(fmt.Sprintf("f%d.go", i), i*10, i*10+5, fmt.Sprintf("Sym%d", i), fmt.Sprintf("content of chunk %d", i)))
	}
	return out
}

func numbers(v Validated) []int {
	var out []int
	for _, c := range v.Citations {
		out = append(out, c.N)
	}
	return out
}

func TestValidateTable(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantText    string
		wantNumbers []int
		wantInvalid int
	}{
		{"single valid", "It starts here [1].", "It starts here [1].", []int{1}, 0},
		{"repeated and out of order keeps first appearance", "A [3]. B [1]. C [3].", "A [3]. B [1]. C [3].", []int{3, 1}, 0},
		{"invalid number removed with its space", "Made up [9].", "Made up.", nil, 1},
		{"zero is invalid", "Zero [0].", "Zero.", nil, 1},
		{"one past the end is invalid", "Past [6].", "Past.", nil, 1},
		{"last valid number", "Last [5].", "Last [5].", []int{5}, 0},
		{"huge number", "Huge [99999999999].", "Huge.", nil, 1},
		{"comma list keeps valid and drops invalid", "Both [1,9].", "Both [1].", []int{1}, 1},
		{"comma list with spaces", "Both [1, 2].", "Both [1][2].", []int{1, 2}, 0},
		{"range expands", "Range [2-4].", "Range [2][3][4].", []int{2, 3, 4}, 0},
		{"range partly out of bounds", "Range [4-8].", "Range [4][5].", []int{4, 5}, 3},
		{"very wide range is one invalid", "Wide [1-9999].", "Wide.", nil, 1},
		{"reversed range is invalid", "Back [4-2].", "Back.", nil, 1},
		{"adjacent markers", "Both [1][3].", "Both [1][3].", []int{1, 3}, 0},
		{"adjacent with one invalid", "Both [1][9].", "Both [1].", []int{1}, 1},
		{"duplicates inside one marker", "Dup [2,2].", "Dup [2].", []int{2}, 0},
		{"no markers", "Plain answer.", "Plain answer.", nil, 0},
		{"empty answer", "", "", nil, 0},
		{"marker at the start", "[1] starts the answer.", "[1] starts the answer.", []int{1}, 0},
		{"marker after opening bracket", "Details ([2]).", "Details ([2]).", []int{2}, 0},
		{"invalid marker mid sentence", "The code [9] runs.", "The code runs.", nil, 1},
		{"invalid marker at the end without a period", "It runs [9]", "It runs", nil, 1},
		{"unicode text around markers", "H\u00e9llo \u4e16\u754c [2].", "H\u00e9llo \u4e16\u754c [2].", []int{2}, 0},
		{"marker right after a unicode letter is not a citation", "caf\u00e9[2]", "caf\u00e9[2]", nil, 0},
		{"negative is not a marker", "Value [-1] stays.", "Value [-1] stays.", nil, 0},
		{"text brackets that are not numbers", "See [note] and [a1].", "See [note] and [a1].", nil, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := Validate(tc.in, five())
			if v.Text != tc.wantText || !reflect.DeepEqual(numbers(v), tc.wantNumbers) || v.Invalid != tc.wantInvalid {
				t.Fatalf("got text=%q numbers=%v invalid=%d\nwant text=%q numbers=%v invalid=%d",
					v.Text, numbers(v), v.Invalid, tc.wantText, tc.wantNumbers, tc.wantInvalid)
			}
		})
	}
}

func TestMarkersInsideCodeAreLeftAlone(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"index glued to an identifier", "The loop reads items[1] and items[9]."},
		{"inline code span", "Use `pool[9]` and `x [9]` here."},
		{"fenced block", "Example:\n```go\nv := a[9] // [9]\n```\nDone."},
		{"long fence", "Example:\n````\n[9]\n````\nDone."},
		{"double index", "It calls grid[1][9] twice."},
		{"unclosed fence masks the rest", "Code:\n```\nx := [9]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := Validate(tc.in, five())
			if v.Text != tc.in || v.Invalid != 0 || len(v.Citations) != 0 {
				t.Fatalf("code was modified: %q -> %q (invalid %d, citations %d)", tc.in, v.Text, v.Invalid, len(v.Citations))
			}
		})
	}
}

func TestRealCitationsStillWorkNextToCode(t *testing.T) {
	in := "It reads `items[1]` [2] and then calls `run()` [9].\nSecond line [3]."
	v := Validate(in, five())
	want := "It reads `items[1]` [2] and then calls `run()`.\nSecond line [3]."
	if v.Text != want || !reflect.DeepEqual(numbers(v), []int{2, 3}) || v.Invalid != 1 {
		t.Fatalf("text=%q numbers=%v invalid=%d", v.Text, numbers(v), v.Invalid)
	}
}

func TestInlineCodeDoesNotSpanLines(t *testing.T) {
	// a stray backtick must not switch off validation for the rest of the answer
	v := Validate("A stray ` tick.\nMade up [9].", five())
	if v.Text != "A stray ` tick.\nMade up." || v.Invalid != 1 {
		t.Fatalf("text=%q invalid=%d", v.Text, v.Invalid)
	}
}

func TestCitationFieldsComeFromTheChunksNotTheAnswer(t *testing.T) {
	v := Validate("See [2]. The file is evil.go:1-99 and content is FAKE.", five())
	if len(v.Citations) != 1 {
		t.Fatalf("citations = %v", v.Citations)
	}
	c := v.Citations[0]
	if c.N != 2 || c.File != "f2.go" || c.StartLine != 20 || c.EndLine != 25 || c.Symbol != "Sym2" ||
		c.Kind != "function" || c.Language != "go" || c.Snippet != "content of chunk 2" {
		t.Fatalf("citation = %+v", c)
	}
}

func TestNoUsedChunksMakesEveryMarkerInvalid(t *testing.T) {
	v := Validate("Claim [1] and [2].", nil)
	if v.Text != "Claim and." || v.Invalid != 2 || v.Grounded {
		t.Fatalf("v = %+v", v)
	}
}

func TestRefusalDetection(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		refused bool
	}{
		{"exact", "Not enough evidence in the retrieved code.", true},
		{"with the missing part", "Not enough evidence in the retrieved code. The billing code is missing.", true},
		{"different case", "NOT ENOUGH EVIDENCE IN THE RETRIEVED CODE. Nothing about it.", true},
		{"leading whitespace", "\n  Not enough evidence in the retrieved code.", true},
		{"in the middle is not a refusal", "The code shows X [1]. Not enough evidence in the retrieved code for Y.", false},
		{"paraphrase is not a refusal", "There is not enough evidence.", false},
		{"normal answer", "It works like this [1].", false},
	}
	for _, tc := range tests {
		v := Validate(tc.in, five())
		if v.Refused != tc.refused {
			t.Errorf("%s: refused=%v, want %v", tc.name, v.Refused, tc.refused)
		}
	}
}

func TestGroundedRules(t *testing.T) {
	if v := Validate("Cited [1].", five()); !v.Grounded {
		t.Error("a valid citation is grounded")
	}
	if v := Validate("No citations at all.", five()); v.Grounded {
		t.Error("an uncited answer is not grounded")
	}
	if v := Validate("Only a fake one [9].", five()); v.Grounded {
		t.Error("an answer whose only citation was invalid is not grounded")
	}
	if v := Validate(RefusalSentence+" Nothing about that.", five()); !v.Grounded || !v.Refused {
		t.Error("a refusal is grounded")
	}
}

func TestExpandHelper(t *testing.T) {
	v, bad := expand("1,3-4, 9,3", 5)
	if !reflect.DeepEqual(v, []int{1, 3, 4}) || bad != 1 {
		t.Fatalf("v=%v bad=%d", v, bad)
	}
	v, bad = expand("2-2", 5)
	if !reflect.DeepEqual(v, []int{2}) || bad != 0 {
		t.Fatalf("v=%v bad=%d", v, bad)
	}
}

func TestValidateDoesNotPanicOnHostileInput(t *testing.T) {
	inputs := []string{
		strings.Repeat("[1]", 5000), strings.Repeat("`", 999), "[", "]", "[[1]]", "[1", "1]", "[1,]", "[,1]", "[1-]", "[-1-2]",
		strings.Repeat("[", 1000) + strings.Repeat("]", 1000), "\x00[1]\x00", "\xff\xfe[1]", "```", "`", "[1]`[2]`[3]",
	}
	for _, in := range inputs {
		_ = Validate(in, five())
	}
}

func TestCodeMask(t *testing.T) {
	text := "a `b` c ```\nd\n``` e"
	mask := codeMask(text)
	var masked strings.Builder
	for i := range text {
		if mask[i] {
			masked.WriteByte(text[i])
		}
	}
	if got := masked.String(); got != "`b`"+"```\nd\n```" {
		t.Fatalf("masked %q", got)
	}
}
