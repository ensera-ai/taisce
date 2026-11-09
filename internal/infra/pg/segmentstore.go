// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ensera-ai/taisce/internal/compaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProjectionSegment is the projection kind a segment registers as.
const ProjectionSegment = "segment"

// SegmentStore writes the segments that stand in for a subject's history, and reads that history
// back for a caller.
//
// # The two readers, and why they see different turns
//
// The compaction pass is offered only turns that are formed and not parked. It writes a summary
// over them and registers it against each one, so what it summarises must be something the
// deployment has admitted: a turn still inside the formation window may yet park or be erased, and
// a summary standing in for it would have been written over nothing. That gate is the write path's,
// and it belongs there.
//
// A context is the other reader, and it carries the subject's own words under a budget with no
// model call. Nothing about formation bears on whether a turn was said, so a context reads every
// turn stored for the subject and reports, per turn, whether a segment was written from it. The
// pass's gate does not belong here, and applying it to this read loses words in two ways that are
// not alike: a turn inside the formation window is missing for as long as the workers are behind,
// which for an active conversation is the newest turns a caller asked for, and a parked turn is
// missing forever, because it will never be formed and so never summarised. An answer that is
// short says so — as the watermark, or as the cut — and is never silently empty.
type SegmentStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewSegmentStore(pool *pgxpool.Pool, schema Schema) *SegmentStore {
	return &SegmentStore{pool: pool, schema: schema}
}

// Subjects lists the data subjects in a scope with more formed turns than the verbatim tail, which
// is every subject the pass may have something to write for.
func (s *SegmentStore) Subjects(ctx context.Context, scope string, limit int) ([]string, error) {
	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT data_subject_id FROM {schema}.observation
		 WHERE scope=$1 AND data_subject_id IS NOT NULL AND kind=$3 AND formed_at IS NOT NULL AND parked_at IS NULL
		 GROUP BY data_subject_id HAVING count(*) > $2 ORDER BY data_subject_id LIMIT $4`),
		scope, compaction.Verbatim, KindTurn, limit)
	if err != nil {
		return nil, fmt.Errorf("list subjects: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var subject string
		if err := rows.Scan(&subject); err != nil {
			return nil, err
		}
		out = append(out, subject)
	}
	return out, rows.Err()
}

// Formed reads the turns of one subject the compaction pass may summarise — formed, not parked — in
// log order, each with its size in characters. It is the pass's reader; a context uses History.
//
// The size is read off the observation, where it was counted when the turn was written. Summing it
// from the messages here meant joining a table that carries neither scope nor subject, so the
// planner read every message row in the project to size the turns of one person in it: 1,699 of the
// 1,982 buffers one subject's context touched on a project of 81 subjects, growing with the project
// while the answer did not.
func (s *SegmentStore) Formed(ctx context.Context, scope, subject string) ([]compaction.Turn, error) {
	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT o.log_offset, o.content_characters
		  FROM {schema}.observation o
		 WHERE o.scope=$1 AND o.data_subject_id=$2 AND o.kind=$3 AND o.formed_at IS NOT NULL AND o.parked_at IS NULL
		 ORDER BY o.log_offset`), scope, subject, KindTurn)
	if err != nil {
		return nil, fmt.Errorf("read formed turns: %w", err)
	}
	defer rows.Close()
	var out []compaction.Turn
	for rows.Next() {
		var t compaction.Turn
		if err := rows.Scan(&t.Offset, &t.Size); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// History reads every turn stored for one subject in log order, each with its size in characters
// and whether a segment was written from its words. It is the context's reader: a turn's words are
// the subject's whether or not the formation pass has reached them, and assembling a context makes
// no model call, so it needs nothing from formation.
//
// Summarised is taken from the registration the segment write made against the turn, which is the
// same row an erasure of that turn follows to delete the segment. So a turn counts as summarised
// exactly when a segment would be destroyed by erasing it — the substrate's own answer, rather than
// a range comparison that would also catch the turns the pass skipped.
//
// The registration is matched on the scope and the subject as given, not on the turn's own columns.
// Written as a correlated reference to o.scope, the subquery cannot be indexed, and the planner
// answers it by scanning every segment registration in the project — so the cost of one person's
// context grows with every other person in it. With both as parameters the probe uses
// `projection_dependency_subject_idx (scope, data_subject_id)`, and the rows it reads are bounded by
// this subject's own history. On a project holding 45,960 turns across 81 subjects, asking for the
// 5,000-turn subject reads 9,966 registration rows through that index instead of 50,286 through a
// sequential scan: 191 buffers against 877. That is a plan, not a timing; no latency is claimed here.
//
// A segment registration always carries the subject, because the write copies it from the
// observation it registers against. If one ever disagreed, the turn would read as unsummarised and
// be handed over as it was said — the direction that returns words rather than hiding them.
func (s *SegmentStore) History(ctx context.Context, scope, subject string) ([]compaction.Turn, error) {
	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT o.log_offset, o.content_characters,
		       EXISTS (SELECT 1 FROM {schema}.projection_dependency d
		                WHERE d.source_observation_id=o.observation_id
		                  AND d.scope=$1 AND d.data_subject_id=$2 AND d.projection_kind=$4)
		  FROM {schema}.observation o
		 WHERE o.scope=$1 AND o.data_subject_id=$2 AND o.kind=$3
		 ORDER BY o.log_offset`),
		scope, subject, KindTurn, ProjectionSegment)
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	defer rows.Close()
	var out []compaction.Turn
	for rows.Next() {
		var t compaction.Turn
		if err := rows.Scan(&t.Offset, &t.Size, &t.Summarised); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Segments reads one subject's segments.
func (s *SegmentStore) Segments(ctx context.Context, scope, subject string) ([]compaction.Segment, error) {
	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT segment_id::text, level, from_offset, to_offset, covered, length(summary)
		  FROM {schema}.segment WHERE scope=$1 AND data_subject_id=$2 ORDER BY level, from_offset`), scope, subject)
	if err != nil {
		return nil, fmt.Errorf("read segments: %w", err)
	}
	defer rows.Close()
	var out []compaction.Segment
	for rows.Next() {
		var seg compaction.Segment
		if err := rows.Scan(&seg.ID, &seg.Level, &seg.From, &seg.To, &seg.Covered, &seg.Size); err != nil {
			return nil, err
		}
		out = append(out, seg)
	}
	return out, rows.Err()
}

