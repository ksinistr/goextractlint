package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goextractlint/internal/cache"
	"goextractlint/internal/domain"
	"goextractlint/internal/system1"
)

type recordingStore struct {
	underlying     cache.DecisionStore
	getKey, putKey string
	fail           bool
}

const (
	targetName = "Target"
	testModel  = "model"
)

func (s *recordingStore) Get(key string) (domain.Decision, bool, error) {
	s.getKey = key
	if s.fail {
		return domain.Decision{}, false, errors.New("read denied")
	}

	decision, hit, err := s.underlying.Get(key)
	if err != nil {
		return decision, hit, fmt.Errorf("read underlying store: %w", err)
	}

	return decision, hit, nil
}

func (s *recordingStore) Put(key string, d domain.Decision) error {
	s.putKey = key
	if s.fail {
		return errors.New("write denied")
	}

	if err := s.underlying.Put(key, d); err != nil {
		return fmt.Errorf("write underlying store: %w", err)
	}

	return nil
}

func TestCacheRequests(t *testing.T) {
	for _, verdict := range []domain.Verdict{domain.Extract, domain.Keep, domain.InsufficientContext} {
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

			store := &recordingStore{underlying: cache.New(t.TempDir())}
			client := cache.NewCachedAnalyzer(
				newSystem1Analyzer(system1.NewClient(server.Client(), "secret")),
				store,
				false,
				io.Discard,
			)

			function := domain.Function{File: testSourcePath, Name: targetName, Source: "func Target(){}"}
			for range 2 {
				d, err := client.Analyze(context.Background(), prepared(t, server.URL, testModel, function))
				if err != nil || d.Verdict != verdict {
					t.Fatalf("decision=%+v err=%v", d, err)
				}
			}

			if calls != 1 || store.getKey != store.putKey ||
				store.getKey != cache.RequestKey(server.URL, testModel, body) {
				t.Fatalf("calls=%d keys=%s %s", calls, store.getKey, store.putKey)
			}

			variants := []struct {
				name, model, endpoint string
				function              domain.Function
			}{
				{
					name:     "source",
					model:    testModel,
					endpoint: server.URL,
					function: domain.Function{
						File:   testSourcePath,
						Name:   targetName,
						Source: "func Target(){ println(1) }",
					},
				},
				{name: testModel, model: unknownName, endpoint: server.URL, function: function},
				{name: "endpoint", model: testModel, endpoint: server.URL + "/other", function: function},
				{
					name:     "imports",
					model:    testModel,
					endpoint: server.URL,
					function: domain.Function{
						File:    testSourcePath,
						Name:    targetName,
						Source:  function.Source,
						Context: []domain.ContextItem{{Kind: "import", Source: `"fmt"`}},
					},
				},
				{
					name:     typeContextKind,
					model:    testModel,
					endpoint: server.URL,
					function: domain.Function{
						File:    testSourcePath,
						Name:    targetName,
						Source:  function.Source,
						Context: []domain.ContextItem{{Kind: typeContextKind, Source: "type Box int"}},
					},
				},
				{
					name:     "truncated",
					model:    testModel,
					endpoint: server.URL,
					function: domain.Function{
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

					c := cache.NewCachedAnalyzer(
						newSystem1Analyzer(system1.NewClient(server.Client(), "secret")),
						store,
						false,
						io.Discard,
					)
					if _, err := c.Analyze(context.Background(), prepared(t, v.endpoint, v.model, v.function)); err != nil {
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
				if _, err := client.Analyze(context.Background(), request); err != nil || calls != before+1 {
					t.Fatalf("exact bytes calls=%d err=%v", calls, err)
				}
			}

			refresh := cache.NewCachedAnalyzer(
				newSystem1Analyzer(system1.NewClient(server.Client(), "secret")),
				store,
				true,
				io.Discard,
			)

			before := calls
			if _, err := refresh.Analyze(context.Background(), prepared(t, server.URL, testModel, function)); err != nil ||
				calls != before+1 {
				t.Fatalf("refresh calls=%d err=%v", calls, err)
			}

			before = calls
			if _, err := client.Analyze(context.Background(), prepared(t, server.URL, testModel, function)); err != nil ||
				calls != before {
				t.Fatalf("refreshed decision was not stored: %v", err)
			}

			store.fail = true

			var diagnostics bytes.Buffer

			failing := cache.NewCachedAnalyzer(
				newSystem1Analyzer(system1.NewClient(server.Client(), "secret")),
				store,
				false,
				&diagnostics,
			)

			before = calls
			if _, err := failing.Analyze(context.Background(), prepared(t, server.URL, testModel, function)); err != nil ||
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
			if cache.RequestKey("endpoint", testModel, tc.a) == cache.RequestKey("endpoint", testModel, tc.b) {
				t.Fatal("identity did not change")
			}
		})
	}
}

func prepared(t *testing.T, endpoint, model string, function domain.Function) preparedRequest {
	t.Helper()

	request, err := prepareAnalysis(backend{endpoint: endpoint, model: model, maxTokens: openRouterMaxTokens}, function)
	if err != nil {
		t.Fatal(err)
	}

	return request
}
