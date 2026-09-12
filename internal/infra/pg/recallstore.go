// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
)

// RecallStore reads the anchors a question names and the facts about them.
type RecallStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

// NewRecallStore binds a reader to the instance memory namespace. Project authorization is a
// predicate on each read; a request never chooses a database namespace.
func NewRecallStore(pool *pgxpool.Pool, schema Schema) *RecallStore {
	return &RecallStore{pool: pool, schema: schema}
}

// Anchors resolves candidate terms to entities inside the authorised scopes.
//
// # Canonical name lookup
//
// A window matches an entity's normalized canonical name. An entity still records the surface forms
// it has been referred to, but migration 0051 removed the alias cache and its index, so nothing
// matches against them: a question that uses a spelling nobody canonicalised reaches the entity
// through the semantic surfaces instead.
//
// # The scope predicate is on every read
//
// The authorised project set is a parameter. Runtime database privileges cover the instance;
// they do not replace these project predicates.
func (s *RecallStore) Anchors(ctx context.Context, scopes, terms []string) ([]domain.Anchor, error) {
	return s.AnchorsForSubject(ctx, scopes, terms, "")
}

// AnchorsForSubject includes the selected speaker without requiring their name in the question.
// The filter is caller attribution within authorised projects, not proof of an end user's identity.
func (s *RecallStore) AnchorsForSubject(ctx context.Context, scopes, terms []string, subject string) ([]domain.Anchor, error) {
	if err := domain.ValidateRecallTerms(terms); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, s.schema.SQL(selectAnchorsSQL), scopes, terms, nullIfEmpty(subject), domain.MaxRecallMatches+1)
	if err != nil {
		return nil, fmt.Errorf("resolve anchors: %w", err)
	}
	defer rows.Close()

	var out []domain.Anchor
	for rows.Next() {
		var a domain.Anchor
		if err := rows.Scan(&a.Entity.ID, &a.Entity.Scope, &a.Entity.CanonicalName,
			&a.Entity.NormalizedName, &a.Entity.Type, &a.Matched); err != nil {
			return nil, err
		}
		out = append(out, a)
		if len(out) > domain.MaxRecallMatches {
			return nil, domain.ErrRecallMatches
		}
	}
	return out, rows.Err()
}

// The matched term is returned alongside the entity, because an anchor a caller did not expect is
// the first thing to look at when a bundle is wrong — and without it the only way to find out why an
// entity was chosen is to re-run the resolution by hand.
//
// Ordered longest match first, so `dublin office` precedes `dublin` when a question named both. That
// is not a ranking of facts; it is the order the anchors are reported in, and it exists so a reader
// sees the most specific thing the question named at the top.
const selectAnchorsSQL = `
WITH terms AS (SELECT DISTINCT unnest($2::text[]) AS term)
SELECT * FROM (
 SELECT * FROM (
    SELECT e.entity_id::text, e.scope, e.canonical_name, e.normalized_name, e.entity_type, t.term
      FROM terms t
      JOIN {schema}.entity e ON e.scope = ANY($1) AND e.identity_kind='named'
       AND e.normalized_name=t.term
     WHERE $3::text IS NULL OR EXISTS (
        SELECT 1 FROM {schema}.projection_dependency d
         WHERE d.scope=e.scope AND d.projection_kind='entity' AND d.projection_id=e.entity_id::text
           AND d.data_subject_id=$3)
    UNION ALL
    SELECT e.entity_id::text,e.scope,e.canonical_name,e.normalized_name,e.entity_type,'speaker'
      FROM {schema}.entity e
     WHERE e.scope=ANY($1) AND e.identity_kind='speaker' AND e.speaker_subject_id=$3
 ) candidates LIMIT $4
) anchors
ORDER BY length(term) DESC, canonical_name, entity_id`

// FactsAbout returns the currently-valid facts naming any of these entities, with their evidence.
//
// # Two index-driven arms rather than one OR
//
// A fact names an entity as its subject or as its object, and `subject = ANY($2) OR object = ANY($2)`
// across two columns defeats both indexes — the planner falls back to a sequential scan over every
// fact in the instance, which is exactly the cost curve this product exists to avoid. Written as a
// UNION ALL of two arms, each of which drives its own index.
//
// # Evidence is an inner join
//
// A fact with no evidence is not returned. That is a product statement rather than an oversight:
// what is sold is a memory that can show its receipt, and a fact whose receipt cannot be produced is
// not one this system offers. Formation writes evidence in the same transaction as the fact, so a
// fact without it is a defect rather than an ordinary state.
//
// # Nothing here ranks
//
// The order is deterministic and carries no judgment: predicate, then fact id. It is not recency, not
// confidence and not salience, because each of those is a stage that has to be justified by a
// measurement it improves — and introduced before the plain path is measured, it becomes impossible
// to say which stage is doing the work or which is doing harm.
// FactsAbout returns facts naming the given entities, from the sources the caller admits.
//
// `sources` is the set of message roles whose facts may come back. It is an ARGUMENT rather than a
// default because that is what makes the bound impossible to forget: there is no call that omits it
// and gets everything, in the same way there is no read that omits the authorised project set and
// gets every project.
func (s *RecallStore) FactsAbout(ctx context.Context, scopes, entityIDs []string, limit int,
	sources []string, at domain.AsOf, hops int) ([]domain.CitedFact, error) {
	return s.FactsAboutForSubject(ctx, scopes, entityIDs, limit, sources, at, hops, "")
}