// Material loads what a planned segment is written from: the turns in its range at level 1, the
// summaries of its units above.
func (s *SegmentStore) Material(ctx context.Context, scope, subject string, plan compaction.Plan) (compaction.Material, error) {
	m := compaction.Material{Level: plan.Level, From: plan.From, To: plan.To}
	if plan.Level > 1 {
		for _, u := range plan.Units {
			var summary string
			if err := s.pool.QueryRow(ctx, s.schema.SQL(`SELECT summary FROM {schema}.segment WHERE scope=$1 AND segment_id=$2::uuid`), scope, u.ID).Scan(&summary); err != nil {
				return compaction.Material{}, fmt.Errorf("read unit %s: %w", u.ID, err)
			}
			m.Units = append(m.Units, compaction.UnitText{From: u.From, To: u.To, Covered: u.Covered, Summary: summary})
		}
		return m, nil
	}
	turns, err := s.turnTexts(ctx, scope, subject, plan.From, plan.To, formedOnly)
	if err != nil {
		return compaction.Material{}, err
	}
	m.Turns = turns
	return m, nil
}

// formedOnly and everything are what the two callers of turnTexts pass: the compaction pass reads
// only what it may summarise, a context reads what the subject said. The gate is a constant, not a
// caller's string, so the query it builds is fixed at compile time.
const (
	formedOnly = true
	everyTurn  = false
)

