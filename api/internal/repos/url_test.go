package repos

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

type urlCase struct {
	Name      string `json:"name"`
	Input     string `json:"input"`
	Valid     bool   `json:"valid"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	Canonical string `json:"canonical"`
}

// The same fixture is used by the Python worker's validator so the two cannot drift.
func TestParseGitHubURL_SharedFixture(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/urls.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []urlCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 50 {
		t.Fatalf("fixture looks truncated: %d cases", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			ref, err := ParseGitHubURL(tc.Input)
			if !tc.Valid {
				if err == nil {
					t.Fatalf("accepted %q as %+v", tc.Input, ref)
				}
				if !errors.Is(err, ErrInvalidURL) {
					t.Fatalf("error does not wrap ErrInvalidURL: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("rejected %q: %v", tc.Input, err)
			}
			if ref.Owner != tc.Owner || ref.Name != tc.Repo || ref.URL != tc.Canonical {
				t.Fatalf("got %+v, want owner=%s repo=%s url=%s", ref, tc.Owner, tc.Repo, tc.Canonical)
			}
		})
	}
}

func TestParseGitHubURL_Lengths(t *testing.T) {
	owner39 := strings.Repeat("a", 39)
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"owner 39 chars", "https://github.com/" + owner39 + "/b", true},
		{"owner 40 chars", "https://github.com/" + owner39 + "a/b", false},
		{"name 100 chars", "https://github.com/a/" + strings.Repeat("b", 100), true},
		{"name 101 chars", "https://github.com/a/" + strings.Repeat("b", 101), false},
		{"huge input", strings.Repeat("a", 10000), false},
		{"long but under cap with bad host", "https://" + strings.Repeat("a", 150) + ".com/a/b", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseGitHubURL(tc.input)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
		})
	}
}

func TestParseGitHubURL_CanonicalIgnoresInputForm(t *testing.T) {
	forms := []string{
		"https://github.com/Foo/Bar",
		"https://github.com/Foo/Bar.git",
		"https://github.com/Foo/Bar/",
		"HTTPS://GITHUB.COM/Foo/Bar",
	}
	for _, f := range forms {
		ref, err := ParseGitHubURL(f)
		if err != nil {
			t.Fatalf("%q: %v", f, err)
		}
		if ref.URL != "https://github.com/Foo/Bar" {
			t.Fatalf("%q canonicalised to %q", f, ref.URL)
		}
	}
}
