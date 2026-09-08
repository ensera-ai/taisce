// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
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

// ErrUnboundSpeaker refuses a relative reference without matching stored user attribution.
var ErrUnboundSpeaker = errors.New("speaker reference needs the observation's attributed user")

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
	// Refused here rather than by the foreign key, which would also refuse it — as a constraint
	// violation from four frames down, naming a column instead of the mistake. Cardinality decides
	// whether this assertion supersedes an earlier fact or sits beside it, so a claim that does not
	// carry one is a claim nobody has decided the meaning of.
	if claim.Cardinality != domain.CardinalityOne && claim.Cardinality != domain.CardinalityMany {
		return "", fmt.Errorf("claim for %q carries no cardinality, so whether it supersedes an "+
			"earlier fact is undecided; it comes from the vocabulary", claim.Predicate)
	}

	var factID string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		subjectSpeaker, objectSpeaker, err := bindSpeaker(ctx, tx, schema, scope, observationID, speaker, dataSubjectID, claim)
		if err != nil {
			return err
		}
		subjectID, err := resolveEntity(ctx, tx, schema, scope, observationID, claim.Subject, claim.SubjectType, dataSubjectID, subjectSpeaker)
		if err != nil {
			return err
		}
		objectID, err := resolveEntity(ctx, tx, schema, scope, observationID, claim.Object, claim.ObjectType, dataSubjectID, objectSpeaker)
		if err != nil {
			return err
		}

		id := uuid.New()
		validFrom := claim.ValidFrom
		if validFrom.IsZero() {
			validFrom = time.Now().UTC()
		}

		// Supersession, for the eleven relations that hold one value at a time.
		//
		// The exclusion constraint refuses two overlapping valid intervals for one subject, so this
		// is not an optimisation — without it the second `lives_in` for a person is an error rather
		// than a move. Closing the earlier interval at the new one's start is what a move IS: the
		// old fact stays, with an end, which is the history the product exists to keep.
		//
		// Both statements are in the same transaction as the insert, so a crash between them cannot
		// leave a subject with no current value. And the constraint is still the guarantee: if two
		// writers race past this close, one of their inserts is refused by the database rather than
		// both succeeding.
		if claim.Cardinality == domain.CardinalityOne && subjectID != "" {
			if _, err := tx.Exec(ctx, schema.SQL(supersedeSQL),
				scope, subjectID, claim.Predicate, validFrom); err != nil {
				return fmt.Errorf("supersede the earlier fact: %w", err)
			}
		}

		if _, err := tx.Exec(ctx, schema.SQL(insertFactSQL),
			id, scope, nullIfEmptyString(subjectID), nullIfEmptyString(objectID),
			claim.Predicate, claim.Statement, validFrom, claim.Confidence,
			nullIfEmpty(dataSubjectID), string(claim.Cardinality), string(speaker)); err != nil {
			return fmt.Errorf("insert fact: %w", err)
		}

		// Evidence names the MESSAGE, not the turn — which means the ordinal as well as the span. A
		// byte offset taken against another message of the same turn does not fail; it resolves to
		// a plausible fragment of the wrong sentence and looks entirely correct.
		if _, err := tx.Exec(ctx, schema.SQL(insertEvidenceSQL),
			id, observationID, claim.SourceOrdinal, claim.Quote, claim.ByteStart, claim.ByteEnd,
			ExtractorVersion, scope); err != nil {
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
	dataSubjectID string, isSpeaker bool) (string, error) {

	if isSpeaker {
		return resolveSpeaker(ctx, tx, schema, scope, observationID, dataSubjectID)
	}
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
ON CONFLICT (scope, normalized_name) WHERE identity_kind = 'named' DO UPDATE
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
     valid, confidence, data_subject_id, cardinality, source_role)
VALUES ($1, $2, $3::uuid, $4::uuid, $5, $6, tstzrange($7, NULL), $8, $9, $10, $11)`

// Closes whatever this subject currently holds for this relation, at the moment the new one starts.
//
// `upper_inf(valid)` rather than "contains now": the interval being closed is the OPEN one, and a
// fact that was already given an end is history that must not be rewritten. Ends are set to the new
// fact's start rather than to now(), so the two intervals meet exactly and a question about any
// instant has one answer.
//
// A range that would become empty or backwards — a fact arriving with a timestamp before the current
// one started — is left alone. Out-of-order arrival is real, and the right answer to it is a
// bitemporal correction rather than an interval this statement invents.
const supersedeSQL = `
UPDATE {schema}.fact
   SET valid = tstzrange(lower(valid), $4)
 WHERE scope = $1
   AND subject_entity_id = $2::uuid
   AND predicate = $3
   AND upper_inf(valid)
   AND lower(valid) < $4`

const insertEvidenceSQL = `
INSERT INTO {schema}.fact_evidence
    (fact_id, source_observation_id, source_ordinal, quote, byte_start, byte_end, extractor_version, scope)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

// ProjectionRejectedClaim is the projection kind a rejected claim registers as.
//
// It registers like anything else, because a refused claim still holds somebody's verbatim words. A
// table of "things we did not store" that turns out to store them is the worst version of this
// feature, and it is invisible to erasure unless it is here.
const ProjectionRejectedClaim = "rejected_claim"

// Reject records a proposal that did not become a fact.
//
// # Why the refusal is written down rather than counted
//
// Both refusals are ordinary — most messages assert nothing, and a model that paraphrases is
// behaving normally — so neither is an error and neither belongs in an error log. But both are
// claims about quality that nobody can check without the words: a rate of eleven percent cannot
// distinguish one missing relation asserted five hundred times, where the vocabulary is wrong and
// the fix is one row, from five hundred relations asserted once, which is the tail and is noise.
//
// # Why it is not part of Assert
//
// A rejected claim never reaches Assert: it has no admitted predicate, or no locatable span, so
// there is no fact to write and no entity to resolve. Folding it in would mean a function whose
// success case sometimes writes a fact and sometimes writes the reason it did not.
func (s *FactStore) Reject(ctx context.Context, schema Schema, scope, observationID, dataSubjectID string,
	rejected domain.RejectedClaim) error {

	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		id := uuid.New()
		if _, err := tx.Exec(ctx, schema.SQL(insertRejectedClaimSQL),
			id, scope, observationID, rejected.SourceOrdinal, rejected.Predicate,
			rejected.Statement, rejected.Quote, rejected.Reason, ExtractorVersion,
			nullIfEmpty(dataSubjectID)); err != nil {
			return fmt.Errorf("record rejected claim: %w", err)
		}
		// In the SAME transaction as the row it describes. A projection written now and registered
		// afterwards is invisible to erasure for as long as the gap lasts, and the gap is exactly
		// the window in which a process dies.
		if _, err := tx.Exec(ctx, schema.SQL(registerProjectionSQL),
			observationID, scope, ProjectionRejectedClaim, id.String(),
			nullIfEmpty(dataSubjectID)); err != nil {
			return fmt.Errorf("register rejected claim projection: %w", err)
		}
		return nil
	})
}

const insertRejectedClaimSQL = `
INSERT INTO {schema}.rejected_claim
    (rejected_claim_id, scope, source_observation_id, source_ordinal, predicate, statement, quote,
     reason, extractor_version, data_subject_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
