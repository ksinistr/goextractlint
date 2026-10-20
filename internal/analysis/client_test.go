package analysis

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"goextractlint/internal/source"
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
		{name: "invalid API JSON", body: `upstream error`, wantErr: "invalid JSON"},
		{name: "oversized", body: strings.Repeat(" ", (8<<20)+1), wantErr: "exceeds 8 MiB"},
		{name: "missing answer", body: `{"answers":{}}`, wantErr: "valid verdict"},
		{
			name:    "unknown verdict",
			body:    `{"answers":{"verdict":{"type":"choice","choice":"other"}}}`,
			wantErr: "valid verdict",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			function := source.Function{
				File:      "example.go",
				Name:      "Example",
				Package:   "p",
				StartLine: 2,
				EndLine:   2,
				Source:    "func Example() {}",
				Context: []source.ContextItem{
					{Kind: "type", File: "types.go", StartLine: 2, EndLine: 2, Source: "type Value int"},
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
						Principle string          `json:"principle"`
						Function  source.Function `json:"function"`
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

				if payload.Model != "test/jev" || payload.State.Principle != Principle() ||
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

			client := NewClient(server.Client(), "test-key")

			prepared, err := Prepare(server.URL, "test/jev", function)
			if err != nil {
				t.Fatal(err)
			}

			result, err := client.Analyze(context.Background(), prepared)
			if string(sent) != string(prepared.Body) || prepared.Endpoint != server.URL+"/api/alpha/decisions" ||
				prepared.Model != "test/jev" {
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
