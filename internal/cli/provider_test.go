package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func Test_resolveBackend(t *testing.T) {
	for _, tc := range []struct {
		name, provider, endpoint, model, key string
		maxTokens                            int
		want                                 backend
		wantErr                              bool
	}{
		{
			name:     "openrouter defaults",
			provider: openRouterProvider,
			key:      " key ",
			want: backend{
				endpoint:  openRouterEndpoint,
				model:     jevModel,
				apiKey:    testAPIKey,
				maxTokens: openRouterMaxTokens,
			},
		},
		{
			name:     "openrouter model override",
			provider: openRouterProvider,
			model:    "typesafe/other",
			key:      testAPIKey,
			want: backend{
				endpoint:  openRouterEndpoint,
				model:     "typesafe/other",
				apiKey:    testAPIKey,
				maxTokens: openRouterMaxTokens,
			},
		},
		{name: "openrouter without key", provider: openRouterProvider, key: " ", wantErr: true},
		{
			name:     "ollama ignores key",
			provider: ollamaProvider,
			key:      testAPIKey,
			want:     backend{endpoint: ollamaEndpoint, model: tevModel, maxTokens: ollamaMaxTokens},
		},
		{
			name:     "ollama model override",
			provider: ollamaProvider,
			model:    customModel,
			want:     backend{endpoint: ollamaEndpoint, model: customModel, maxTokens: ollamaMaxTokens},
		},
		{
			name:      "ollama max tokens override",
			provider:  ollamaProvider,
			maxTokens: 8192,
			want:      backend{endpoint: ollamaEndpoint, model: tevModel, maxTokens: 8192},
		},
		{name: "unknown", provider: unknownName, key: testAPIKey, wantErr: true},
		{
			name:     "openrouter custom endpoint",
			provider: openRouterProvider,
			endpoint: customEndpoint,
			key:      testAPIKey,
			want: backend{
				endpoint:  customEndpoint,
				model:     jevModel,
				apiKey:    testAPIKey,
				maxTokens: openRouterMaxTokens,
			},
		},
		{
			name:     "ollama custom endpoint",
			provider: ollamaProvider,
			endpoint: customEndpoint,
			want:     backend{endpoint: customEndpoint, model: tevModel, maxTokens: ollamaMaxTokens},
		},
		{name: "relative endpoint", provider: ollamaProvider, endpoint: "/v1/systemone", wantErr: true},
		{name: "unsupported scheme", provider: ollamaProvider, endpoint: "ftp://host/decisions", wantErr: true},
		{name: "malformed endpoint", provider: ollamaProvider, endpoint: "http://[::1", wantErr: true},
		{name: "endpoint without key", provider: openRouterProvider, endpoint: customEndpoint, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveBackend(backendOptions{
				provider:  tc.provider,
				endpoint:  tc.endpoint,
				model:     tc.model,
				key:       tc.key,
				maxTokens: tc.maxTokens,
			})
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("backend=%+v err=%v", got, err)
			}
		})
	}
}

func TestRunCLI_provider(t *testing.T) {
	for _, tc := range []struct {
		name, key, url, model, authorization string
		args                                 []string
	}{
		{
			name:          "openrouter",
			key:           testAPIKey,
			url:           openRouterEndpoint,
			model:         jevModel,
			authorization: "Bearer key",
		},
		{
			name:  "ollama",
			args:  []string{providerFlag, ollamaProvider},
			url:   ollamaEndpoint,
			model: tevModel,
		},
		{
			name:  "ollama with key and model",
			key:   testAPIKey,
			args:  []string{providerFlag, ollamaProvider, modelFlag, customModel},
			url:   ollamaEndpoint,
			model: customModel,
		},
		{
			name:  "ollama custom endpoint",
			args:  []string{providerFlag, ollamaProvider, endpointFlag, customEndpoint},
			url:   customEndpoint,
			model: tevModel,
		},
		{
			name:          "openrouter custom endpoint",
			key:           testAPIKey,
			args:          []string{endpointFlag, customEndpoint},
			url:           customEndpoint,
			model:         jevModel,
			authorization: "Bearer key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			write(t, root, testSourcePath, "package p\nfunc A(){}")

			calls := 0
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++

				var payload struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}

				if r.URL.String() != tc.url || payload.Model != tc.model ||
					r.Header.Get("Authorization") != tc.authorization {
					t.Errorf("url=%s model=%s authorization=%q", r.URL, payload.Model, r.Header.Get("Authorization"))
				}

				return response(200, answer(keepAnswer)), nil
			})}

			var out, status bytes.Buffer

			args := append(append([]string{noCacheFlag}, tc.args...), testSourcePath)

			err := Run(context.Background(), args, tc.key, client, nil, failReader{t}, &out, &status)
			if err != nil || calls != 1 {
				t.Fatalf("err=%v calls=%d stderr=%s", err, calls, &status)
			}
		})
	}
}

const (
	openRouterEndpoint = "https://openrouter.ai/api/alpha/decisions"
	ollamaEndpoint     = "http://localhost:11434/v1/systemone"
	customModel        = "custom:latest"
	providerFlag       = "-provider"
	modelFlag          = "-jev-model"
	unknownName        = "other"
	endpointFlag       = "-endpoint"
	jevModel           = "typesafe/jev-1.13"
	tevModel           = "tev1:4b-mxfp8"
	customEndpoint     = "https://decisions.example.test/v1/decide"
)
