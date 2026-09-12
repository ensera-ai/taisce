// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// withStaleness returns the rebuild's own result with what it left behind added beside it.
//
// # Why the result's own fields stay where they are
//
// An operator scripts against this output. Nesting the result under a key to make room for a new one
// would move every field somebody already reads, for the convenience of the field being added — the
// wrong way round. So the result is marshalled as it always was and two keys are added next to its
// own: `stale`, present only when something is, and `what_to_do`, which says the next step rather
// than restating the problem.
//
// # Why staleness is read and never stored
//
// These are consequences of the state the database is in now. The subject pass writes reports back
// continuously, so a number recorded at the end of a rebuild would be wrong by the time anybody
// looked at it.
//
// A failure to read it is not a failure of the rebuild, which has already committed: the operator is
// told what happened and simply is not told what is stale. Turning a successful rebuild into an
// error because a count could not be taken would be the reporting breaking the thing it reports on.
func withStaleness(ctx context.Context, pool *pgxpool.Pool, schema pg.Schema, scope string, result any) any {
	encoded, err := json.Marshal(result)
	if err != nil {
		return result
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil || out == nil {
		return result
	}
	stale, err := pg.NewRebuildJobStore(pool).Staleness(ctx, schema, scope)
	if err != nil || !stale.Anything() {
		return out
	}
	out["stale"] = stale

	var advice []string
	if stale.Entities > 0 {
		advice = append(advice,
			"entities the new interpretation does not refer to are still here, and so are their alias "+
				"receipts; recall can still anchor on a name this extractor never produced")
	}
	if stale.Communities > 0 {
		advice = append(advice,
			"reports were removed when their facts changed and the worker's subject pass writes them "+
				"back a few per tick; until it does, a recall that would return a theme returns none")
	}
	// In a fixed order, because advice that arrives in a different order each time reads as a
	// different answer to somebody comparing two runs.
	for _, surface := range []struct {
		name     string
		coverage pg.EmbeddingCoverage
	}{
		{"embeddings", stale.MessageEmbeddings},
		{"entity-embeddings", stale.EntityEmbeddings},
		{"report-embeddings", stale.ReportEmbeddings},
	} {
		switch {
		case surface.coverage.Behind > 0:
			advice = append(advice,
				"the active "+surface.name+" generation covers content "+plural(surface.coverage.Behind)+
					" behind the log; start, build and activate a new one with `taisce "+surface.name+"`")
		case surface.coverage.Stale:
			advice = append(advice,
				"the active "+surface.name+" generation was built over a set of things that has since "+
					"changed; start, build and activate a new one with `taisce "+surface.name+"`")
		}
	}
	out["what_to_do"] = advice
	return out
}

// plural says how far behind in words rather than in a template with a number in it, because an
// operator reads this at two in the morning.
func plural(n int64) string {
	if n == 1 {
		return "one turn"
	}
	return strconv.FormatInt(n, 10) + " turns"
}
