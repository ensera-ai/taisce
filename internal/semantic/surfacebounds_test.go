// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package semantic_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/semantic"
)

func TestEntityIdentityCannotBypassTheSemanticSurfaceBudget(t *testing.T) {
	if surface, err := semantic.Surface(strings.Repeat("x", semantic.SurfaceBudget+1), nil, nil); !errors.Is(err, semantic.ErrSurfaceLimit) || surface != "" {
		t.Fatalf("oversized identity: %d bytes %v", len(surface), err)
	}
	surface, err := semantic.Surface("name", []string{strings.Repeat("alias", semantic.SurfaceBudget), "short alias"}, nil)
	if err != nil || len(surface) > semantic.SurfaceBudget || surface != "name | short alias" {
		t.Fatalf("identity bypass: %q %v", surface, err)
	}
}

func TestSemanticBudgetCountsUnicodeBytesAndWholeOptionalItems(t *testing.T) {
	exact := strings.Repeat("é", 1000)
	if surface, err := semantic.Surface(exact, []string{"alias"}, []string{"evidence"}); err != nil || surface != exact {
		t.Fatalf("exact identity: %d %v", len(surface), err)
	}
	if _, err := semantic.Surface(exact+"x", nil, nil); !errors.Is(err, semantic.ErrSurfaceLimit) {
		t.Fatalf("identity overflow %v", err)
	}
	alias := strings.Repeat("é", 998) // 1-byte identity + 3-byte separator + 1996-byte alias = 2000.
	if surface, err := semantic.Surface("x", []string{alias}, nil); err != nil || len(surface) != semantic.SurfaceBudget || !utf8.ValidString(surface) {
		t.Fatalf("exact alias: %d %v", len(surface), err)
	}
	quote := strings.Repeat("é", 999)
	if surface, err := semantic.Surface("x", nil, []string{quote}); err != nil || len(surface) != semantic.SurfaceBudget || !strings.HasSuffix(surface, quote) {
		t.Fatalf("exact quote: %d %v", len(surface), err)
	}
	oversized := "a" + strings.Repeat("x", semantic.SurfaceBudget)
	if surface, err := semantic.Surface("name", nil, []string{oversized, "z short whole quote"}); err != nil || surface != "name\nz short whole quote" {
		t.Fatalf("long quote excluded later evidence: %q %v", surface, err)
	}
}

func TestSemanticCollectionsRefuseMalformedOrUnboundedWork(t *testing.T) {
	for _, tc := range []struct {
		canonical       string
		aliases, quotes []string
		want            error
	}{
		{"", nil, nil, semantic.ErrInvalidSurface}, {" \t ", nil, nil, semantic.ErrInvalidSurface}, {"\xff", nil, nil, semantic.ErrInvalidSurface}, {"name\x00", nil, nil, semantic.ErrInvalidSurface},
		{"name", []string{"\xff"}, nil, semantic.ErrInvalidSurface}, {"name", nil, []string{"quote\x00"}, semantic.ErrInvalidSurface},
		{"name", make([]string, semantic.MaxSurfaceAliases+1), nil, semantic.ErrSurfaceLimit}, {"name", nil, make([]string, semantic.MaxSurfaceQuotes+1), semantic.ErrSurfaceLimit},
		{"x", nil, []string{strings.Repeat("a", semantic.MaxSurfaceInputBytes)}, semantic.ErrSurfaceLimit},
	} {
		if surface, err := semantic.Surface(tc.canonical, tc.aliases, tc.quotes); !errors.Is(err, tc.want) || surface != "" {
			t.Fatalf("invalid input returned %d bytes: %v", len(surface), err)
		}
	}
	if _, err := semantic.Surface("x", nil, []string{strings.Repeat("a", semantic.MaxSurfaceInputBytes-1)}); err != nil {
		t.Fatalf("exact collection byte limit: %v", err)
	}
	if surface, err := semantic.Surface(" x ", make([]string, semantic.MaxSurfaceAliases), make([]string, semantic.MaxSurfaceQuotes)); err != nil || surface != "x" {
		t.Fatalf("exact collection counts: %q %v", surface, err)
	}
}
