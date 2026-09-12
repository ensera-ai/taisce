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
	// Surfaces selects which composed surfaces may answer: facts, reports, passages. Nil is
	// all three; an empty list, a repeat or an unknown name is refused.
	Surfaces []string `json:"surfaces,omitempty"`
	// Themes forces the report surface even when the question anchored, for a caller who wants
	// the subjects around an entity as well as the facts about it.
	Themes bool `json:"themes,omitempty"`
}

type EffectiveControls struct {
	MaxCharacters int      `json:"max_characters"`
	MaxRows       int      `json:"max_rows"`
	SourceRoles   []string `json:"source_roles"`
	Hops          int      `json:"hops"`
	Surfaces      []string `json:"surfaces"`
	Themes        bool     `json:"themes"`
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
	surfaces := controls.Surfaces
	if surfaces == nil {
		surfaces = AllSurfaces
	}
	if len(surfaces) < 1 || len(surfaces) > len(AllSurfaces) {
		return domain.Bundle{}, EffectiveControls{}, ErrInvalidControls
	}
	selected := map[string]bool{}
	for _, surface := range surfaces {
		if !validSurface(surface) || selected[surface] {
			return domain.Bundle{}, EffectiveControls{}, ErrInvalidControls
		}
		selected[surface] = true
	}
	effective.Surfaces = slices.Clone(surfaces)
	effective.Themes = controls.Themes
	// Only an explicit selection is recorded: on a deployment without semantic surfaces, a caller who
	// named none gets the exact path and nothing about surfaces, and one who named them is told
	// which did not run.
	if controls.Surfaces != nil {
		local.surfaces = selected
	}
	local.themes = controls.Themes
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