// turnTexts reads the messages of a subject's turns in an offset range, oldest first. See the type
// comment for why its two callers see different turns.
func (s *SegmentStore) turnTexts(ctx context.Context, scope, subject string, from, to int64, onlyFormed bool) ([]compaction.TurnText, error) {
	gate := ""
	if onlyFormed {
		gate = ` AND o.formed_at IS NOT NULL AND o.parked_at IS NULL`
	}
	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT o.log_offset, o.occurred_at, m.role, m.content
		  FROM {schema}.observation o JOIN {schema}.turn_message m ON m.observation_id=o.observation_id
		 WHERE o.scope=$1 AND o.data_subject_id=$2 AND o.kind=$5`+gate+`
		   AND o.log_offset BETWEEN $3 AND $4
		 ORDER BY o.log_offset, m.ordinal`), scope, subject, from, to, KindTurn)
	if err != nil {
		return nil, fmt.Errorf("read turns: %w", err)
	}
	defer rows.Close()
	var out []compaction.TurnText
	for rows.Next() {
		var offset int64
		var at time.Time
		var role, content string
		if err := rows.Scan(&offset, &at, &role, &content); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Offset != offset {
			out = append(out, compaction.TurnText{Offset: offset, OccurredAt: at})
		}
		out[len(out)-1].Messages = append(out[len(out)-1].Messages, compaction.MessageText{Role: role, Content: content})
	}
	return out, rows.Err()
}

// ErrSegmentConflict says a segment already exists where this one would go: another replica wrote
// it, or the plan is stale.
var ErrSegmentConflict = errors.New("a segment already covers this range")

// Write stores a segment and registers it to every observation it covers, in one transaction, so
// that erasure of any of them reaches it and a segment never exists without its registrations.
func (s *SegmentStore) Write(ctx context.Context, scope, subject string, plan compaction.Plan, summary compaction.Summary) (compaction.Segment, error) {
	id := uuid.NewString()
	seg := compaction.Segment{ID: id, Level: plan.Level, From: plan.From, To: plan.To, Covered: plan.Turns, Size: len(summary.Text)}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, s.schema.SQL(`
			INSERT INTO {schema}.segment (scope, segment_id, data_subject_id, level, from_offset, to_offset, covered, summary)
			VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8)`),
			scope, id, subject, plan.Level, plan.From, plan.To, plan.Turns, summary.Text); err != nil {
			if isUniqueViolation(err) {
				return ErrSegmentConflict
			}
			return fmt.Errorf("insert segment: %w", err)
		}
		tag, err := tx.Exec(ctx, s.schema.SQL(`
			INSERT INTO {schema}.projection_dependency (source_observation_id, scope, projection_kind, projection_id, data_subject_id)
			SELECT observation_id, scope, $4, $5, data_subject_id FROM {schema}.observation
			 WHERE scope=$1 AND data_subject_id=$2 AND kind=$6 AND formed_at IS NOT NULL AND parked_at IS NULL
			   AND log_offset BETWEEN $3 AND $7`),
			scope, subject, plan.From, ProjectionSegment, id, KindTurn, plan.To)
		if err != nil {
			return fmt.Errorf("register segment: %w", err)
		}
		if tag.RowsAffected() != int64(plan.Turns) {
			// The history moved under the plan: a turn was erased or parked since it was made.
			// Refused rather than written over a range the summary does not describe.
			return fmt.Errorf("%w: the range holds %d turns, the plan covered %d", ErrSegmentConflict, tag.RowsAffected(), plan.Turns)
		}
		return nil
	})
	if err != nil {
		return compaction.Segment{}, err
	}
	return seg, nil
}

// Assembled is a context as the API returns it: the segments and the turns, oldest first, with
// their text, and the cut.
type Assembled struct {
	Segments   []AssembledSegment
	Turns      []compaction.TurnText
	Characters int
	Truncated  bool
}

type AssembledSegment struct {
	compaction.Segment
	Summary string
}

// Context assembles one subject's history under a budget, with no model call: the planner's choice
// over every turn the subject said, then the text of what it chose. A turn with a summary written
// from it is handed over as that summary; every other turn is handed over as it was said.
func (s *SegmentStore) Context(ctx context.Context, scope, subject string, budget int) (Assembled, error) {
	turns, err := s.History(ctx, scope, subject)
	if err != nil {
		return Assembled{}, err
	}
	segments, err := s.Segments(ctx, scope, subject)
	if err != nil {
		return Assembled{}, err
	}
	chosen, err := compaction.Assemble(turns, segments, budget)
	if err != nil {
		return Assembled{}, err
	}
	out := Assembled{Characters: chosen.Characters, Truncated: chosen.Truncated}
	for _, seg := range chosen.Segments {
		var summary string
		if err := s.pool.QueryRow(ctx, s.schema.SQL(`SELECT summary FROM {schema}.segment WHERE scope=$1 AND segment_id=$2::uuid`), scope, seg.ID).Scan(&summary); err != nil {
			return Assembled{}, fmt.Errorf("read segment %s: %w", seg.ID, err)
		}
		out.Segments = append(out.Segments, AssembledSegment{Segment: seg, Summary: summary})
	}
	if len(chosen.Turns) > 0 {
		texts, err := s.turnTexts(ctx, scope, subject, chosen.Turns[0].Offset, chosen.Turns[len(chosen.Turns)-1].Offset, everyTurn)
		if err != nil {
			return Assembled{}, err
		}
		wanted := map[int64]bool{}
		for _, t := range chosen.Turns {
			wanted[t.Offset] = true
		}
		for _, t := range texts {
			if wanted[t.Offset] {
				out.Turns = append(out.Turns, t)
			}
		}
	}
	return out, nil
}

// isUniqueViolation says whether an error is the substrate refusing a duplicate, which for a segment
// is the one-per-range rule holding against a concurrent writer.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
