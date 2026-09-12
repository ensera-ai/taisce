// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package inference

import (
	"testing"

	"github.com/ensera-ai/taisce/internal/report"
)

// Source revisions authorize persistence; adding them must not change what leaves for inference.
func TestReportSourceRevisionsNeverEnterModelMaterial(t *testing.T) {
	c := report.Context{
		Facts:    []report.Fact{{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: "Atlas works at Ensera", Quote: "Atlas works at Ensera"}},
		Children: []report.Report{{Title: "work", Summary: "employment"}},
	}
	before := stripFence(renderContext(c))
	c.Facts[0].SourceObservationID = "private-source-identity"
	c.Facts[0].SourceRevision = 9988776655
	c.Children[0].SourceObservationIDs = []string{"private-child-source"}
	c.Children[0].SourceRevisions = map[string]int64{"private-child-source": 8877665544}
	if after := stripFence(renderContext(c)); after != before {
		t.Fatal("internal revision provenance changed the model input")
	}
}
