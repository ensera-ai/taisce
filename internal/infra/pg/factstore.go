// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
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

// ExtractorVersion is the code label for direct callers with no configured model identity. Runtime
// formation supplies a configuration fingerprint instead; this label does not identify model weights.
const ExtractorVersion = "extract/v1"

// ErrNotSpokenByPrincipal is returned when a claim would become a fact about the principal but the
// message it came from was not the principal speaking.
var ErrNotSpokenByPrincipal = errors.New("claim is not the principal's to make")

// ErrUnboundSpeaker refuses a relative reference without matching stored user attribution.
var ErrUnboundSpeaker = errors.New("speaker reference needs the observation's attributed user")

// ErrConflictingValue is returned when a claim would be a second current value for a single-cardinality
// relation whose existing value it cannot supersede, because both start at the same instant. The
// exclusion constraint is what refuses it; this names the refusal so a caller can record it as one
// rather than retry it. The database error stays in the chain, so the constraint name is readable.
var ErrConflictingValue = errors.New("a second current value for a single-cardinality relation")

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

	return s.AssertVersioned(ctx, schema, scope, observationID, speaker, dataSubjectID, claim, ExtractorVersion)
}

// AssertVersioned preserves the configured extraction identity on new evidence. Retained records
// and source receipts keep their original stamps; changing a source's pinned identity is refused.
func (s *FactStore) AssertVersioned(ctx context.Context, schema Schema, scope, observationID string,
	speaker domain.Role, dataSubjectID string, claim domain.Claim, version string) (string, error) {

	var factID string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		factID, err = assertFactTx(ctx, tx, schema, scope, observationID, speaker, dataSubjectID, claim, version, time.Time{})
		return err
	})
	return factID, err
}

// Human corrections share the same source, role and temporal write boundary; only provenance differs.
func assertFactTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, observationID string,
	speaker domain.Role, dataSubjectID string, claim domain.Claim, extractorVersion string, knownAt time.Time) (string, error) {
	return assertFactVersionTx(ctx, tx, schema, scope, observationID, speaker, dataSubjectID, claim, extractorVersion, knownAt, uuid.Nil)
}

