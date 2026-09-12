// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// A project being reinterpreted answers from two extractors at once, and freshness is where a caller
// finds that out. Before and after the job it says nothing, because a field that is always present
// is one a caller compares against zero — and a finished rebuild would then read as an active one
// that has done nothing.
func TestFreshnessReportsAnActiveRebuildAndNothingBeforeOrAfterIt(t *testing.T) {
	ctx := context.Background()
	f := generationSource(t, "rebuild_visibility")
	observations := pg.NewObservationStore(f.pool)

	before, err := observations.Freshness(ctx, f.schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if before.Rebuilding != nil {
		t.Fatalf("a project nobody has rebuilt reports one: %+v", before.Rebuilding)
	}

	jobs := pg.NewRebuildJobStore(f.pool)
	key, actor := uuid.NewString(), uuid.NewString()
	session, err := jobs.Acquire(ctx, f.schema, "p1", key, f.version, actor)
	if err != nil {
		t.Fatal(err)
	}
	during, err := observations.Freshness(ctx, f.schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if during.Rebuilding == nil {
		t.Fatal("a project mid-rebuild says nothing about it")
	}
	status, err := jobs.Status(ctx, f.schema, "p1", key)
	if err != nil {
		t.Fatal(err)
	}
	// The numbers are the operator's job, read through the project's own route, so the two cannot
	// disagree about how far the reinterpretation has reached.
	if during.Rebuilding.ReinterpretingThrough != status.Through ||
		during.Rebuilding.ReinterpretedThrough != status.After ||
		during.Rebuilding.Acknowledged != status.Rebuilt ||
		during.Rebuilding.Skipped != status.Skipped {
		t.Fatalf("freshness and status disagree: %+v against %+v", during.Rebuilding, status)
	}

	// Progress moves it. A figure that never changed would be indistinguishable from a stuck job.
	work, found, err := session.Next(ctx)
	if err != nil || !found {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := f.facts.ApplyGeneration(ctx, f.schema, f.snapshot, work.Key, f.version, actor,
		pg.GenerationPlan{Claims: []domain.Claim{f.claim}, Job: session.Guard(work.Offset)}); err != nil {
		t.Fatal(err)
	}
	if err := session.Complete(ctx, work.Offset, true); err != nil {
		t.Fatal(err)
	}
	advanced, err := observations.Freshness(ctx, f.schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if advanced.Rebuilding == nil || advanced.Rebuilding.Acknowledged != during.Rebuilding.Acknowledged+1 ||
		advanced.Rebuilding.ReinterpretedThrough <= during.Rebuilding.ReinterpretedThrough {
		t.Fatalf("progress did not move: %+v then %+v", during.Rebuilding, advanced.Rebuilding)
	}
	session.Close()

	// Cancelled is not active. A job that stopped is not a reason to distrust the next bundle, and
	// reporting one would make this field mean "a rebuild happened here once".
	if _, err := jobs.Cancel(ctx, f.schema, "p1", key, actor); err != nil {
		t.Fatal(err)
	}
	after, err := observations.Freshness(ctx, f.schema, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Rebuilding != nil {
		t.Fatalf("a cancelled rebuild is still reported: %+v", after.Rebuilding)
	}
	// And the rest of freshness is untouched by any of it.
	if after.Stored != before.Stored || after.HasStored != before.HasStored || after.Parked != before.Parked {
		t.Fatalf("reporting a rebuild changed the rest of freshness: %+v then %+v", before, after)
	}
}
