// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation

import (
	"context"
	"errors"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

const MaxRebuildPage = 100

type ProjectRebuilder struct {
	rebuilder *Rebuilder
	jobs      *pg.RebuildJobStore
}

func NewProjectRebuilder(rebuilder *Rebuilder, jobs *pg.RebuildJobStore) *ProjectRebuilder {
	return &ProjectRebuilder{rebuilder: rebuilder, jobs: jobs}
}

// RunPage is restartable from its durable pending offset. A publication whose progress write was
// interrupted replays its source operation key, so retries do not spend another inference call.
func (r *ProjectRebuilder) RunPage(ctx context.Context, schema pg.Schema, scope, key, actor string, limit int) (pg.RebuildJob, error) {
	if limit < 1 || limit > MaxRebuildPage {
		return pg.RebuildJob{}, pg.ErrGenerationLimit
	}
	version := r.rebuilder.extractor.PipelineVersion()
	session, err := r.jobs.Acquire(ctx, schema, scope, key, version, actor)
	if err != nil {
		return pg.RebuildJob{}, err
	}
	defer session.Close()
	for i := 0; i < limit; i++ {
		work, found, err := session.Next(ctx)
		if err != nil {
			return pg.RebuildJob{}, err
		}
		if !found {
			break
		}
		rebuilt := false
		if work.SourceID != "" {
			if work.CancelRequested {
				_, rebuilt, err = r.rebuilder.facts.FindGeneration(ctx, schema, scope, work.SourceID, work.Key, version)
				if err == nil && rebuilt {
					err = r.rebuilder.facts.FinishGeneration(ctx, schema, scope, work.SourceID)
				}
			} else {
				_, err = r.rebuilder.rebuild(ctx, schema, scope, work.SourceID, work.Key, actor, session.Guard(work.Offset))
				rebuilt = err == nil
			}
			if errors.Is(err, pg.ErrGenerationSource) {
				err = nil
				rebuilt = false
			}
			if err != nil {
				return pg.RebuildJob{}, err
			}
		}
		if err := session.Complete(ctx, work.Offset, rebuilt); err != nil {
			return pg.RebuildJob{}, err
		}
	}
	return r.jobs.Status(ctx, schema, scope, key)
}
