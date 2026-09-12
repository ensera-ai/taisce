// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation

import (
	"context"
	"errors"

	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

var ErrGenerationInference = errors.New("generation inference failed before cutover")

type Rebuilder struct {
	facts     *pg.FactStore
	extractor *extract.Extractor
}

func NewRebuilder(facts *pg.FactStore, extractor *extract.Extractor) *Rebuilder {
	return &Rebuilder{facts: facts, extractor: extractor}
}

// Rebuild prepares all output while the old facts remain available. The one-source transaction
// checks that nothing changed meanwhile, and commits a receipt keyed by the operator's request.
// An interrupted preparation can cost another model attempt; an interrupted published operation
// reuses its recorded outcome. Provider bodies never enter the operator's error output.
func (r *Rebuilder) Rebuild(ctx context.Context, schema pg.Schema, scope, source, key, principal string) (pg.GenerationResult, error) {
	return r.rebuild(ctx, schema, scope, source, key, principal, nil)
}

func (r *Rebuilder) rebuild(ctx context.Context, schema pg.Schema, scope, source, key, principal string, guard *pg.GenerationJobGuard) (pg.GenerationResult, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultPolicy().TurnBudget)
	defer cancel()
	version := r.extractor.PipelineVersion()
	prior, found, err := r.facts.FindGeneration(ctx, schema, scope, source, key, version)
	if err != nil {
		return pg.GenerationResult{}, err
	}
	if found {
		return prior, r.facts.FinishGeneration(ctx, schema, scope, source)
	}
	snapshot, err := r.facts.ReadGenerationSource(ctx, schema, scope, source)
	if err != nil {
		return pg.GenerationResult{}, err
	}
	plan := pg.GenerationPlan{Job: guard}
	for _, message := range snapshot.Messages {
		message.OccurredAt = snapshot.MessageTime(message)
		result, err := r.extractor.Extract(ctx, message)
		if err != nil {
			return pg.GenerationResult{}, ErrGenerationInference
		}
		plan.Claims = append(plan.Claims, result.Claims...)
		plan.Rejected = append(plan.Rejected, result.Rejected...)
		if len(plan.Claims) > pg.MaxGenerationClaims || len(plan.Rejected) > pg.MaxGenerationClaims {
			return pg.GenerationResult{}, pg.ErrGenerationLimit
		}
	}
	out, err := r.facts.ApplyGeneration(ctx, schema, snapshot, key, version, principal, plan)
	if err != nil {
		return pg.GenerationResult{}, err
	}
	return out, r.facts.FinishGeneration(ctx, schema, scope, source)
}
