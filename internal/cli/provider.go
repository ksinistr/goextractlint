package cli

import (
	"cmp"
	"fmt"
	"net/url"
	"strings"
)

const (
	openRouterProvider = "openrouter"
	ollamaProvider     = "ollama"
)

const (
	openRouterMaxTokens = 8192
	ollamaMaxTokens     = 2048
)

type backend struct {
	endpoint, model, apiKey string
	maxTokens               int
}

type backendOptions struct {
	provider, endpoint, model, key string
	maxTokens                      int
}

func resolveBackend(options backendOptions) (backend, error) {
	selected, err := providerBackend(options.provider, options.model, options.key)
	if err != nil {
		return backend{}, err
	}

	selected.maxTokens = cmp.Or(options.maxTokens, selected.maxTokens)

	if options.endpoint == "" {
		return selected, nil
	}

	if err := validateEndpoint(options.endpoint); err != nil {
		return backend{}, err
	}

	selected.endpoint = options.endpoint

	return selected, nil
}

func providerBackend(provider, model, key string) (backend, error) {
	switch provider {
	case openRouterProvider:
		key = strings.TrimSpace(key)
		if key == "" {
			return backend{}, fmt.Errorf("set OPENROUTER_API_KEY")
		}

		return backend{
			endpoint:  "https://openrouter.ai/api/alpha/decisions",
			model:     cmp.Or(model, "typesafe/jev-1.13"),
			apiKey:    key,
			maxTokens: openRouterMaxTokens,
		}, nil
	case ollamaProvider:
		return backend{
			endpoint:  "http://localhost:11434/v1/systemone",
			model:     cmp.Or(model, "tev1:4b-mxfp8"),
			maxTokens: ollamaMaxTokens,
		}, nil
	default:
		return backend{}, fmt.Errorf("unknown provider %q", provider)
	}
}

func validateEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse endpoint: %w", err)
	}

	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("endpoint must be an absolute http or https URL")
	}

	return nil
}