// FactsAboutForSubject applies attribution before fanout at EVERY hop, including through shared
// named entities. Filtering the final bundle would already have let other people's edges guide it.
func (s *RecallStore) FactsAboutForSubject(ctx context.Context, scopes, entityIDs []string, limit int,
	sources []string, at domain.AsOf, hops int, subject string) ([]domain.CitedFact, error) {

	if len(sources) == 0 {
		// An empty set returns nothing rather than everything. A caller that computed its source
		// list and got nothing meant nothing; the alternative — treating empty as unrestricted — is
		// the failure the whole mechanism exists to prevent.
		return nil, nil
	}
	// A statement per shape of the question, rather than one statement with an OR on the temporal
	// predicate. `($6 IS NULL AND upper_inf(valid)) OR valid @> $6` is unindexable in both of its
	// arms: the planner cannot know which branch it is planning for, so it plans for neither.
	if hops < 1 {
		hops = 1
	}
	stmt := selectFactsAboutSQL
	args := []any{scopes, entityIDs, limit, sources, domain.ContextWindow, hops, domain.Fanout, nullIfEmpty(subject)}
	switch {
	case at.HasValid() && at.HasKnown():
		stmt, args = selectFactsBitemporalSQL, append(args, at.Valid.UTC(), at.Known.UTC())
	case at.HasValid():
		stmt, args = selectFactsValidAtSQL, append(args, at.Valid.UTC())
	case at.HasKnown():
		stmt, args = selectFactsKnownAtSQL, append(args, at.Known.UTC())
	}
	rows, err := s.pool.Query(ctx, s.schema.SQL(stmt), args...)
	if err != nil {
		return nil, fmt.Errorf("read facts: %w", err)
	}
	defer rows.Close()

	var out []domain.CitedFact
	for rows.Next() {
		var f domain.CitedFact
		var subject, object *string
		var window []byte
		var windowStart, messageBytes *int
		var via, path []string
		if err := rows.Scan(&f.ID, &f.Scope, &subject, &f.Predicate, &object, &f.Statement,
			&f.Confidence, &f.ValidFrom, &f.ValidUntil, &f.AnchoredOn, &f.SourceRole,
			&f.Evidence.SourceObservationID, &f.Evidence.SourceOrdinal, &f.Evidence.Quote,
			&f.Evidence.ByteStart, &f.Evidence.ByteEnd, &f.Evidence.ExtractorVersion,
			&window, &windowStart, &messageBytes, &f.Hops, &via, &path); err != nil {
			return nil, err
		}
		// No message means no context, and the fact still comes back. A message can be gone because
		// it was erased, and a citation that lost its surrounding words has lost legibility rather
		// than validity — the quote and the span are still what they were. Refusing the fact here
		// would make an erasure elsewhere in the turn look like memory loss.
		if window != nil && windowStart != nil && messageBytes != nil {
			f.Evidence.Context = domain.RawWindow{
				Bytes:        window,
				Start:        *windowStart,
				QuoteStart:   f.Evidence.ByteStart,
				QuoteEnd:     f.Evidence.ByteEnd,
				MessageBytes: *messageBytes,
			}.Legible()
		}
		// The chain, and only when there is one to show. A one-hop fact was reached directly from
		// something the question named, and reporting a path of one entity and no relations would be
		// ceremony on every row of the ordinary bundle.
		if f.Hops > 1 {
			f.Path, f.Via = path, via
		}

		// An end whose entity was erased is left blank rather than dropped. The fact is still a
		// fact and its evidence still resolves; what it has lost is a hop, and reporting the loss
		// is more honest than hiding the row.
		if subject != nil {
			f.Subject = *subject
		}
		if object != nil {
			f.Object = *object
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// `upper_inf(valid)` and `upper_inf(known)` — what is true now, as best we know now. "What was true
// at T1 as we understood it at T2" is the same table read with containment operators instead, and it
// is a different question with a different caller.
//
// The anchor id is carried through both arms, so a reader can see which entity brought a fact into
// the bundle rather than having to work it out from the ends.
//
// # The context window is cut in the database, and the scope predicate is on the join that cuts it
//
// The span is bytes, so the window is byte arithmetic — and doing it here rather than in the server
// is what stops a hundred-kilobyte message crossing the wire so that three hundred bytes of it can
// be shown. `substring` clamps at both ends, so a span near either edge of a message returns a
// shorter window rather than failing.
//
// The join reaches the message through its observation with `src.scope = r.scope` written on it. The
// fact has already passed the scope predicate and the evidence names an observation that produced
// it, so a cross-scope reach would need a defect upstream — which is exactly why the predicate is
// here as well. Text is the thing a boundary failure leaks, and this is the one join in the read
// path that returns text nobody has cited.
//
// Both joins are LEFT: an erased message leaves a fact whose quote and span are still what they
// were, and dropping the row would make an erasure elsewhere in the turn look like memory loss.
const factsAboutTemplate = `
WITH RECURSIVE reachable AS (
    SELECT a.entity_id, a.entity_id AS anchor, 0 AS hops,
           ARRAY[a.entity_id] AS path, ARRAY[]::text[] AS via,
           NULL::uuid AS fact_id, NULL::text AS scope, NULL::uuid AS subject_entity_id,
           NULL::text AS predicate, NULL::uuid AS object_entity_id, NULL::text AS statement,
           NULL::real AS confidence, NULL::timestamptz AS valid_from,
           NULL::timestamptz AS valid_until, NULL::text AS source_role
      FROM unnest($2::uuid[]) AS a(entity_id)
    UNION ALL
    SELECT step.other, r.anchor, r.hops + 1,
           r.path || step.other, r.via || step.predicate,
           step.fact_id, step.scope, step.subject_entity_id, step.predicate,
           step.object_entity_id, step.statement, step.confidence, step.valid_from,
           step.valid_until, step.source_role
      FROM reachable r
      CROSS JOIN LATERAL (
          SELECT * FROM (
              SELECT f.object_entity_id AS other, f.fact_id, f.scope, f.subject_entity_id,
                     f.predicate, f.object_entity_id, f.statement, f.confidence,
                     lower(f.valid) AS valid_from, upper(f.valid) AS valid_until, f.source_role
                FROM {schema}.fact f
               WHERE f.scope = ANY($1)
                 AND f.subject_entity_id = r.entity_id
                 AND f.source_role = ANY($4)
                 AND ($8::text IS NULL OR f.data_subject_id=$8)
                 AND (f.retention_until IS NULL OR f.retention_until > now())
                 AND %[1]s
               ORDER BY f.fact_id
               LIMIT $7
          ) forward
          UNION ALL
          SELECT * FROM (
              SELECT f.subject_entity_id AS other, f.fact_id, f.scope, f.subject_entity_id,
                     f.predicate, f.object_entity_id, f.statement, f.confidence,
                     lower(f.valid) AS valid_from, upper(f.valid) AS valid_until, f.source_role
                FROM {schema}.fact f
               WHERE f.scope = ANY($1)
                 AND f.object_entity_id = r.entity_id
                 AND f.source_role = ANY($4)
                 AND ($8::text IS NULL OR f.data_subject_id=$8)
                 AND (f.retention_until IS NULL OR f.retention_until > now())
                 AND %[1]s
               ORDER BY f.fact_id
               LIMIT $7
          ) backward
      ) step
     WHERE r.hops < $6
       AND step.other IS NOT NULL
       AND NOT (step.other = ANY(r.path))
),
reached AS (
    SELECT DISTINCT ON (r.fact_id) r.*
      FROM reachable r
     WHERE r.fact_id IS NOT NULL
     ORDER BY r.fact_id, r.hops
)
SELECT r.fact_id::text, r.scope, s.canonical_name, r.predicate, o.canonical_name, r.statement,
       r.confidence, r.valid_from, r.valid_until, r.anchor::text, r.source_role,
       e.source_observation_id::text, e.source_ordinal, e.quote, e.byte_start, e.byte_end,
       e.extractor_version,
       substring(convert_to(m.content, 'UTF8')
                 from greatest(0, e.byte_start - $5) + 1
                 for  (e.byte_end + $5) - greatest(0, e.byte_start - $5)),
       greatest(0, e.byte_start - $5),
       octet_length(m.content),
       r.hops, r.via,
       (SELECT array_agg(coalesce(pe.canonical_name, '') ORDER BY p.ord)
          FROM unnest(r.path) WITH ORDINALITY AS p(id, ord)
          LEFT JOIN {schema}.entity pe ON pe.entity_id = p.id AND pe.scope = ANY($1))
  FROM reached r
  -- One citation per fact: the earliest span in the log that the caller may see. A fact several
  -- spans support is still one fact, and joining every evidence row returned it once per span,
  -- charged each copy to the budget and counted each against the row limit. The other spans
  -- stay reachable through record inspection.
  -- A LEFT join to the observation, as the context join below has always been: a fact whose message
  -- was erased, or whose evidence names a turn this project does not hold, is still returned with its
  -- quote and no context. Only a named subject requires the observation, as it always did.
  JOIN LATERAL (
      SELECT ev.*
        FROM {schema}.fact_evidence ev
        LEFT JOIN {schema}.observation source
          ON source.observation_id = ev.source_observation_id AND source.scope = r.scope
       WHERE ev.fact_id = r.fact_id
         AND ($8::text IS NULL OR source.data_subject_id = $8)
       ORDER BY source.log_offset NULLS LAST, ev.source_ordinal, ev.byte_start
       LIMIT 1
  ) e ON true
  LEFT JOIN {schema}.entity s ON s.entity_id = r.subject_entity_id
  LEFT JOIN {schema}.entity o ON o.entity_id = r.object_entity_id
  LEFT JOIN {schema}.observation src
         ON src.observation_id = e.source_observation_id
        AND src.scope = r.scope
  LEFT JOIN {schema}.turn_message m
         ON m.observation_id = src.observation_id
        AND m.ordinal = e.source_ordinal
 ORDER BY r.hops, r.predicate, r.fact_id
 LIMIT $3`

// The four reads the template makes, and every difference between them is in one predicate.
//
// # Two halves, named independently
//
// A caller can ask about a moment in the world, a moment in this system's belief, or both. The half
// they did not name is the OPEN interval rather than a timestamp of now, and that distinction is not
// cosmetic: `known` is stamped by the database's clock and a "now" computed anywhere else is a
// different clock — measured 82ms apart between a server process and a database container on one
// machine. Substituting our clock hides a fact the database recorded a moment ago, so a caller writes
// something and a read that named only a validity time cannot see it.
//
// # The current read asks whether anything has superseded the fact, not whether now is inside it
//
// `upper_inf(valid)` rather than `valid @> now()`, for the same reason from the other side. A turn
// whose `occurred_at` is a few minutes ahead of this server's clock — ordinary when the client sends
// the time — produces a fact whose validity starts in the future, and containment would hide it until
// the clock caught up. The question the current read is really asking is "is this still the answer".
//
// It is also the predicate `fact_reconcile_idx` is partial on, so the hot read stays on the small
// index holding only current facts rather than one growing with every superseded version.
//
// # The as-of reads are containment, which is the bitemporal question
//
// `valid @> $6` is what was true of the world then; `known @> $6` or `$7` is what this system had
// been told by then. Together they answer "what would you have told me at that moment", which is the
// question an audit asks and the one no scalar timestamp can answer. `fact_bitemporal_idx` is the
// GiST index over exactly this pair.
var (
	selectFactsAboutSQL = fmt.Sprintf(factsAboutTemplate,
		"upper_inf(f.valid) AND upper_inf(f.known)")
	selectFactsValidAtSQL = fmt.Sprintf(factsAboutTemplate,
		"f.valid @> $9::timestamptz AND upper_inf(f.known)")
	selectFactsKnownAtSQL    = historicalFactsSQL(9, "upper_inf(state.valid)")
	selectFactsBitemporalSQL = historicalFactsSQL(10, "state.valid @> $9::timestamptz")
)

// Historical statements retain the indexed endpoint arms and add a per-fact range lookup. Ordinary
// current reads never join history. Temporal state is selected before fanout, at every hop.
func historicalFactsSQL(knownParam int, predicate string) string {
	query := fmt.Sprintf(factsAboutTemplate, predicate)
	join := fmt.Sprintf(`FROM {schema}.fact f
 JOIN LATERAL (
   SELECT f.valid WHERE f.known @> $%[1]d::timestamptz
   UNION ALL
   SELECT h.valid FROM {schema}.fact_history h
    WHERE h.fact_id=f.fact_id AND h.scope=f.scope AND h.known @> $%[1]d::timestamptz
 ) state ON true`, knownParam)
	query = strings.ReplaceAll(query, "FROM {schema}.fact f", join)
	query = strings.ReplaceAll(query, "lower(f.valid)", "lower(state.valid)")
	return strings.ReplaceAll(query, "upper(f.valid)", "upper(state.valid)")
}
