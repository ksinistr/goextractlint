package system1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"goextractlint/internal/goparse"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type preparedRequest struct {
	Endpoint string
	Model    string
	Body     []byte
}

type Config struct {
	Endpoint     string
	Model        string
	APIKey       string
	MaxTokens    int
	RefreshCache bool
}

type Client struct {
	http        HTTPClient
	config      Config
	store       DecisionStore
	diagnostics io.Writer
}

func NewClient(client HTTPClient, config Config, store DecisionStore, diagnostics io.Writer) *Client {
	return &Client{http: client, config: config, store: store, diagnostics: diagnostics}
}

func (c *Client) Evaluate(ctx context.Context, function goparse.Function) (Decision, error) {
	request, err := prepareRequest(c.config, function)
	if err != nil {
		return Decision{}, fmt.Errorf("prepare evaluation request: %w", err)
	}

	return c.evaluateRequest(ctx, request)
}

func (c *Client) call(ctx context.Context, prepared preparedRequest) (body []byte, resultErr error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, prepared.Endpoint, bytes.NewReader(prepared.Body))
	if err != nil {
		return nil, fmt.Errorf("net/http.NewRequestWithContext: %w", err)
	}

	if c.config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close response: %w", closeErr))
		}
	}()

	const maxResponseBytes = 8 << 20

	body, err = io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeds 8 MiB")
	}

	if err := validateAPIResponse(response.StatusCode, body); err != nil {
		return nil, err
	}

	return body, nil
}

func validateAPIResponse(statusCode int, body []byte) error {
	if statusCode < 200 || statusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", statusCode, strings.TrimSpace(string(body)))
	}

	if !json.Valid(body) {
		return fmt.Errorf("API returned invalid JSON: %s", body)
	}

	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(envelope.Error) != 0 && string(envelope.Error) != "null" {
		return fmt.Errorf("API error: %s", envelope.Error)
	}

	return nil
}
