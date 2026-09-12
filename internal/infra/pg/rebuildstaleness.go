// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Stale is what a rebuild left behind.
//
// # Why an operation reports what it broke
//
// A rebuild publishes a new interpretation of a project's turns and finishes. From the operator's
// side that looks complete, and it is not: the entities the old interpretation made are still
// there, the embedding generations were built over content that has changed, and the reports were
// deleted by their invalidation trigger and come back only when the worker's subject pass reaches
// them. Each has a different remedy and none of them is visible.
//
// This does not repair any of it — re-deriving those projections is larger work, and doing it
// silently inside a rebuild would be the same mistake in the other direction. It reports, so an
// operator knows what to do next and a monitoring system can see that something is owed.
//
// Every number here is a count against a table that already exists. Nothing is stored; a staleness
// report is true at the moment it is read and would be a lie the moment after.
type Stale struct {
	// Entities is how many entities no current fact refers to. A rebuild that changed its mind
	// about a claim leaves the entity it used to point at, and nothing removes it: the alias cache
	// then answers with names the current extractor never produced.
	Entities int `json:"unreferenced_entities"`
	// NameReceipts is how many alias receipts belong to those entities.
	NameReceipts int `json:"unreferenced_name_receipts"`
	// Communities is how many subjects are waiting for a report. The invalidation trigger deletes a
	// report when its facts change; the subject pass writes them back a few per tick, so this is the
	// size of the window in which a recall that would have returned a theme returns nothing.
	Communities int `json:"communities_without_a_report"`
	// MessageEmbeddings, EntityEmbeddings and ReportEmbeddings are whether the active generation of
	// each still covers what is stored. False means a search is reading an index built for content
	// that has since changed — which `Activate` would refuse today, but a generation activated
	// before the rebuild is not asked again.
	MessageEmbeddings EmbeddingCoverage `json:"message_embeddings"`
	EntityEmbeddings  EmbeddingCoverage `json:"entity_embeddings"`
	ReportEmbeddings  EmbeddingCoverage `json:"report_embeddings"`
}

// EmbeddingCoverage is one semantic surface's active generation, and whether it still covers.
//
// # Why the three surfaces answer differently
//
// A message generation records the log offset it was built through, so its staleness is arithmetic:
// the log has moved on by this much. An entity or report generation records how many targets existed
// instead, and the database marks its build `stale` when that set changes — which is a stronger
// signal than a count, because it fires on the change itself. Reporting an offset for a surface that
// does not have one would be inventing a number, so each is read by the mechanism it actually has.
type EmbeddingCoverage struct {
	// Active is false when no generation has been activated for this surface, which is not
	// staleness: it is a deployment that has never built one, and saying "stale" would send an
	// operator looking for a problem they do not have.
	Active bool `json:"active"`
	// Covers is what a message generation was built up to and Current is where the log is now;
	// Behind is the difference. Absent for the other two surfaces, which have no offset.
	Covers  int64 `json:"covers_through_offset,omitempty"`
	Current int64 `json:"current_offset,omitempty"`
	Behind  int64 `json:"behind,omitempty"`
	// Stale is the database's own verdict on an entity or report generation whose input set has
	// changed since it was built.
	Stale bool `json:"stale,omitempty"`
}

// Behindhand reports whether this surface needs a new generation, by whichever measure it has.
func (c EmbeddingCoverage) Behindhand() bool { return c.Behind > 0 || c.Stale }

// Anything reports whether a rebuild left anything worth telling an operator about.
func (s Stale) Anything() bool {
	return s.Entities > 0 || s.Communities > 0 ||
		s.MessageEmbeddings.Behindhand() || s.EntityEmbeddings.Behindhand() || s.ReportEmbeddings.Behindhand()
}

