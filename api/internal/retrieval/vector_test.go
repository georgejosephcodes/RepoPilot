package retrieval

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestVectorLiteralFormat(t *testing.T) {
	got, err := VectorLiteral([]float32{1, 0, -0.5, 0.1, 1e-9, 123456.78}, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") || strings.ContainsAny(got, " \n") {
		t.Fatalf("bad literal %q", got)
	}
	parts := strings.Split(strings.Trim(got, "[]"), ",")
	if len(parts) != 6 {
		t.Fatalf("parts = %d", len(parts))
	}
	want := []float32{1, 0, -0.5, 0.1, 1e-9, 123456.78}
	for i, p := range parts {
		f, err := strconv.ParseFloat(p, 32)
		if err != nil || float32(f) != want[i] {
			t.Fatalf("part %d = %q does not round-trip to %v", i, p, want[i])
		}
	}
	if strings.Contains(got, "NaN") || strings.Contains(got, "Inf") {
		t.Fatal("literal must never contain NaN or Inf")
	}
}

func TestVectorLiteralIsShortestExactForm(t *testing.T) {
	got, _ := VectorLiteral([]float32{0.1, 0.25}, 2)
	if got != "[0.1,0.25]" {
		t.Fatalf("got %q", got)
	}
}

func TestVectorLiteralRejectsBadInput(t *testing.T) {
	if _, err := VectorLiteral([]float32{1, 2, 3}, 4); !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("err = %v", err)
	}
	if _, err := VectorLiteral(nil, 2); !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("err = %v", err)
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := VectorLiteral([]float32{1, float32(bad)}, 2); !errors.Is(err, ErrInvalidVector) {
			t.Fatalf("%v: err = %v", bad, err)
		}
	}
}

func TestVectorLiteralFullSize(t *testing.T) {
	v := make([]float32, 2048)
	for i := range v {
		v[i] = float32(i) / 3000
	}
	got, err := VectorLiteral(v, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, ",") != 2047 {
		t.Fatalf("commas = %d", strings.Count(got, ","))
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, tc := range []struct {
		v    string
		want bool
	}{
		{"0.8.6", true}, {"0.8.0", true}, {"0.7.9", false}, {"0.10.0", true}, {"1.0.0", true},
		{"0.8", true}, {"0.7", false}, {"0.8.0-dev", true}, {"", false}, {"abc", false},
	} {
		if got := versionAtLeast(tc.v, 0, 8, 0); got != tc.want {
			t.Errorf("versionAtLeast(%q, 0.8.0) = %v, want %v", tc.v, got, tc.want)
		}
	}
}
