package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taisce/taisce/internal/domain"
)

// FactStore resolves entities and asserts facts with their evidence.
type FactStore struct{ pool *pgxpool.Pool }

func NewFactStore(pool *pgxpool.Pool) *FactStore { return &FactStore{pool: pool} }

// Projection kinds. Erasure walks `projection_dependency`, so these strings are what make a fact and
// an entity reachable from the observation they derived from.
const (
	ProjectionFact   = "fact"
	ProjectionEntity = "entity"
)

// ExtractorVersion is stamped on every piece of evidence, so a corpus extracted under changed rules
// is distinguishable from one extracted under these. It migrates nothing; it makes the difference
// visible when somebody asks why two facts that look alike disagree.
const ExtractorVersion = "extract/v1"

// ErrNotSpokenByPrincipal is returned when a claim would become a fact about the principal but the
// message it came from was not the principal speaking.
var ErrNotSpokenByPrincipal = errors.New("claim is not the principal's to make")

// Assert resolves both entity ends and writes the fact with its evidence, in one transaction.
//
// # The role policy is enforced here, not at the edge
//
// It could be enforced in the handler, and then every future write path would have to remember to.
// Here it is on the only road to the fact table, so a path that forgets does not exist.
//
// A claim from an assistant or a tool message is refused as a fact ABOUT THE PRINCIPAL. That is not
// the same as refusing to remember it: the message is already stored and retrievable as a passage.
// What is refused is the promotion of a model's sentence into something the person is recorded as
// having said, because once promoted nothing distinguishes it from something they actually told us.
func (s *FactStore) Assert(ctx context.Context, schema Schema, scope, observationID string,
	speaker domain.Role, dataSubjectID string, claim domain.Claim) (string, error) {

	if dataSubjectID != "" && !speaker.SpeaksForPrincipal() {
		return "", fmt.Errorf("%w: message %d has role %q", ErrNotSpokenByPrincipal, claim.SourceOrdinal, speaker)
	}

	var factID string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		subjectID, err := resolveEntity(ctx, tx, schema, scope, observationID, claim.Subject, claim.SubjectType, dataSubjectID)
		if err != nil {
			return err
		}
		objectID, err := resolveEntity(ctx, tx, schema, scope, observationID, claim.Object, claim.ObjectType, dataSubjectID)
		if err != nil {
			return err
		}

		id := uuid.New()
		validFrom := claim.ValidFrom
		if validFrom.IsZero() {
			validFrom = time.Now().UTC()
		}
		if _, err := tx.Exec(ctx, schema.SQL(insertFactSQL),
			id, scope, nullIfEmptyString(subjectID), nullIfEmptyString(objectID),
			claim.Predicate, claim.Statement, validFrom, claim.Confidence,
			nullIfEmpty(dataSubjectID)); err != nil {
			return fmt.Errorf("insert fact: %w", err)
		}

		// Evidence names the MESSAGE, not the turn. A byte span against a concatenation that exists
		// nowhere resolves to the wrong words while looking entirely correct.
		if _, err := tx.Exec(ctx, schema.SQL(insertEvidenceSQL),
			id, observationID, claim.Quote, claim.ByteStart, claim.ByteEnd, ExtractorVersion); err != nil {
			return fmt.Errorf("insert evidence: %w", err)
		}

		if _, err := tx.Exec(ctx, schema.SQL(registerProjectionSQL),
			observationID, scope, ProjectionFact, id.String(), nullIfEmpty(dataSubjectID)); err != nil {
			return fmt.Errorf("register fact projection: %w", err)
		}

		factID = id.String()
		return nil
	})
	return factID, err
}

