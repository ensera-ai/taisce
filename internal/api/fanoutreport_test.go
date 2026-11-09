// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/recall"
)

// cappedWalk is a traversal that left facts behind on a busy entity, and says so.
type cappedWalk struct{ capped bool }

func (cappedWalk) AnchorsForSubject(_ context.Context, _, terms []string, _ string) ([]domain.Anchor, error) {
	return []domain.Anchor{{Entity: domain.Entity{ID: "e1", Scope: "p1", CanonicalName: "Atlas"}, Matched: terms[0]}}, nil
}

func (w cappedWalk) FactsAboutForSubject(context.Context, []string, []string, int, []string,
	domain.AsOf, int, string) ([]domain.CitedFact, bool, error) {
	return []domain.CitedFact{{ID: "f1", Subject: "Atlas", Predicate: "works_at", Object: "Ensera",
		Statement: "Atlas works at Ensera", SourceRole: "user", AnchoredOn: "e1",
		Evidence: domain.Evidence{Quote: "works at Ensera"}}}, w.capped, nil
}

// The bundle a caller receives over the route carries both halves of the answer: that it was cut,
// and that the cut was the traversal's rather than their own budget's.
//
// Over the route rather than in the recaller alone, because the field name is the part a caller
// writes code against, and a signal computed and not rendered is a signal nobody receives.
func TestTheRouteTellsACallerWhenTheNeighbourhoodWasCapped(t *testing.T) {
	for _, tc := range []struct {
		name   string
		capped bool
	}{
		{name: "capped", capped: true},
		{name: "complete", capped: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "fanoutreport")
			h.server.Close()
			h.server = httptest.NewServer(api.NewServer(
				credential.NewStore(h.pool, string(migrate.ControlSchema)), api.Stores{
					Notifications: h.notifyStore, Notifier: h.notifier,
					Audit: pg.NewAuditStore(h.pool, h.schema), Exporter: pg.NewExporter(h.pool, h.schema),
					Projects: pg.NewProjectStore(h.pool, h.schema), Observations: pg.NewObservationStore(h.pool),
					Eraser: pg.NewEraser(h.pool),
					Recaller: recall.NewWithBudget(cappedWalk{capped: tc.capped},
						recall.Budget{Characters: 100000, MaxRows: 50}),
					Citations: pg.NewCitationStore(h.pool, h.schema), Records: pg.NewRecordStore(h.pool, h.schema),
					Subjects: pg.NewSubjectStore(h.pool, h.schema), Contexts: pg.NewSegmentStore(h.pool, h.schema),
				}, h.schema, nil).Handler())
			t.Cleanup(h.server.Close)

			var out struct {
				Truncated bool `json:"truncated"`
				Reach     struct {
					NeighbourhoodCapped bool `json:"neighbourhood_capped"`
				} `json:"reach"`
			}
			h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Atlas"}, 200, &out)

			if out.Truncated != tc.capped {
				t.Errorf("truncated=%v for a capped=%v walk", out.Truncated, tc.capped)
			}
			if out.Reach.NeighbourhoodCapped != tc.capped {
				t.Errorf("reach.neighbourhood_capped=%v for a capped=%v walk",
					out.Reach.NeighbourhoodCapped, tc.capped)
			}
		})
	}
}
