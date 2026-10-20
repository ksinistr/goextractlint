package cache

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"

	"goextractlint/internal/domain"
	"goextractlint/internal/system1"
)

type CachedAnalyzer struct {
	analyzer    Analyzer
	store       DecisionStore
	refresh     bool
	diagnostics io.Writer
}

func NewCachedAnalyzer(analyzer Analyzer, store DecisionStore, refresh bool, diagnostics io.Writer) *CachedAnalyzer {
	return &CachedAnalyzer{analyzer, store, refresh, diagnostics}
}

func (c *CachedAnalyzer) diagnostic(err error) {
	if err != nil && c.diagnostics != nil {
		fmt.Fprintf(c.diagnostics, "cache: %v\n", err)
	}
}

func (c *CachedAnalyzer) Analyze(ctx context.Context, request system1.PreparedRequest) (domain.Decision, error) {
	if err := ctx.Err(); err != nil {
		return domain.Decision{}, fmt.Errorf("context: %w", err)
	}

	key := RequestKey(request.Endpoint, request.Model, request.Body)
	if !c.refresh {
		decision, hit, err := c.store.Get(key)
		c.diagnostic(err)

		if err == nil && hit && decision.Validate() == nil {
			return decision, nil
		}
	}

	decision, err := c.analyzer.Analyze(ctx, request)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("analyze function: %w", err)
	}

	if err := decision.Validate(); err != nil {
		return domain.Decision{}, fmt.Errorf("validate decision: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return domain.Decision{}, fmt.Errorf("context: %w", err)
	}

	c.diagnostic(c.store.Put(key, decision))

	return decision, nil
}

type DecisionStore interface {
	Get(string) (domain.Decision, bool, error)
	Put(string, domain.Decision) error
}

func RequestKey(endpoint, model string, data []byte) string {
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

type Analyzer interface {
	Analyze(context.Context, system1.PreparedRequest) (domain.Decision, error)
}
