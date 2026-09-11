//go:build inference

// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// The compaction prompt's live measurement (rule 14): a segment says what the turns said, keeps
// the facts a later question would need, and adds nothing. A summary that drops the answer that
// changed, or invents a detail, stands in for the turns it covers and loses them.
package inference_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/compaction"
	"github.com/ensera-ai/taisce/internal/infra/inference"
)

func TestALiveSegmentKeepsWhatChangedAndInventsNothing(t *testing.T) {
	config, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("inference is not configured, and this target exists to exercise it: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	summariser := inference.NewSummariser(config)

	// Level 1: eight turns in which an answer changed and a tool ran.
	turns := []compaction.TurnText{
		turn(1, "I've just moved to Dublin for the new job, so my address is in Dublin now.", "Noted: you live in Dublin."),
		turn(2, "My manager is Aoife and the team is called Platform.", "Aoife, on Platform. Noted."),
		turn(3, "Book the all-hands room for Friday.", "Room A is booked for Friday."),
		turn(4, "We use Postgres for everything, no other database.", "One database, Postgres. Noted."),
		turn(5, "Quick update: we relocated the family to Cork last week, so I live in Cork now.", "Noted: you live in Cork now."),
		turn(6, "The migration to the new cluster finishes in October.", "October for the cluster migration. Noted."),
		turn(7, "Remind me that Aoife prefers written notes over calls.", "Aoife prefers written notes. Noted."),
		turn(8, "And I cycle to the Cork office most days.", "Cycling to the Cork office. Noted."),
	}
	segment, err := summariser.Write(ctx, compaction.Material{Level: 1, From: 1, To: 8, Turns: turns})
	if err != nil {
		t.Fatalf("write level 1: %v", err)
	}
	t.Logf("level 1: %s", segment.Text)
	lower := strings.ToLower(segment.Text)
	for _, kept := range []string{"cork", "aoife", "postgres", "october"} {
		if !strings.Contains(lower, kept) {
			t.Errorf("the segment lost %q, which a later question needs: %q", kept, segment.Text)
		}
	}
	// The move is the fact that changed: a segment that says Dublin without Cork, or that
	// contradicts the later turn, has kept the stale answer.
	if strings.Contains(lower, "dublin") && !strings.Contains(lower, "cork") {
		t.Errorf("the segment kept the earlier address and lost the later one: %q", segment.Text)
	}
	for _, invented := range []string{"london", "engineer", "ceo", "google", "microsoft", "2019", "wife", "husband"} {
		if strings.Contains(lower, invented) {
			t.Errorf("the segment contains %q, which is in none of the turns", invented)
		}
	}
	if n := len([]rune(segment.Text)); n > 2000 || n < 80 {
		t.Errorf("a level-1 segment over eight turns is %d characters; a segment is shorter than its turns and longer than a line", n)
	}

	// Level 2: eight level-1 segments condensed again; the fact that changed still survives.
	units := []compaction.UnitText{{From: 1, To: 8, Covered: 8, Summary: segment.Text}}
	for i := 2; i <= 8; i++ {
		units = append(units, compaction.UnitText{From: int64(i * 8), To: int64(i*8 + 7), Covered: 8,
			Summary: fmt.Sprintf("Turns %d to %d: the user reviewed the platform team's sprint %d, with no change of address, employer or manager.", i*8, i*8+7, i)})
	}
	higher, err := summariser.Write(ctx, compaction.Material{Level: 2, From: 1, To: 71, Units: units})
	if err != nil {
		t.Fatalf("write level 2: %v", err)
	}
	t.Logf("level 2: %s", higher.Text)
	if lower := strings.ToLower(higher.Text); !strings.Contains(lower, "cork") || !strings.Contains(lower, "aoife") {
		t.Errorf("the level-2 segment lost the address or the manager: %q", higher.Text)
	}
}

func turn(offset int64, user, assistant string) compaction.TurnText {
	return compaction.TurnText{Offset: offset, Messages: []compaction.MessageText{{Role: "user", Content: user}, {Role: "assistant", Content: assistant}}}
}
