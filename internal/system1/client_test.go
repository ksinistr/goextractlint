package system1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type httpFunc func(*http.Request) (*http.Response, error)

func (f httpFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestCall(t *testing.T) {
	for _, tc := range []struct {
		name, path, response, key string
		status                    int
		canceled                  bool
		wantErr                   string
	}{
		{name: "decisions", path: "/api/alpha/decisions", response: `{"answers":{}}`, status: 200, key: testKey},
		{name: "without key", path: "/v1/systemone", response: `{"answers":{}}`, status: 200},
		{name: "another endpoint", path: otherEndpoint, response: `{"result":"ok"}`, status: 200, key: testKey},
		{name: "HTTP error", path: otherEndpoint, response: `{"error":"denied"}`, status: 403, wantErr: "HTTP 403", key: testKey},
		{name: "invalid JSON", path: otherEndpoint, response: "invalid", status: 200, wantErr: "invalid JSON", key: testKey},
		{name: "canceled", path: otherEndpoint, canceled: true, wantErr: "context canceled", key: testKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if tc.canceled {
				cancel()
			}

			client := NewClient(httpFunc(func(r *http.Request) (*http.Response, error) {
				if err := r.Context().Err(); err != nil {
					return nil, fmt.Errorf("test transport: %w", err)
				}

				body, err := io.ReadAll(r.Body)
				if err != nil {
					return nil, fmt.Errorf("test transport: %w", err)
				}

				if r.URL.Path != tc.path || r.Method != http.MethodPost || string(body) != `{"input":1}` ||
					r.Header.Get("Authorization") != authorization(tc.key) ||
					r.Header.Get("Content-Type") != "application/json" {
					t.Fatal("incorrect request")
				}

				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.response))}, nil
			}), tc.key)

			body, err := client.Call(
				ctx,
				PreparedRequest{Endpoint: "https://example.test" + tc.path, Body: []byte(`{"input":1}`)},
			)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v", err)
				}

				if tc.canceled && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}

				return
			}

			if err != nil || string(body) != tc.response {
				t.Fatalf("body=%s error=%v", body, err)
			}
		})
	}
}

const otherEndpoint = "/api/other"

func authorization(key string) string {
	if key == "" {
		return ""
	}

	return "Bearer " + key
}

const testKey = "key"