func nullIfEmptyString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// resolveEntity finds or creates the entity a name refers to, inside the caller's transaction.
//
// # Resolution is by normalised name and deliberately by nothing cleverer
//
// Lowercase, trim, collapse whitespace. That merges the overwhelming majority of what an extractor
// produces for one thing, because it is reading the same words back in different sentences.
//
// It does NOT merge "Dublin office" with "our Dublin site", and nothing here pretends to. Doing that
// needs an embedding comparison with a threshold nobody can justify, or a model call per entity at
// write time. Under-merging leaves two nodes where there should be one, costing recall on a hop.
// Over-merging puts two people's facts on one node, which is a governance failure. The asymmetry
// decides it.
//
// # Why the type is not part of identity
//
// With the type in the key, one thing extracted once as a place and once as a thing becomes two
// nodes holding two halves of what is known about it — a silent under-merge on every hop, invisible
// because both rows look correct on their own. The type is an attribute, kept as first seen.
//
// # Why this runs in the caller's transaction
//
// A failed assert must leave no entity behind. Resolved separately, it would leave orphans for facts
// that do not exist — harmless individually, and exactly the kind of drift that makes an entity
// count stop meaning anything.
func resolveEntity(ctx context.Context, tx pgx.Tx, schema Schema, scope, observationID, name, entityType,
	dataSubjectID string) (string, error) {

	normalized := domain.NormalizeName(name)
	if normalized == "" {
		// A blank end is not an entity. Empty rather than an error: the fact is still worth
		// storing, it simply has an unresolvable end, and traversal skips it.
		return "", nil
	}
	if entityType == "" {
		entityType = "thing"
	}

	// UPSERT rather than select-then-insert. Two observations naming the same entity form
	// concurrently, so a check-then-write races and the loser fails on the unique constraint.
	//
	// DO UPDATE rather than DO NOTHING, because DO NOTHING returns no row and would need a second
	// round trip to read the winner. The update is a no-op assignment that exists to make RETURNING
	// fire — the canonical name is kept as first seen, so a later mention in different casing does
	// not rewrite what a person reads.
	var id string
	if err := tx.QueryRow(ctx, schema.SQL(upsertEntitySQL),
		uuid.New(), scope, name, normalized, entityType).Scan(&id); err != nil {
		return "", fmt.Errorf("resolve entity %q: %w", name, err)
	}

	// The surface form becomes an alias when it differs from the canonical one, so the entity
	// accumulates the ways it has actually been referred to. Cheap, and it is what an operator
	// judging whether a merge was right needs to see.
	if _, err := tx.Exec(ctx, schema.SQL(addAliasSQL), id, name, normalized); err != nil {
		return "", fmt.Errorf("record alias for %q: %w", name, err)
	}

	if _, err := tx.Exec(ctx, schema.SQL(registerProjectionSQL),
		observationID, scope, ProjectionEntity, id, nullIfEmpty(dataSubjectID)); err != nil {
		return "", fmt.Errorf("register entity projection: %w", err)
	}
	return id, nil
}

const upsertEntitySQL = `
INSERT INTO {schema}.entity (entity_id, scope, canonical_name, normalized_name, entity_type)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (scope, normalized_name) DO UPDATE
   SET resolution_version = {schema}.entity.resolution_version
RETURNING entity_id::text`

// Appended only when absent and only when it differs from the canonical name, so an entity mentioned
// a thousand times does not accumulate a thousand copies. Both arrays move together — one is what a
// person reads and the other is what a lookup matches — because one without the other is how they
// drift.
const addAliasSQL = `
UPDATE {schema}.entity
   SET aliases            = array_append(aliases, $2),
       normalized_aliases = array_append(normalized_aliases, $3)
 WHERE entity_id = $1::uuid
   AND canonical_name <> $2
   AND NOT ($3 = ANY(normalized_aliases))`

// `known` defaults to [now, ∞): we believe it from the moment it is recorded. `valid` opens at the
// claim's own time, because when something became true and when we heard about it are different
// questions and the whole point of two axes is to answer both.
const insertFactSQL = `
INSERT INTO {schema}.fact
    (fact_id, scope, subject_entity_id, object_entity_id, predicate, statement,
     valid, confidence, data_subject_id)
VALUES ($1, $2, $3::uuid, $4::uuid, $5, $6, tstzrange($7, NULL), $8, $9)`

const insertEvidenceSQL = `
INSERT INTO {schema}.fact_evidence
    (fact_id, source_observation_id, quote, byte_start, byte_end, extractor_version)
VALUES ($1, $2, $3, $4, $5, $6)`
