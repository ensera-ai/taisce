// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package formation

import (
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/compaction"
)

// Material over the ceiling loses its oldest turns until it fits, and the count of what went is
// reported; a single turn is never cut away, because a segment written from nothing is not a
// segment.
func TestMaterialOverTheCeilingIsCutFromTheOldestTurnAndTheCutIsCounted(t *testing.T) {
	turn := func(offset int64) compaction.TurnText {
		return compaction.TurnText{Offset: offset, Messages: []compaction.MessageText{{Role: "user", Content: strings.Repeat("x", compaction.MaxMaterialCharacters/2)}}}
	}
	m, cut := bound(compaction.Material{Level: 1, Turns: []compaction.TurnText{turn(1), turn(2), turn(3)}})
	if cut != 2 || len(m.Turns) != 1 || m.Turns[0].Offset != 3 {
		t.Fatalf("expected the two oldest turns cut, got %d cut and %d turns", cut, len(m.Turns))
	}
	if m, cut := bound(compaction.Material{Level: 1, Turns: []compaction.TurnText{turn(1), turn(2)}}); cut != 1 || len(m.Turns) != 1 {
		t.Fatalf("expected one cut, got %d", cut)
	}
	if m, cut := bound(compaction.Material{Level: 1, Turns: []compaction.TurnText{turn(1)}}); cut != 0 || len(m.Turns) != 1 {
		t.Fatalf("a single turn is never cut, got %d", cut)
	}
}
