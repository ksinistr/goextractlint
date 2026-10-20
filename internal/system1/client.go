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
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type PreparedRequest struct {
	Endpoint string
	Model    string
	Body     []byte
}

type Client struct {
	http   HTTPClient
	apiKey string
}

func NewClient(client HTTPClient, apiKey string) *Client {
	return &Client{http: client, apiKey: apiKey}
}

func (c *Client) Call(ctx context.Context, prepared PreparedRequest) (body []byte, resultErr error) {
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
