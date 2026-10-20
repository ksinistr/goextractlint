package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"goextractlint/internal/source"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type PreparedRequest struct {
	Endpoint string
	Model    string
	Body     []byte
}

type Analyzer interface {
	Analyze(context.Context, PreparedRequest) (Decision, error)
}

type Client struct {
	http   HTTPClient
	apiKey string
}

func NewClient(client HTTPClient, apiKey string) *Client {
	return &Client{http: client, apiKey: apiKey}
}

func Prepare(endpoint, model string, function source.Function) (PreparedRequest, error) {
	payload := map[string]any{
		"model": model,
		"state": map[string]any{"principle": Principle(), "function": function},
		"questions": map[string]any{
			"verdict": map[string]any{
				"type": "choice",
				"instructions": "Using the principle in state, decide whether this function contains a fragment worth " +
					"extracting to reveal intention.",
				"criteria": map[string]string{
					"extract": "A specific fragment hides an intention behind implementation details; an " +
						"intention-revealing named function would improve understanding of the enclosing " +
						"function.",
					"keep": "The function is understandable at its current level; extraction would merely relocate " +
						"details or add indirection without clarifying intention.",
					"insufficient_context": "Missing domain or type information prevents a reliable judgment about whether " +
						"extraction would clarify intention.",
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return PreparedRequest{}, fmt.Errorf("encode request: %w", err)
	}

	return PreparedRequest{strings.TrimRight(endpoint, "/") + "/api/alpha/decisions", model, data}, nil
}

func (c *Client) Analyze(ctx context.Context, prepared PreparedRequest) (Decision, error) {
	body, err := c.call(ctx, prepared)
	if err != nil {
		return Decision{}, err
	}

	return Decode(body)
}

func (c *Client) call(ctx context.Context, prepared PreparedRequest) (body []byte, resultErr error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, prepared.Endpoint, bytes.NewReader(prepared.Body))
	if err != nil {
		return nil, fmt.Errorf("net/http.NewRequestWithContext: %w", err)
	}

	request.Header.Set("Authorization", "Bearer "+c.apiKey)
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
