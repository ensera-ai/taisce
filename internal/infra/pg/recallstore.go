package pg

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
)

// RecallStore reads the anchors a question names and the facts about them.
type RecallStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

// NewRecallStore binds a reader to one tenant. The schema is held rather than passed per call
// because this is the read path a request handler drives, and a tenant chosen per query is a tenant
// that can be chosen wrongly per query.
func NewRecallStore(pool *pgxpool.Pool, schema Schema) *RecallStore {
	return &RecallStore{pool: pool, schema: schema}
}

// Anchors resolves candidate terms to entities inside the authorised scopes.
//
// # Canonical name and aliases in one pass
//
// An entity accumulates the surface forms it has actually been referred to. Matching only the
// canonical name would miss a question that used the same words the conversation used — which is
// most questions, because a person asks about something the way they mentioned it.
//
// # The scope predicate is on every read
//
// A project is a permission inside a tenant, so the authorised set is a parameter and the check is a
// predicate. That is the soft boundary; the hard one is the schema name in the statement, which no
// predicate can be wrong about.
func (s *RecallStore) Anchors(ctx context.Context, scopes, terms []string) ([]domain.Anchor, error) {
	rows, err := s.pool.Query(ctx, s.schema.SQL(selectAnchorsSQL), scopes, terms)
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
SELECT e.entity_id::text, e.scope, e.canonical_name, e.normalized_name, e.entity_type, t.term
  FROM unnest($2::text[]) AS t(term)
  JOIN {schema}.entity e
    ON e.scope = ANY($1)
   AND (e.normalized_name = t.term OR t.term = ANY(e.normalized_aliases))
 ORDER BY length(t.term) DESC, e.canonical_name`

// FactsAbout returns the currently-valid facts naming any of these entities, with their evidence.
//
// # Two index-driven arms rather than one OR
//
// A fact names an entity as its subject or as its object, and `subject = ANY($2) OR object = ANY($2)`
// across two columns defeats both indexes — the planner falls back to a sequential scan over every
// fact in the tenant, which is exactly the cost curve this product exists to avoid. Written as a
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
// gets every project (D24).
func (s *RecallStore) FactsAbout(ctx context.Context, scopes, entityIDs []string, limit int,
	sources []string) ([]domain.CitedFact, error) {

	if len(sources) == 0 {
		// An empty set returns nothing rather than everything. A caller that computed its source
		// list and got nothing meant nothing; the alternative — treating empty as unrestricted — is
		// the failure the whole mechanism exists to prevent.
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, s.schema.SQL(selectFactsAboutSQL), scopes, entityIDs, limit, sources)
	if err != nil {
		return nil, fmt.Errorf("read facts: %w", err)
	}
	defer rows.Close()

	var out []domain.CitedFact
	for rows.Next() {
		var f domain.CitedFact
		var subject, object *string
		if err := rows.Scan(&f.ID, &f.Scope, &subject, &f.Predicate, &object, &f.Statement,
			&f.Confidence, &f.ValidFrom, &f.AnchoredOn, &f.SourceRole,
			&f.Evidence.SourceObservationID, &f.Evidence.SourceOrdinal, &f.Evidence.Quote,
			&f.Evidence.ByteStart, &f.Evidence.ByteEnd, &f.Evidence.ExtractorVersion); err != nil {
			return nil, err
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
const selectFactsAboutSQL = `
WITH reachable AS (
    SELECT f.fact_id, f.scope, f.subject_entity_id, f.predicate, f.object_entity_id, f.statement,
           f.confidence, lower(f.valid) AS valid_from, f.subject_entity_id AS anchored_on,
           f.source_role
      FROM {schema}.fact f
     WHERE f.scope = ANY($1)
       AND f.subject_entity_id = ANY($2::uuid[])
       AND f.source_role = ANY($4)
       AND upper_inf(f.valid) AND upper_inf(f.known)
    UNION ALL
    SELECT f.fact_id, f.scope, f.subject_entity_id, f.predicate, f.object_entity_id, f.statement,
           f.confidence, lower(f.valid) AS valid_from, f.object_entity_id AS anchored_on,
           f.source_role
      FROM {schema}.fact f
     WHERE f.scope = ANY($1)
       AND f.object_entity_id = ANY($2::uuid[])
       AND f.source_role = ANY($4)
       AND upper_inf(f.valid) AND upper_inf(f.known)
)
SELECT r.fact_id::text, r.scope, s.canonical_name, r.predicate, o.canonical_name, r.statement,
       r.confidence, r.valid_from, r.anchored_on::text, r.source_role,
       e.source_observation_id::text, e.source_ordinal, e.quote, e.byte_start, e.byte_end,
       e.extractor_version
  FROM reachable r
  JOIN {schema}.fact_evidence e ON e.fact_id = r.fact_id
  LEFT JOIN {schema}.entity s ON s.entity_id = r.subject_entity_id
  LEFT JOIN {schema}.entity o ON o.entity_id = r.object_entity_id
 ORDER BY r.predicate, r.fact_id
 LIMIT $3`
