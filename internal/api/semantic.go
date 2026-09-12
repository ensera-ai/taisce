// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api

import (
	"context"
	"fmt"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/entitycandidate"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/passage"
	"github.com/ensera-ai/taisce/internal/recall"
	"github.com/ensera-ai/taisce/internal/reportcandidate"
)

// semanticSurfaces composes the three retrievers the API already serves separately into what recall
// consults after exact anchoring. It lives here rather than in the recall package because it
// is transport-side glue over concrete stores; the correctness argument for each surface stays in
// the retriever that owns it, and recall sees only the bounded, scoped interface.
type semanticSurfaces struct {
	entities    *entitycandidate.Retriever
	reports     *reportcandidate.Retriever
	passages    *passage.Retriever
	communities *pg.CommunityStore
}

// NewSemanticSurfaces returns nil when no retriever is configured, so a deployment without an
// embedding revision keeps the exact path alone and says so in `degraded`.
func NewSemanticSurfaces(entities *entitycandidate.Retriever, reports *reportcandidate.Retriever,
	passages *passage.Retriever, communities *pg.CommunityStore) recall.Semantic {
	if entities == nil && reports == nil && passages == nil {
		return nil
	}
	return &semanticSurfaces{entities: entities, reports: reports, passages: passages, communities: communities}
}

var errSurfaceUnavailable = fmt.Errorf("surface is not configured")

func (s *semanticSurfaces) EntityAnchors(ctx context.Context, scope, question string, limit int) ([]domain.Anchor, error) {
	if s.entities == nil {
		return nil, errSurfaceUnavailable
	}
	page, err := s.entities.Search(ctx, scope, entitycandidate.Query{Question: question, Limit: limit, Candidates: limit * 4})
	if err != nil {
		return nil, err
	}
	anchors := make([]domain.Anchor, 0, len(page.Candidates))
	for _, c := range page.Candidates {
		anchors = append(anchors, domain.Anchor{
			Entity:  domain.Entity{ID: c.EntityID, Scope: scope, CanonicalName: c.NamePreview},
			Matched: fmt.Sprintf("semantic:%.2f", c.Similarity),
		})
	}
	return anchors, nil
}

func (s *semanticSurfaces) Reports(ctx context.Context, scope, question string, limit, children int) ([]domain.ReportHit, error) {
	if s.reports == nil || s.communities == nil {
		return nil, errSurfaceUnavailable
	}
	page, err := s.reports.Search(ctx, scope, reportcandidate.Query{Question: question, Limit: limit, Candidates: limit * 4})
	if err != nil {
		return nil, err
	}
	var hits []domain.ReportHit
	// A child of the best match can also be a direct match; it is one report and is listed once,
	// with its lineage, because a bundle that repeats a report spends budget on nothing.
	seen := make(map[string]bool, limit+children)
	for i, c := range page.Candidates {
		if seen[c.CommunityID] {
			continue
		}
		full, ok, err := s.communities.Report(ctx, scope, c.CommunityID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		seen[c.CommunityID] = true
		hits = append(hits, domain.ReportHit{
			CommunityID: c.CommunityID, ReportID: c.ReportID, Title: full.Title, Summary: full.Summary,
			Importance: full.Importance, Similarity: c.Similarity, Sources: full.SourceObservationIDs,
		})
		// The best match's children, from the stored hierarchy: a deeper subject under the one the
		// question matched, without a model call. Depth one, bounded, deterministic.
		if i == 0 && children > 0 {
			kids, err := s.communities.Children(ctx, scope, c.CommunityID)
			if err != nil {
				return nil, err
			}
			for _, kid := range kids {
				if children == 0 {
					break
				}
				child, ok, err := s.communities.Report(ctx, scope, kid.ID)
				if err != nil {
					return nil, err
				}
				if !ok || seen[kid.ID] {
					continue
				}
				seen[kid.ID] = true
				hits = append(hits, domain.ReportHit{
					CommunityID: kid.ID, Title: child.Title, Summary: child.Summary, Importance: child.Importance,
					Level: kid.Level, Parent: c.CommunityID, Sources: child.SourceObservationIDs,
				})
				children--
			}
		}
	}
	return hits, nil
}

func (s *semanticSurfaces) Passages(ctx context.Context, scope, question string, limit int, roles []string, subject string) ([]domain.Passage, error) {
	if s.passages == nil {
		return nil, errSurfaceUnavailable
	}
	query := passage.Query{Question: question, Limit: limit, Candidates: limit * 4, Subject: subject}
	if len(roles) == 1 {
		query.Role = roles[0]
	}
	page, err := s.passages.Search(ctx, scope, query)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, role := range roles {
		allowed[role] = true
	}
	var out []domain.Passage
	for _, m := range page.Passages {
		if len(roles) > 1 && !allowed[m.Role] {
			continue
		}
		out = append(out, domain.Passage{
			ChunkID: m.ChunkID, SourceID: m.SourceID, Ordinal: m.Ordinal, Role: m.Role, Quote: m.Preview,
			Similarity: m.Similarity, OccurredAt: m.OccurredAt, Complete: m.PreviewComplete,
		})
	}
	return out, nil
}
