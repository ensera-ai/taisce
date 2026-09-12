// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package recall

import (
	"context"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/domain"
)

// Semantic is what recall may consult after exact anchoring. Every method is bounded by the
// caller, scoped to one project, and free to refuse: a surface that refuses is reported as degraded
// and the rest of the bundle still answers. There is deliberately no generation here; the only model
// work any of these may do is embed the question, which D106 already opted the read path into.
type Semantic interface {
	// EntityAnchors proposes entities the question means, by similarity over stored names and
	// evidence. A proposal becomes an anchor to walk from and never a merged identity.
	EntityAnchors(ctx context.Context, scope, question string, limit int) ([]domain.Anchor, error)
	// Reports returns the community reports nearest the question, each followed by up to
	// `children` of the best match's children from the stored hierarchy, with their sources.
	Reports(ctx context.Context, scope, question string, limit, children int) ([]domain.ReportHit, error)
	// Passages returns source messages nearest the question, restricted to the given roles.
	// Passages is narrowed to one person when subject is not empty: a passage is somebody's words,
	// and a recall narrowed to one person must not quote anybody else.
	Passages(ctx context.Context, scope, question string, limit int, roles []string, subject string) ([]domain.Passage, error)
}

// The surfaces a caller may select, and the bounds each runs under. The bounds are per call and
// small on purpose: a surface exists to start a walk or to fill what the budget has left, not to
// replace the anchor-bounded read G1 is about.
const (
	SurfaceFacts    = "facts"
	SurfaceReports  = "reports"
	SurfacePassages = "passages"

	MaxSemanticAnchors = 4
	MaxReportHits      = 3
	MaxReportChildren  = 5
	MaxPassageHits     = 8
)

// AllSurfaces is what a caller gets by naming none.
var AllSurfaces = []string{SurfaceFacts, SurfaceReports, SurfacePassages}

// WithSemantic returns a recaller that composes the given surfaces. Nil keeps the exact path alone,
// which is what a deployment without an embedding revision runs.
func (r *Recaller) WithSemantic(s Semantic) *Recaller {
	c := *r
	c.semantic = s
	return &c
}

func reportSize(h domain.ReportHit) int {
	return utf8.RuneCountInString(h.Title) + utf8.RuneCountInString(h.Summary)
}

func passageSize(p domain.Passage) int {
	return utf8.RuneCountInString(p.Quote) + utf8.RuneCountInString(p.Role)
}

func validSurface(s string) bool {
	return s == SurfaceFacts || s == SurfaceReports || s == SurfacePassages
}