// A generation uses the same attribution, withdrawal, entity and evidence boundary as an ordinary
// assertion. Only its identity and temporal reconciliation differ; its caller owns atomic cutover.
func assertFactVersionTx(ctx context.Context, tx pgx.Tx, schema Schema, scope, observationID string,
	speaker domain.Role, dataSubjectID string, claim domain.Claim, extractorVersion string, knownAt time.Time, generation uuid.UUID) (string, error) {

	// The role gate is on the speaker reference, not on the role alone: bindSpeaker refuses a
	// first-person claim from a message the principal did not speak, and admits the same message's
	// claims about named third parties under its own role. A claim that speaks for the principal
	// is the only thing a non-user message must never write; everything else it says is its own,
	// labelled with its role, and recall selects user statements by default.
	// Refused here rather than by the foreign key, which would also refuse it — as a constraint
	// violation from four frames down, naming a column instead of the mistake. Cardinality decides
	// whether this assertion supersedes an earlier fact or sits beside it, so a claim that does not
	// carry one is a claim nobody has decided the meaning of.
	if claim.Cardinality != domain.CardinalityOne && claim.Cardinality != domain.CardinalityMany {
		return "", fmt.Errorf("claim for %q carries no cardinality, so whether it supersedes an "+
			"earlier fact is undecided; it comes from the vocabulary", claim.Predicate)
	}

	var factID string
	err := func() error {
		if err := lockClaimSource(ctx, tx, schema, scope, observationID); err != nil {
			return err
		}
		subjectSpeaker, objectSpeaker, err := bindSpeaker(ctx, tx, schema, scope, observationID, speaker, dataSubjectID, claim)
		if err != nil {
			return err
		}
		if generation == uuid.Nil {
			if err := pinLockedExtractionTx(ctx, tx, schema, scope, observationID, extractorVersion); err != nil {
				return err
			}
		}
		if err := refuseRetractedClaim(ctx, tx, schema, scope, observationID, dataSubjectID, claim, subjectSpeaker, objectSpeaker); err != nil {
			return err
		}
		if generation != uuid.Nil {
			if err := refuseCorrectedGenerationSlotTx(ctx, tx, schema, scope, observationID, dataSubjectID, claim, subjectSpeaker); err != nil {
				return err
			}
		}
		id, err := sourceClaimID(scope, observationID, dataSubjectID, claim, subjectSpeaker, objectSpeaker)
		if err != nil {
			return err
		}
		if generation != uuid.Nil {
			id = uuid.NewHash(sha256.New(), generation, id[:], 8)
		}
		var retained bool
		if err := tx.QueryRow(ctx, schema.SQL(`SELECT EXISTS(SELECT 1 FROM {schema}.fact WHERE scope=$1 AND fact_id=$2)`), scope, id).Scan(&retained); err != nil {
			return err
		}
		if retained {
			factID = id.String()
			return nil
		}
		restored, err := restoreFactReceiptTx(ctx, tx, schema, scope, id.String())
		if err != nil {
			return err
		}
		if restored {
			factID = id.String()
			return nil
		}
		subjectID, err := resolveEntity(ctx, tx, schema, scope, observationID, claim.Subject, claim.SubjectType, dataSubjectID, subjectSpeaker)
		if err != nil {
			return err
		}
		objectID, err := resolveEntity(ctx, tx, schema, scope, observationID, claim.Object, claim.ObjectType, dataSubjectID, objectSpeaker)
		if err != nil {
			return err
		}

		validFrom := claim.ValidFrom
		if validFrom.IsZero() {
			validFrom = time.Now().UTC()
		}

		// Stamp knowledge after entity upserts acquire their row locks. A transaction that waited
		// behind another assertion must not use its earlier transaction-start timestamp.
		learnedAt := knownAt
		if learnedAt.IsZero() {
			query := `SELECT clock_timestamp()`
			var args []any
			if claim.Cardinality == domain.CardinalityOne && subjectID != "" {
				// Wall time can move backwards. The subject row lock serializes these transitions;
				// each successor must start strictly after the knowledge it archives, even then.
				// And after knowledge already closed: a retracted fact in this slot whose validity
				// overlaps keeps the end the retraction stamped, and a clock that stepped back since
				// would open this fact before it — overlapping in both valid and known time, which
				// the exclusion constraint refuses as a conflicting value.
				query = schema.SQL(`SELECT greatest(clock_timestamp(),
                    max(lower(known)+interval '1 microsecond') FILTER (WHERE upper_inf(valid) AND upper_inf(known) AND lower(valid)<$4),
                    max(upper(known)) FILTER (WHERE NOT upper_inf(known) AND valid && tstzrange($4,NULL)))
                    FROM {schema}.fact WHERE scope=$1 AND subject_entity_id=$2::uuid AND predicate=$3`)
				args = []any{scope, subjectID, claim.Predicate, validFrom}
			}
			if err := tx.QueryRow(ctx, query, args...).Scan(&learnedAt); err != nil {
				return fmt.Errorf("stamp fact knowledge: %w", err)
			}
		}
		// Save the previous validity at its original knowledge interval before changing the
		// retained row. The snapshot, causal source, registration and new assertion commit together.
		var boundary generationBoundary
		if generation != uuid.Nil && claim.Cardinality == domain.CardinalityOne && subjectID != "" {
			var err error
			boundary, err = generationValidityTx(ctx, tx, schema, scope, observationID, generation.String(), subjectID, claim.Predicate, id.String(), validFrom)
			if err != nil {
				return err
			}
		}
		if generation == uuid.Nil && claim.Cardinality == domain.CardinalityOne && subjectID != "" {
			if _, err := tx.Exec(ctx, schema.SQL(supersedeSQL),
				scope, subjectID, claim.Predicate, validFrom, learnedAt, observationID, id, nullIfEmpty(dataSubjectID)); err != nil {
				return fmt.Errorf("supersede the earlier fact: %w", err)
			}
		}

		if _, err := tx.Exec(ctx, schema.SQL(insertFactSQL),
			id, scope, nullIfEmptyString(subjectID), nullIfEmptyString(objectID),
			claim.Predicate, claim.Statement, validFrom, claim.Confidence,
			nullIfEmpty(dataSubjectID), string(claim.Cardinality), string(speaker), learnedAt, boundary.Until, boundary.Successor, boundary.Source); err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) && databaseError.Code == "23P01" && databaseError.ConstraintName == "fact_single_cardinality_excl" {
				return fmt.Errorf("insert fact: %w: %w", ErrConflictingValue, err)
			}
			return fmt.Errorf("insert fact: %w", err)
		}

		// Evidence names the MESSAGE, not the turn — which means the ordinal as well as the span. A
		// byte offset taken against another message of the same turn does not fail; it resolves to
		// a plausible fragment of the wrong sentence and looks entirely correct.
		if _, err := tx.Exec(ctx, schema.SQL(insertEvidenceSQL),
			id, observationID, claim.SourceOrdinal, claim.Quote, claim.ByteStart, claim.ByteEnd,
			extractorVersion, scope); err != nil {
			return fmt.Errorf("insert evidence: %w", err)
		}

		if _, err := tx.Exec(ctx, schema.SQL(registerProjectionSQL),
			observationID, scope, ProjectionFact, id.String(), nullIfEmpty(dataSubjectID)); err != nil {
			return fmt.Errorf("register fact projection: %w", err)
		}

		// The fact inherits the deadline of the turns behind it, so a read can refuse it with a
		// column test instead of walking to its evidence (0068). Run after the evidence row,
		// and again whenever another observation comes to support the same fact, because a fact
		// survives while any of its supporters does.
		if _, err := tx.Exec(ctx, schema.SQL(stampFactRetentionSQL), id, scope); err != nil {
			return fmt.Errorf("stamp the fact's retention: %w", err)
		}

		if _, err := tx.Exec(ctx, schema.SQL(retainFactReceiptSQL), scope, id); err != nil {
			return fmt.Errorf("retain formation receipt: %w", err)
		}

		factID = id.String()
		return nil
	}()
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
	if len(name) > 4096 || len(normalized) > 4096 {
		return "", ErrEntityNameLimit
	}
	if normalized == "" {
		// A blank end is not an entity. Empty rather than an error: the fact is still worth
		// storing, it simply has an unresolvable end, and traversal skips it.
		return "", nil
	}
	if entityType == "" {
		entityType = "thing"
	}
	var retainedSource string
	if err := tx.QueryRow(ctx, schema.SQL(`SELECT observation_id::text FROM {schema}.observation WHERE scope=$1 AND observation_id=$2::uuid FOR KEY SHARE`), scope, observationID).Scan(&retainedSource); err != nil {
		return "", err
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
	if err := retainEntityNameTx(ctx, tx, schema, scope, observationID, id, name, normalized); err != nil {
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

// `known` defaults to [now, ∞): we believe it from the moment it is recorded. `valid` opens at the
// claim's own time, because when something became true and when we heard about it are different
// questions and the whole point of two axes is to answer both.
const insertFactSQL = `
INSERT INTO {schema}.fact
    (fact_id, scope, subject_entity_id, object_entity_id, predicate, statement,
     valid, confidence, data_subject_id, cardinality, source_role, known, recorded_at, superseded_by, supersession_source)
VALUES ($1, $2, $3::uuid, $4::uuid, $5, $6, tstzrange($7, $13), $8, $9, $10, $11, tstzrange($12,NULL), $12, $14::uuid, $15::uuid)`

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
WITH previous AS MATERIALIZED (
 SELECT fact_id,scope,valid,known FROM {schema}.fact
 WHERE scope=$1 AND subject_entity_id=$2::uuid AND predicate=$3
   AND upper_inf(valid) AND upper_inf(known) AND lower(valid)<$4
 FOR UPDATE
), saved AS (
 INSERT INTO {schema}.fact_history(history_id,scope,fact_id,valid,known,source_observation_id)
 SELECT gen_random_uuid(),p.scope,p.fact_id,p.valid,
   tstzrange(lower(p.known),$5),$6::uuid FROM previous p
 RETURNING history_id,fact_id
), registered AS (
 INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id)
 SELECT d.source_observation_id,d.scope,'fact_history',s.history_id::text,d.data_subject_id
 FROM saved s JOIN {schema}.projection_dependency d ON d.projection_kind='fact' AND d.projection_id=s.fact_id::text AND d.scope=$1
 UNION
 SELECT $6::uuid,$1,'fact_history',s.history_id::text,$8::text FROM saved s
 ON CONFLICT DO NOTHING
)
UPDATE {schema}.fact f SET valid=tstzrange(lower(f.valid),$4),known=tstzrange($5,NULL),
 superseded_by=$7::uuid,supersession_source=$6::uuid
 WHERE f.fact_id IN (SELECT fact_id FROM saved)`

// The latest deadline among the observations supporting this fact, or none when any of them is
// kept indefinitely: a fact is reachable while anything behind it is.
const stampFactRetentionSQL = `
UPDATE {schema}.fact f
   SET retention_until = s.deadline
  FROM (
    SELECT CASE WHEN bool_or(o.retention_until IS NULL) THEN NULL ELSE max(o.retention_until) END AS deadline
      FROM {schema}.fact_evidence e
      JOIN {schema}.observation o ON o.observation_id = e.source_observation_id AND o.scope = e.scope
     WHERE e.scope = $2 AND e.fact_id = $1
  ) s
 WHERE f.scope = $2 AND f.fact_id = $1
   AND f.retention_until IS DISTINCT FROM s.deadline`

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

	return s.RejectVersioned(ctx, schema, scope, observationID, dataSubjectID, rejected, ExtractorVersion)
}

// Rejected text belongs to the extraction configuration that proposed it, including stable retries.
func (s *FactStore) RejectVersioned(ctx context.Context, schema Schema, scope, observationID, dataSubjectID string,
	rejected domain.RejectedClaim, version string) error {

	id, err := rejectedIdentity(scope, observationID, rejected, version)
	if err != nil {
		return fmt.Errorf("identify rejected source: %w", err)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := pinExtractionTx(ctx, tx, schema, scope, observationID, version); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, schema.SQL(insertRejectedClaimSQL),
			id, scope, observationID, rejected.SourceOrdinal, rejected.Predicate,
			rejected.Statement, rejected.Quote, rejected.Reason, version,
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
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (rejected_claim_id) DO NOTHING`
