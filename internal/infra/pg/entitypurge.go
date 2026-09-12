// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EntityPurge is what a purge did, or what it would have done.
//
// # Why one type serves both
//
// The preview is the purge, rolled back. Counting what a delete would remove with one set of
// queries and removing it with another is two predicates wearing one name, and they diverge on the
// day somebody edits one — which is the day an operator approves a preview and gets something else.
// So the transaction runs in full either way and is committed only when the caller confirmed.
type EntityPurge struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"canonical_name"`
	Reason   string `json:"reason,omitempty"`
	// Previewed is true when nothing was committed: this is what a confirmed call would do.
	Previewed bool `json:"previewed"`
	// Removed is per table, so a reader can see that the words were not among them.
	Removed map[string]int `json:"removed"`
	// Residual counts, after the deletes and in the same transaction, what still refers to the
	// entity. Anything but zero is a purge that did not do what it claimed.
	Residual map[string]int `json:"residual"`
	// Subjects is how many distinct people contributed the claims being removed, and Anonymous is
	// how many of the claims came from turns attributed to nobody. An entity is shared by
	// construction, so a purge reaches other people's knowledge; the number is reported before the
	// caller confirms rather than discovered afterwards.
	Subjects  int `json:"contributing_subjects"`
	Anonymous int `json:"claims_from_unattributed_turns"`
	// Observations is how many source turns supported the removed claims. They are NOT removed: the
	// words a person said are not the system's inference about them, and an entity purged because
	// the extractor was wrong must not take the evidence of that error with it.
	Observations int       `json:"source_turns_kept"`
	CompletedAt  time.Time `json:"completed_at"`
}

// Clean reports whether nothing that should have gone survived.
func (p EntityPurge) Clean() bool {
	for _, n := range p.Residual {
		if n != 0 {
			return false
		}
	}
	return true
}

// PurgeEntity removes one entity and the claims that stand on it, and reports what that cost.
//
// # What it is for, and what it is not
//
// An entity is an inference: the extractor decided that these words name a thing and that thing is
// this node. When that inference is wrong — two people merged into one, a phrase read as a company
// — every claim anchored there is wrong with it, and no erasure reaches the problem, because
// erasure is about a person's data and this is about the system's own mistake. So this is the
// operation for withdrawing a node, and its receipt is shaped like an erasure's for the same reason:
// a count of what went and a count of what is still there, taken against the same predicate inside
// one transaction.
//
// It is not erasure and must never be offered as it. The turns stay, their messages stay, and a
// rebuild from those turns can propose the entity again — which is correct, because the words have
// not changed and the next extractor may be better. An operator who needs the words gone wants
// erasure by subject or by source.
//
// # The ledger row commits with the purge
//
// A confirmed purge writes its own ledger row, with the rows it removed as the magnitude, inside the
// transaction that removes them. Written after the commit, a failed ledger insert left a purge done
// and nowhere recorded — and a purge, unlike a read, is the operation an auditor asks about.
// Now the two commit together or not at all. A preview rolls back and keeps its row on the ordinary
// path, because nothing it did survives to be accounted for.
func (s *RecordStore) PurgeEntity(ctx context.Context, scope, id, reason string, confirm bool, record domain.AuditEntry) (EntityPurge, error) {
	if _, err := NewSchema(scope); err != nil {
		return EntityPurge{}, ErrEntityNotFound
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		// An unparseable id is not a 400 here: a caller who guesses identifiers learns nothing from
		// the difference between "not an id" and "not yours", and the entity routes already answer
		// both the same way.
		return EntityPurge{}, ErrEntityNotFound
	}
	entity := parsed.String()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EntityPurge{}, err
	}
	// Rolled back unless confirmed, which is what makes the preview the purge.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	out := EntityPurge{
		EntityID: entity, Reason: reason, Previewed: !confirm,
		Removed: map[string]int{}, Residual: map[string]int{},
	}
	if err := tx.QueryRow(ctx, s.schema.SQL(
		`SELECT left(canonical_name,4096) FROM {schema}.entity WHERE scope=$1 AND entity_id=$2::uuid FOR UPDATE`),
		scope, entity).Scan(&out.Name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EntityPurge{}, ErrEntityNotFound
		}
		return EntityPurge{}, fmt.Errorf("read the entity: %w", err)
	}

	// Who loses something, counted before anything goes. A turn attributed to nobody is counted
	// separately rather than as a person, because a document is not a data subject.
	const claimants = `
		SELECT count(DISTINCT o.data_subject_id) FILTER (WHERE o.data_subject_id IS NOT NULL),
		       count(*) FILTER (WHERE o.data_subject_id IS NULL),
		       count(DISTINCT o.observation_id)
		  FROM {schema}.fact f
		  JOIN {schema}.fact_evidence e ON e.scope=f.scope AND e.fact_id=f.fact_id
		  JOIN {schema}.observation o ON o.scope=e.scope AND o.observation_id=e.source_observation_id
		 WHERE f.scope=$1 AND (f.subject_entity_id=$2::uuid OR f.object_entity_id=$2::uuid)`
	if err := tx.QueryRow(ctx, s.schema.SQL(claimants), scope, entity).
		Scan(&out.Subjects, &out.Anonymous, &out.Observations); err != nil {
		return EntityPurge{}, fmt.Errorf("count who is affected: %w", err)
	}

	// Order matters. The facts' foreign keys to the entity are deferred but not cascading, so the
	// facts go first or the commit refuses; evidence is counted before its facts cascade it away.
	for _, step := range []struct{ table, sql string }{
		// A receipt carries the entity its fact named, and recovery rebuilds a fact from its receipt
		// — inserting that entity back before it checks anything. Left behind, the receipts of the
		// withdrawn claims are a standing instruction to undo this purge on the next recovery pass.
		// Matched on the identity the receipt stored, not on the fact rows, which are about to go and
		// which a half-finished earlier purge may already have taken. fact_receipt_history follows by
		// cascade. The words are untouched: a rebuild from the turns may propose the node again, and
		// that is a fresh inference rather than this one restored.
		{"fact_receipt", `DELETE FROM {schema}.fact_receipt WHERE scope=$1
		      AND (subject_identity->>'entity_id'=$2 OR object_identity->>'entity_id'=$2)`},
		{"fact_evidence", `DELETE FROM {schema}.fact_evidence e USING {schema}.fact f
		    WHERE f.scope=e.scope AND f.fact_id=e.fact_id AND f.scope=$1
		      AND (f.subject_entity_id=$2::uuid OR f.object_entity_id=$2::uuid)`},
		{"fact", `DELETE FROM {schema}.fact WHERE scope=$1
		      AND (subject_entity_id=$2::uuid OR object_entity_id=$2::uuid)`},
		{"entity_name_receipt", `DELETE FROM {schema}.entity_name_receipt WHERE scope=$1 AND entity_id=$2::uuid`},
		{"entity", `DELETE FROM {schema}.entity WHERE scope=$1 AND entity_id=$2::uuid`},
	} {
		tag, err := tx.Exec(ctx, s.schema.SQL(step.sql), scope, entity)
		if err != nil {
			return EntityPurge{}, fmt.Errorf("purge %s: %w", step.table, err)
		}
		out.Removed[step.table] = int(tag.RowsAffected())
	}

	// Counted after the deletes and before the commit, against the same predicates.
	for _, check := range []struct{ table, sql string }{
		{"fact_receipt", `SELECT count(*) FROM {schema}.fact_receipt WHERE scope=$1
		      AND (subject_identity->>'entity_id'=$2 OR object_identity->>'entity_id'=$2)`},
		{"fact", `SELECT count(*) FROM {schema}.fact WHERE scope=$1
		      AND (subject_entity_id=$2::uuid OR object_entity_id=$2::uuid)`},
		{"entity", `SELECT count(*) FROM {schema}.entity WHERE scope=$1 AND entity_id=$2::uuid`},
		{"entity_name_receipt", `SELECT count(*) FROM {schema}.entity_name_receipt WHERE scope=$1 AND entity_id=$2::uuid`},
		{"projection_dependency", `SELECT count(*) FROM {schema}.projection_dependency
		     WHERE scope=$1 AND projection_kind='entity' AND projection_id=$2`},
	} {
		var n int
		if err := tx.QueryRow(ctx, s.schema.SQL(check.sql), scope, entity).Scan(&n); err != nil {
			return EntityPurge{}, fmt.Errorf("count residual %s: %w", check.table, err)
		}
		out.Residual[check.table] = n
	}

	out.CompletedAt = time.Now().UTC()
	if confirm {
		record.Magnitude = 0
		for _, n := range out.Removed {
			record.Magnitude += n
		}
		if err := record.Validate(); err != nil {
			return EntityPurge{}, fmt.Errorf("the purge's ledger row: %w", err)
		}
		if _, err := tx.Exec(ctx, s.schema.SQL(`
			INSERT INTO {schema}.audit_entry (operation, principal, principal_kind, project, magnitude, outcome)
			VALUES ($1, $2, $3, $4, $5, $6)`),
			record.Operation, record.Principal, record.PrincipalKind, record.Project, record.Magnitude, record.Outcome); err != nil {
			return EntityPurge{}, fmt.Errorf("record the purge on the ledger: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return EntityPurge{}, fmt.Errorf("commit the purge: %w", err)
		}
	}
	return out, nil
}
