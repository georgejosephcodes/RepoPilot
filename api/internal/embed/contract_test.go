package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

// The same fixture is loaded by the Python worker's tests (worker/tests/test_embed_contract.py).
type contract struct {
	Defaults struct {
		BaseURL        string `json:"base_url"`
		Model          string `json:"model"`
		Dimension      int    `json:"dimension"`
		SendDimensions bool   `json:"send_dimensions"`
		InputTypes     bool   `json:"input_types"`
		QueryType      string `json:"query_type"`
		MaxChars       int    `json:"max_chars"`
	} `json:"defaults"`
	RequestCases []struct {
		Name   string `json:"name"`
		Config struct {
			Model          string `json:"model"`
			Dimension      int    `json:"dimension"`
			SendDimensions bool   `json:"send_dimensions"`
			InputTypes     bool   `json:"input_types"`
			QueryType      string `json:"query_type"`
		} `json:"config"`
		Kind  string         `json:"kind"`
		Texts []string       `json:"texts"`
		Body  map[string]any `json:"body"`
	} `json:"request_cases"`
	Truncation []struct {
		MaxChars int    `json:"max_chars"`
		Input    string `json:"input"`
		Expected string `json:"expected"`
	} `json:"truncation"`
	Normalization []struct {
		Raw      []float64 `json:"raw"`
		Expected []float64 `json:"expected"`
	} `json:"normalization"`
}

func loadContract(t *testing.T) contract {
	t.Helper()
	data, err := os.ReadFile("../../../testdata/embed_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var c contract
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestContractDefaults(t *testing.T) {
	want := loadContract(t).Defaults
	got, err := ConfigFromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != want.BaseURL || got.Model != want.Model || got.Dimension != want.Dimension ||
		got.SendDimensions != want.SendDimensions || got.InputTypes != want.InputTypes || got.MaxChars != want.MaxChars ||
		got.QueryType != want.QueryType {
		t.Fatalf("defaults differ from the contract: %+v vs %+v", got, want)
	}
}

func TestContractRequestBodies(t *testing.T) {
	for _, tc := range loadContract(t).RequestCases {
		if tc.Kind != "query" { // documents are sent by the Python worker only
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&got)
				vec := make([]float64, tc.Config.Dimension)
				vec[0] = 1
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vec}}})
			}))
			defer srv.Close()

			cfg := DefaultConfig()
			cfg.BaseURL, cfg.APIKey = srv.URL, "k"
			cfg.Model, cfg.Dimension = tc.Config.Model, tc.Config.Dimension
			cfg.SendDimensions, cfg.InputTypes = tc.Config.SendDimensions, tc.Config.InputTypes
			if tc.Config.QueryType != "" {
				cfg.QueryType = tc.Config.QueryType
			}
			e, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.EmbedQuery(context.Background(), tc.Texts[0]); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.Body) {
				t.Fatalf("request body differs from the contract:\n got  %v\n want %v", got, tc.Body)
			}
		})
	}
}

func TestContractTruncation(t *testing.T) {
	for _, tc := range loadContract(t).Truncation {
		cfg := DefaultConfig()
		cfg.APIKey, cfg.MaxChars = "k", tc.MaxChars
		e, _ := New(cfg)
		if got := e.Prepare(tc.Input); got != tc.Expected {
			t.Errorf("Prepare(%q) with max %d = %q, want %q", tc.Input, tc.MaxChars, got, tc.Expected)
		}
	}
}

func TestContractNormalization(t *testing.T) {
	for _, tc := range loadContract(t).Normalization {
		got, err := Normalize(tc.Raw)
		if err != nil {
			t.Fatal(err)
		}
		for i := range got {
			if math.Abs(float64(got[i])-tc.Expected[i]) > 1e-6 {
				t.Errorf("Normalize(%v) = %v, want %v", tc.Raw, got, tc.Expected)
				break
			}
		}
	}
}