// Staleness reads what a rebuild of this project left behind.
//
// Read rather than remembered: these are consequences of the state the database is in now, and a
// number recorded at the end of a rebuild would be wrong by the time anybody looked — the subject
// pass writes reports back continuously, so the honest answer changes every few seconds.
func (s *RebuildJobStore) Staleness(ctx context.Context, schema Schema, scope string) (Stale, error) {
	var out Stale
	// An entity nothing points at. Both ends of a fact count, and a fact that is no longer current
	// does not: the question is what the CURRENT interpretation refers to.
	if err := s.pool.QueryRow(ctx, schema.SQL(`
		SELECT count(*)::int,
		       coalesce((SELECT count(*) FROM {schema}.entity_name_receipt r
		                  WHERE r.scope=$1 AND r.entity_id IN (
		                    SELECT e.entity_id FROM {schema}.entity e
		                     WHERE e.scope=$1 AND NOT EXISTS (
		                       SELECT 1 FROM {schema}.fact f
		                        WHERE f.scope=e.scope AND upper_inf(f.known)
		                          AND (f.subject_entity_id=e.entity_id OR f.object_entity_id=e.entity_id))))::int, 0)
		  FROM {schema}.entity e
		 WHERE e.scope=$1
		   AND NOT EXISTS (SELECT 1 FROM {schema}.fact f
		                    WHERE f.scope=e.scope AND upper_inf(f.known)
		                      AND (f.subject_entity_id=e.entity_id OR f.object_entity_id=e.entity_id))`),
		scope).Scan(&out.Entities, &out.NameReceipts); err != nil {
		return Stale{}, fmt.Errorf("count unreferenced entities: %w", err)
	}

	if err := s.pool.QueryRow(ctx, schema.SQL(`
		SELECT count(*)::int FROM {schema}.community c
		 WHERE c.scope=$1
		   AND NOT EXISTS (SELECT 1 FROM {schema}.community_report r
		                    WHERE r.scope=c.scope AND r.community_id=c.community_id)`),
		scope).Scan(&out.Communities); err != nil {
		return Stale{}, fmt.Errorf("count communities awaiting a report: %w", err)
	}

	var current int64
	if err := s.pool.QueryRow(ctx, schema.SQL(
		`SELECT coalesce(max(log_offset),0) FROM {schema}.observation WHERE scope=$1`), scope).Scan(&current); err != nil {
		return Stale{}, fmt.Errorf("read the current offset: %w", err)
	}

	// Message embeddings: an offset, so the answer is arithmetic.
	messages := EmbeddingCoverage{Current: current}
	var through *int64
	if err := s.pool.QueryRow(ctx, schema.SQL(`
		SELECT g.through_offset FROM {schema}.embedding_active a
		  JOIN {schema}.embedding_generation g ON g.scope=a.scope AND g.generation_id=a.generation_id
		 WHERE a.scope=$1`), scope).Scan(&through); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Stale{}, fmt.Errorf("read message embedding coverage: %w", err)
	}
	if through != nil {
		messages.Active, messages.Covers = true, *through
		if behind := current - *through; behind > 0 {
			messages.Behind = behind
		}
	}
	out.MessageEmbeddings = messages

	// Entity and report embeddings: no offset, and a build the database marks stale when the set of
	// things it covers changes. That verdict is the one to report.
	for _, surface := range []struct {
		into   *EmbeddingCoverage
		active string
		build  string
	}{
		{&out.EntityEmbeddings, "entity_embedding_active", "entity_embedding_build"},
		{&out.ReportEmbeddings, "report_embedding_active", "report_embedding_build"},
	} {
		var state *string
		if err := s.pool.QueryRow(ctx, schema.SQL(fmt.Sprintf(`
			SELECT b.state FROM {schema}.%s a
			  JOIN {schema}.%s b ON b.scope=a.scope AND b.generation_id=a.generation_id
			 WHERE a.scope=$1`, surface.active, surface.build)), scope).Scan(&state); err != nil &&
			!errors.Is(err, pgx.ErrNoRows) {
			return Stale{}, fmt.Errorf("read %s state: %w", surface.active, err)
		}
		if state != nil {
			*surface.into = EmbeddingCoverage{Active: true, Stale: *state == "stale"}
		}
	}
	return out, nil
}
