package system1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"goextractlint/internal/goparse"
)

const (
	evaluationTestModel = "test/jev"
	typeContextKind     = "type"
	emptyAnswers        = `{"answers":{}}`
	invalidJSON         = "invalid JSON"
)

func TestJevResponses(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		status  int
		wantErr string
	}{
		{
			name: "extract",
			body: `{"answers":{"verdict":{"type":"choice","choice":"extract","confidence":0.8,"probabilities":{"extract":0.9,"keep":0.1,"insufficient_context":0}}},"usage":{"cost":0.0001}}`,
		},
		{name: "keep", body: `{"answers":{"verdict":{"type":"choice","choice":"keep"}}}`},
		{
			name: "insufficient context",
			body: `{"answers":{"verdict":{"type":"choice","choice":"insufficient_context"}}}`,
		},
		{name: "HTTP failure", status: 401, body: `{"error":{"message":"unauthorized"}}`, wantErr: "HTTP 401"},
		{name: "error in successful envelope", body: `{"error":{"message":"provider failed"}}`, wantErr: "API error"},
		{name: "invalid API JSON", body: `upstream error`, wantErr: invalidJSON},
		{name: "oversized", body: strings.Repeat(" ", (8<<20)+1), wantErr: "exceeds 8 MiB"},
		{name: "missing answer", body: emptyAnswers, wantErr: "valid verdict"},
		{
			name:    "unknown verdict",
			body:    `{"answers":{"verdict":{"type":"choice","choice":"other"}}}`,
			wantErr: "valid verdict",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			function := goparse.Function{
				File:      "example.go",
				Name:      "Example",
				Package:   "p",
				StartLine: 2,
				EndLine:   2,
				Source:    "func Example() {}",
				Context: []goparse.ContextItem{
					{Kind: typeContextKind, Source: "type Value int"},
				},
			}

			var sent []byte

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/alpha/decisions" || r.Method != http.MethodPost ||
					r.Header.Get("Authorization") != "Bearer test-key" ||
					r.Header.Get("Content-Type") != "application/json" {
					t.Error("incorrect endpoint, method or authorization")
				}

				var payload struct {
					Model string `json:"model"`
					State struct {
						Principle string           `json:"principle"`
						Function  goparse.Function `json:"function"`
					} `json:"state"`
					Questions map[string]any `json:"questions"`
				}

				var err error

				sent, err = io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}

				if err := json.Unmarshal(sent, &payload); err != nil {
					t.Error(err)
				}

				if payload.Model != evaluationTestModel || payload.State.Principle != principle() ||
					!reflect.DeepEqual(payload.State.Function, function) ||
					payload.Questions["verdict"] == nil {
					t.Error("incorrect model, function or instructions")
				}

				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}

				if _, err := io.WriteString(w, tc.body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()

			config := Config{
				Endpoint:  server.URL + "/api/alpha/decisions",
				Model:     evaluationTestModel,
				APIKey:    "test-key",
				MaxTokens: 8192,
			}
			client := NewClient(server.Client(), config, nil, io.Discard)

			prepared, err := prepareRequest(
				Config{
					Endpoint:  server.URL + "/api/alpha/decisions",
					Model:     evaluationTestModel,
					MaxTokens: 8192,
				},
				function,
			)
			if err != nil {
				t.Fatal(err)
			}

			result, err := client.Evaluate(context.Background(), function)

			if string(sent) != string(prepared.Body) || prepared.Endpoint != server.URL+"/api/alpha/decisions" ||
				prepared.Model != evaluationTestModel {
				t.Fatal("prepared request differs from HTTP")
			}

			if tc.wantErr == "" {
				if err != nil || result.Validate() != nil {
					t.Fatalf("result = %+v", result)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want %q", err, tc.wantErr)
			}
		})
	}
}

func Test_fitContext(t *testing.T) {
	small := goparse.ContextItem{Kind: typeContextKind, Source: "type Small int"}
	other := goparse.ContextItem{Kind: typeContextKind, Source: "type Other int"}
	huge := goparse.ContextItem{
		Kind:   typeContextKind,
		Source: "type Huge struct{ F string `" + strings.Repeat("x", 8000) + "` }",
	}

	for _, tc := range []struct {
		name          string
		context       []goparse.ContextItem
		truncated     bool
		maxTokens     int
		want          []goparse.ContextItem
		wantTruncated bool
	}{
		{
			name:      "all fit",
			context:   []goparse.ContextItem{small, other},
			maxTokens: 2048,
			want:      []goparse.ContextItem{small, other},
		},
		{
			name:          "skip large item and include following",
			context:       []goparse.ContextItem{huge, small},
			maxTokens:     2048,
			want:          []goparse.ContextItem{small},
			wantTruncated: true,
		},
		{
			name:          "nothing fits",
			context:       []goparse.ContextItem{small},
			maxTokens:     1,
			wantTruncated: true,
		},
		{
			name:          "keeps earlier truncation",
			context:       []goparse.ContextItem{small},
			truncated:     true,
			maxTokens:     2048,
			want:          []goparse.ContextItem{small},
			wantTruncated: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			function := goparse.Function{
				File:             "example.go",
				Name:             "Example",
				Source:           "func Example() {}",
				Context:          tc.context,
				ContextTruncated: tc.truncated,
			}
			target := Config{Model: evaluationTestModel, MaxTokens: tc.maxTokens}

			got, err := fitContext(target, function)
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got.Context, tc.want) || got.ContextTruncated != tc.wantTruncated {
				t.Fatalf("context=%+v truncated=%v", got.Context, got.ContextTruncated)
			}

			data, err := encodeRequest(target.Model, got)
			if err != nil {
				t.Fatal(err)
			}

			if len(got.Context) > 0 && estimatedTokens(data) > tc.maxTokens {
				t.Fatalf("estimated tokens %d exceed %d", estimatedTokens(data), tc.maxTokens)
			}
		})
	}
}
