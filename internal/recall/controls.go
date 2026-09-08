// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package recall

import (
	"context"
	"errors"
	"slices"

	"github.com/ensera-ai/taisce/internal/domain"
)

var ErrInvalidControls = errors.New("invalid recall controls")

// Controls narrows the configured budget and explicitly selects whose assertions are considered.
// Source selection changes the trust choice, never the authorized projects or subject filter.
type Controls struct {
	MaxCharacters *int     `json:"max_characters,omitempty"`
	SourceRoles   []string `json:"source_roles,omitempty"`
	Hops          int      `json:"hops,omitempty"`
}

type EffectiveControls struct {
	MaxCharacters int      `json:"max_characters"`
	MaxRows       int      `json:"max_rows"`
	SourceRoles   []string `json:"source_roles"`
	Hops          int      `json:"hops"`
}

// RecallWithControls copies the recaller's small configuration value so simultaneous callers can
// choose different context windows without mutating shared state or bypassing the server ceiling.
func (r *Recaller) RecallWithControls(ctx context.Context, scopes []string, question string, at domain.AsOf, subject string, controls Controls) (domain.Bundle, EffectiveControls, error) {
	local := *r
	effective := EffectiveControls{MaxCharacters: r.budget.Characters, MaxRows: r.budget.MaxRows, Hops: clampHops(controls.Hops)}
	if controls.MaxCharacters != nil {
		if *controls.MaxCharacters < 1 || *controls.MaxCharacters > r.budget.Characters {
			return domain.Bundle{}, EffectiveControls{}, ErrInvalidControls
		}
		effective.MaxCharacters = *controls.MaxCharacters
	}
	roles := controls.SourceRoles
	if roles == nil {
		roles = []string{string(domain.RoleUser)}
	}
	if len(roles) < 1 || len(roles) > 4 {
		return domain.Bundle{}, EffectiveControls{}, ErrInvalidControls
	}
	seen := map[string]bool{}
	for _, role := range roles {
		if !domain.Role(role).Valid() || seen[role] {
			return domain.Bundle{}, EffectiveControls{}, ErrInvalidControls
		}
		seen[role] = true
	}
	effective.SourceRoles = slices.Clone(roles)
	local.budget.Characters = effective.MaxCharacters
	bundle, err := local.RecallForSubjectAsOf(ctx, scopes, question, effective.SourceRoles, at, effective.Hops, subject)
	return bundle, effective, err
}

func clampHops(hops int) int {
	if hops < DirectHop {
		return DirectHop
	}
	if hops > MaxHops {
		return MaxHops
	}
	return hops
}
