package system1

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

type DecisionStore interface {
	Get(string) (Decision, bool, error)
	Put(string, Decision) error
}

func (c *Client) diagnostic(err error) {
	if err != nil && c.diagnostics != nil {
		fmt.Fprintf(c.diagnostics, "cache: %v\n", err)
	}
}

func (c *Client) evaluateRequest(ctx context.Context, request preparedRequest) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, fmt.Errorf("context: %w", err)
	}

	key := requestKey(request.Endpoint, request.Model, request.Body)
	if decision, hit := c.cachedDecision(key); hit {
		return decision, nil
	}

	body, err := c.call(ctx, request)
	if err != nil {
		return Decision{}, fmt.Errorf("call system1: %w", err)
	}

	decision, err := decodeDecision(body)
	if err != nil {
		return Decision{}, err
	}

	if err := ctx.Err(); err != nil {
		return Decision{}, fmt.Errorf("context: %w", err)
	}

	if c.store != nil {
		c.diagnostic(c.store.Put(key, decision))
	}

	return decision, nil
}

func (c *Client) cachedDecision(key string) (Decision, bool) {
	if c.store == nil || c.config.RefreshCache {
		return Decision{}, false
	}

	decision, hit, err := c.store.Get(key)
	c.diagnostic(err)

	return decision, err == nil && hit && decision.Validate() == nil
}

func requestKey(endpoint, model string, data []byte) string {
	identity, err := json.Marshal(struct {
		Version         int
		Endpoint, Model string
		Request         []byte
	}{1, endpoint, model, data})
	if err != nil {
		panic(fmt.Errorf("encode request identity: %w", err))
	}

	return fmt.Sprintf("%x", sha256.Sum256(identity))
}
