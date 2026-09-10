// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package formation

import (
	"context"
	"errors"
	"fmt"

	"github.com/ensera-ai/taisce/internal/compaction"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// SegmentStore is what the compaction pass needs from the database.
type SegmentStore interface {
	Subjects(ctx context.Context, scope string, limit int) ([]string, error)
	Formed(ctx context.Context, scope, subject string) ([]compaction.Turn, error)
	Segments(ctx context.Context, scope, subject string) ([]compaction.Segment, error)
	Material(ctx context.Context, scope, subject string, plan compaction.Plan) (compaction.Material, error)
	Write(ctx context.Context, scope, subject string, plan compaction.Plan, summary compaction.Summary) (compaction.Segment, error)
}

// Summariser writes one segment's prose from its material. A model, behind a port.
type Summariser interface {
	Write(ctx context.Context, m compaction.Material) (compaction.Summary, error)
}

// Compaction rolls up every subject's history in a scope, one segment at a time.
//
// # Why this is a pass and not a read
//
// Writing a segment is a model call. A context assembly that triggered one would put inference on
// the read path and make one caller pay for a stretch of history nobody had asked to condense. So
// the pass writes segments off the read path, from formed turns, and a context is assembled from
// whatever segments exist when it is asked for: a history the pass has not reached yet is handed
// over verbatim and cut, never summarised on demand.
//
// # Why the pass is bounded by segments written
//
// A pass bounded by segments written is a pass whose cost an operator can predict: at most `limit`
// model calls, each over at most MaxMaterialCharacters. A subject with a long unrolled history
// catches up over several passes rather than in one, which is also what keeps one subject's
// backlog from holding every other subject's compaction for long.
type Compaction struct {
	store      SegmentStore
	summariser Summariser
}

// NewCompaction builds the pass. A nil summariser writes nothing and reports so.
func NewCompaction(store SegmentStore, summariser Summariser) *Compaction {
	return &Compaction{store: store, summariser: summariser}
}

// CompactionPass is what one call did.
type CompactionPass struct {
	Subjects int
	Written  int
	Cut      int
	Errored  int
}

// Run writes at most limit segments across the subjects of one scope, oldest gaps first.
func (c *Compaction) Run(ctx context.Context, scope string, limit int) (CompactionPass, error) {
	var pass CompactionPass
	if c.summariser == nil {
		return pass, nil
	}
	subjects, err := c.store.Subjects(ctx, scope, limit)
	if err != nil {
		return pass, err
	}
	pass.Subjects = len(subjects)
	for _, subject := range subjects {
		// A subject with a long unrolled history takes as many of this pass's writes as it needs,
		// up to the limit; the next subject gets what is left, and the next pass starts over.
		for pass.Written < limit && ctx.Err() == nil {
			written, cut, err := c.one(ctx, scope, subject)
			pass.Written += written
			pass.Cut += cut
			if err != nil {
				if ctx.Err() != nil {
					return pass, ctx.Err()
				}
				pass.Errored++
				break
			}
			if written == 0 {
				break
			}
		}
	}
	return pass, nil
}

// one writes the next segment a subject needs, or nothing when its history is rolled up.
func (c *Compaction) one(ctx context.Context, scope, subject string) (int, int, error) {
	turns, err := c.store.Formed(ctx, scope, subject)
	if err != nil {
		return 0, 0, err
	}
	segments, err := c.store.Segments(ctx, scope, subject)
	if err != nil {
		return 0, 0, err
	}
	plan, ok, err := compaction.Next(turns, segments)
	if err != nil || !ok {
		return 0, 0, err
	}
	material, err := c.store.Material(ctx, scope, subject, plan)
	if err != nil {
		return 0, 0, err
	}
	material, cut := bound(material)
	summary, err := c.summariser.Write(ctx, material)
	if err != nil {
		return 0, cut, fmt.Errorf("summarise %s level %d %d-%d: %w", subject, plan.Level, plan.From, plan.To, err)
	}
	if _, err := c.store.Write(ctx, scope, subject, plan, summary); err != nil {
		if errors.Is(err, pg.ErrSegmentConflict) {
			// Another replica wrote it, or the history moved: not this pass's failure. The next
			// pass plans again over what is there.
			return 0, cut, nil
		}
		return 0, cut, err
	}
	return 1, cut, nil
}

// bound cuts material over the ceiling from the oldest turn, and says how many turns went. The
// segment still covers the whole range, because its registrations must; what it was written from
// is smaller, and that is reported rather than hidden.
func bound(m compaction.Material) (compaction.Material, int) {
	cut := 0
	for m.Size() > compaction.MaxMaterialCharacters && len(m.Turns) > 1 {
		m.Turns = m.Turns[1:]
		cut++
	}
	return m, cut
}
