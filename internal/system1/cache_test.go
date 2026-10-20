package system1

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goextractlint/internal/goparse"
)

type recordingStore struct {
	decisions      map[string]Decision
	getKey, putKey string
	fail           bool
}

const (
	targetName     = "Target"
	testModel      = "model"
	testSourcePath = "a.go"
)

func (s *recordingStore) Get(key string) (Decision, bool, error) {
	s.getKey = key
	if s.fail {
		return Decision{}, false, errors.New("read denied")
	}

	decision, hit := s.decisions[key]

	return decision, hit, nil
}

func (s *recordingStore) Put(key string, d Decision) error {
	s.putKey = key
	if s.fail {
		return errors.New("write denied")
	}

	s.decisions[key] = d

	return nil
}

func TestCacheRequests(t *testing.T) {
	for _, verdict := range []Verdict{Extract, Keep, InsufficientContext} {
		t.Run(string(verdict), func(t *testing.T) {
			calls := 0

			var body []byte

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++

				var err error

				body, err = io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}

				if _, err := io.WriteString(w, `{"answers":{"verdict":{"type":"choice","choice":"`+string(verdict)+`"}}}`); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()

			store := &recordingStore{decisions: make(map[string]Decision)}
			client := NewClient(
				server.Client(),
				Config{Endpoint: server.URL, Model: testModel, APIKey: testKey, MaxTokens: 8192},
				store,
				io.Discard,
			)

			function := goparse.Function{File: testSourcePath, Name: targetName, Source: "func Target(){}"}
			for range 2 {
				d, err := client.Evaluate(context.Background(), function)
				if err != nil || d.Verdict != verdict {
					t.Fatalf("decision=%+v err=%v", d, err)
				}
			}

			if calls != 1 || store.getKey != store.putKey ||
				store.getKey != requestKey(server.URL, testModel, body) {
				t.Fatalf("calls=%d keys=%s %s", calls, store.getKey, store.putKey)
			}

			variants := []struct {
				name, model, endpoint string
				function              goparse.Function
			}{
				{
					name:     "source",
					model:    testModel,
					endpoint: server.URL,
					function: goparse.Function{
						File:   testSourcePath,
						Name:   targetName,
						Source: "func Target(){ println(1) }",
					},
				},
				{name: testModel, model: "other", endpoint: server.URL, function: function},
				{name: "endpoint", model: testModel, endpoint: server.URL + "/other", function: function},
				{
					name:     "imports",
					model:    testModel,
					endpoint: server.URL,
					function: goparse.Function{
						File:    testSourcePath,
						Name:    targetName,
						Source:  function.Source,
						Context: []goparse.ContextItem{{Kind: "import", Source: `"fmt"`}},
					},
				},
				{
					name:     typeContextKind,
					model:    testModel,
					endpoint: server.URL,
					function: goparse.Function{
						File:    testSourcePath,
						Name:    targetName,
						Source:  function.Source,
						Context: []goparse.ContextItem{{Kind: typeContextKind, Source: "type Box int"}},
					},
				},
				{
					name:     "truncated",
					model:    testModel,
					endpoint: server.URL,
					function: goparse.Function{
						File:             testSourcePath,
						Name:             targetName,
						Source:           function.Source,
						ContextTruncated: true,
					},
				},
			}
			for _, v := range variants {
				t.Run(v.name, func(t *testing.T) {
					before := calls

					c := NewClient(
						server.Client(),
						Config{
							Endpoint:  v.endpoint,
							Model:     v.model,
							APIKey:    testKey,
							MaxTokens: 8192,
						},
						store,
						io.Discard,
					)
					if _, err := c.Evaluate(context.Background(), v.function); err != nil {
						t.Fatal(err)
					}

					if calls != before+1 {
						t.Fatal("stale decision reused")
					}
				})
			}

			base := prepared(t, server.URL, testModel, function)
			for _, changed := range [][]byte{append(append([]byte(nil), base.Body...), ' '), bytes.Replace(base.Body, []byte("Using the principle"), []byte("Apply the principle"), 1)} {
				before := calls
				request := base

				request.Body = changed
				if _, err := client.evaluateRequest(context.Background(), request); err != nil || calls != before+1 {
					t.Fatalf("exact bytes calls=%d err=%v", calls, err)
				}
			}

			refresh := NewClient(
				server.Client(),
				Config{Endpoint: server.URL, Model: testModel, APIKey: testKey, MaxTokens: 8192, RefreshCache: true},
				store,
				io.Discard,
			)

			before := calls
			if _, err := refresh.Evaluate(context.Background(), function); err != nil ||
				calls != before+1 {
				t.Fatalf("refresh calls=%d err=%v", calls, err)
			}

			before = calls
			if _, err := client.Evaluate(context.Background(), function); err != nil ||
				calls != before {
				t.Fatalf("refreshed decision was not stored: %v", err)
			}

			store.fail = true

			var diagnostics bytes.Buffer

			failing := NewClient(
				server.Client(),
				Config{Endpoint: server.URL, Model: testModel, APIKey: testKey, MaxTokens: 8192},
				store,
				&diagnostics,
			)

			before = calls
			if _, err := failing.Evaluate(context.Background(), function); err != nil ||
				calls != before+1 ||
				!strings.Contains(diagnostics.String(), "read denied") ||
				!strings.Contains(diagnostics.String(), "write denied") {
				t.Fatalf("fallback calls=%d err=%v diagnostics=%s", calls, err, diagnostics.String())
			}
		})
	}
}

func TestRequestKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b []byte
	}{
		{"principle", []byte(`{"principle":"old"}`), []byte(`{"principle":"new"}`)},
		{"exact bytes", []byte(`{"a":1}`), []byte(`{ "a":1 }`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if requestKey("endpoint", testModel, tc.a) == requestKey("endpoint", testModel, tc.b) {
				t.Fatal("identity did not change")
			}
		})
	}
}

func prepared(t *testing.T, endpoint, model string, function goparse.Function) preparedRequest {
	t.Helper()

	request, err := prepareRequest(Config{Endpoint: endpoint, Model: model, MaxTokens: 8192}, function)
	if err != nil {
		t.Fatal(err)
	}

	return request
}
