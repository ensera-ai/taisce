// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
)

// Eraser deletes everything derived from a data subject's observations — or from a named set of
// source observations — and counts what survived.
type Eraser struct{ pool *pgxpool.Pool }

// NewEraser builds an eraser.
func NewEraser(pool *pgxpool.Pool) *Eraser { return &Eraser{pool: pool} }

// ErasureReceipt is one erasure as the ledger of erasures holds it: what was asked, when, and the
// counted residual once it completed. The selector names a subject or a source, never content.
type ErasureReceipt struct {
	RequestID   string          `json:"id"`
	Scope       string          `json:"project"`
	Selector    json.RawMessage `json:"selector"`
	Reason      string          `json:"reason"`
	RequestedAt time.Time       `json:"requested_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	Residual    json.RawMessage `json:"residual,omitempty"`
}

// Receipts lists one project's erasures, newest first.
func (e *Eraser) Receipts(ctx context.Context, schema Schema, scope string, limit int) ([]ErasureReceipt, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := e.pool.Query(ctx, schema.SQL(`
		SELECT request_id::text, scope, selector, reason, requested_at, completed_at, residual
		  FROM {schema}.erasure_request WHERE scope=$1 ORDER BY requested_at DESC, request_id LIMIT $2`), scope, limit)
	if err != nil {
		return nil, fmt.Errorf("list erasures: %w", err)
	}
	defer rows.Close()
	out := []ErasureReceipt{}
	for rows.Next() {
		var r ErasureReceipt
		if err := rows.Scan(&r.RequestID, &r.Scope, &r.Selector, &r.Reason, &r.RequestedAt, &r.CompletedAt, &r.Residual); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// projectionKind is a declared kind and the table it lives in, read from `projection_kind`.
type projectionKind struct {
	kind  string
	table string
	idCol string
	// survivesSharing is whether a projection of this kind is kept when another data subject also
	// registered it. True for a shared identity — an entity means the same thing to everybody who
	// registered it. False for anything holding text drawn from several people, where keeping it
	// because somebody else also contributed leaves the departing subject's words in a row the
	// erasure counted as retained.
	survivesSharing bool
}

// sourceSelection names what an erasure removes: one data subject, or a set of source observations.
//
// Both walk the same path and count against the same predicates, because the receipt is the product
// and two walks would be two receipts with different meanings under one name. What differs is only
// how an observation is picked — by the person it is attributed to, or by its own identity — and
// that is rendered here, once, so every statement in the walk takes (scope, selector) and nothing
// else. A source selector never touches the data-subject row: the person's other turns survive, so
// the person does.
// sourceSelection is how a governance walk names what it is for: one person, or a list of turns.
// Erasure and export both take it, so the two walks cannot disagree about what "this person's data"
// means — an export that reached rows an erasure would miss is the failure a receipt exists to rule
// out.
type sourceSelection struct {
	subject string
	sources []string
}

func (s sourceSelection) bySource() bool { return len(s.sources) > 0 }

// arg is what every statement binds as $2.
func (s sourceSelection) arg() any {
	if s.bySource() {
		return s.sources
	}
	return s.subject
}

// observations is the predicate that picks the selected observations, on an observation aliased by
// prefix ("o." or "").
func (s sourceSelection) observations(prefix string) string {
	if s.bySource() {
		return prefix + "observation_id = ANY($2::uuid[])"
	}
	return prefix + "data_subject_id = $2"
}

// subjects is the subquery of the people the selection covers, for tables keyed by subject rather
// than by source. A subject selection is that person; a source selection is whoever the selected
// turns were about, which is how a document's export reaches the subject rows it registered.
func (s sourceSelection) subjects() string {
	if s.bySource() {
		return "SELECT DISTINCT data_subject_id FROM {schema}.observation WHERE scope=$1 AND " +
			s.observations("") + " AND data_subject_id IS NOT NULL"
	}
	return "SELECT $2::text"
}

// owned is the subquery of selected observation ids, for tables that point at a source.
func (s sourceSelection) owned() string {
	return "SELECT observation_id FROM {schema}.observation WHERE scope=$1 AND " + s.observations("")
}

// registrations picks the projection registrations the selection made, on `projection_dependency d`.
func (s sourceSelection) registrations() string {
	if s.bySource() {
		return "d.source_observation_id = ANY($2::uuid[])"
	}
	return "d.data_subject_id = $2"
}

// foreign is a registration NOT made by the selection — the one that keeps a shared projection alive.
func (s sourceSelection) foreign() string {
	if s.bySource() {
		return "NOT (other.source_observation_id = ANY($2::uuid[]))"
	}
	return "other.data_subject_id IS DISTINCT FROM $2"
}

// record is the selector as the receipt keeps it: a subject or a list of sources, never content.
func (s sourceSelection) record() ([]byte, error) {
	if s.bySource() {
		return json.Marshal(map[string]any{"source_observation_ids": s.sources})
	}
	return json.Marshal(map[string]any{"data_subject_id": s.subject})
}

// Erase removes a data subject from a project and returns the receipt.
//
// # Everything in one transaction, including the count
//
// The residual is counted after the deletes and before the commit. Counted afterwards it would be a
// separate read of a moving target: a concurrent formation writing a new projection for the same
// subject would show up as a residual the erasure did not leave, or worse, a concurrent erasure of
// the same subject would make each of them report the other's work as their own clean sweep.
//
// # The order matters and is not obvious
//
// The projections are deleted through their registrations, then counted through the SAME
// registrations, and only then are the observations deleted. Deleting the observations first would
// cascade the registrations away, and the count would then be against a predicate that selects
// nothing — a residual of zero that means "I could not find anything to check" rather than "nothing
// survived".
//
// # What is deliberately NOT deleted
//
// A projection registered to an observation belonging to somebody else as well is kept. An entity is
// the case that matters: `Ensera` is registered by every subject who mentioned it, and removing it
// because one of them left would take a node out of everybody else's graph. So the predicate deletes
// only projections whose every registration belongs to this subject — and the residual is counted
// with that same predicate, so a shared entity surviving is correctly not a residual.
func (e *Eraser) Erase(ctx context.Context, schema Schema, scope, dataSubjectID, reason string) (domain.Erasure, error) {
	if dataSubjectID == "" {
		// An erasure with no subject would match every row whose data_subject_id is NULL, which is
		// every projection that was never about a person. There is no plausible caller for that and
		// one very implausible outcome.
		return domain.Erasure{}, fmt.Errorf("an erasure needs a data subject")
	}
	return e.erase(ctx, schema, scope, sourceSelection{subject: dataSubjectID}, reason)
}

// EraseSources removes a set of observations by their own identity — a document that was observed
// project-wide, which no subject erasure can reach — and returns the same receipt.
//
// The walk is the subject walk with the observations picked by id, so the receipt means the same
// thing: every projection registered only to these sources is gone, a projection another source
// also registered is kept, and the residual is counted against the same predicate. An id from
// another project matches nothing here: the predicate is scoped before it is keyed, so the receipt
// reports zero deleted rather than reaching across.
func (e *Eraser) EraseSources(ctx context.Context, schema Schema, scope string, sources []string, reason string) (domain.Erasure, error) {
	if len(sources) == 0 {
		return domain.Erasure{}, fmt.Errorf("an erasure by source names at least one observation")
	}
	for _, id := range sources {
		if _, err := uuid.Parse(id); err != nil {
			return domain.Erasure{}, fmt.Errorf("an erasure by source names observations by id: %q is not one", id)
		}
	}
	return e.erase(ctx, schema, scope, sourceSelection{sources: sources}, reason)
}

func (e *Eraser) erase(ctx context.Context, schema Schema, scope string, sel sourceSelection, reason string) (domain.Erasure, error) {
	out := domain.Erasure{
		RequestID:            uuid.New().String(),
		Scope:                scope,
		DataSubjectID:        sel.subject,
		SourceObservationIDs: sel.sources,
		Reason:               reason,
		Deleted:              map[string]int{},
		Residual:             map[string]int{},
	}
	selector, err := sel.record()
	if err != nil {
		return domain.Erasure{}, err
	}
	arg := sel.arg()

	err = pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		// Serialize registered identity updates and source acceptance with this erasure. A later
		// newly accepted source is new collection, not a retry of the erased source.
		if !sel.bySource() {
			if _, err := tx.Exec(ctx, schema.SQL(`SELECT 1 FROM {schema}.data_subject WHERE scope=$1 AND subject_id=$2 FOR UPDATE`), scope, arg); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, schema.SQL(`SELECT 1 FROM {schema}.observation WHERE scope=$1 AND `+sel.observations("")+` ORDER BY observation_id FOR UPDATE`), scope, arg); err != nil {
			return err
		}

		kinds, err := declaredKinds(ctx, tx, schema)
		if err != nil {
			return err
		}
		if len(kinds) == 0 {
			// Nothing declared means nothing would be deleted and the residual would be an empty
			// object — a receipt that proves nothing while looking like a clean sweep.
			return fmt.Errorf("no projection kinds are declared, so an erasure would assert nothing")
		}

		if _, err := tx.Exec(ctx, schema.SQL(openErasureSQL),
			out.RequestID, scope, selector, reason); err != nil {
			return fmt.Errorf("open erasure request: %w", err)
		}

		if err := eraseGenerationMetadataTx(ctx, tx, schema, scope, sel.owned(), arg, &out); err != nil {
			return err
		}
		for _, k := range kinds {
			tag, err := tx.Exec(ctx, deleteProjectionSQL(schema, k, sel), scope, arg)
			if err != nil {
				return fmt.Errorf("delete %s: %w", k.kind, err)
			}
			out.Deleted[k.kind] = int(tag.RowsAffected())

			var residual int
			if err := tx.QueryRow(ctx, countResidualSQL(schema, k, sel), scope, arg).Scan(&residual); err != nil {
				return fmt.Errorf("count residual %s: %w", k.kind, err)
			}
			out.Residual[k.kind] = residual
		}

		// Registered aggregate projections must be deleted and counted before changing their
		// source-owned name inputs. Otherwise the invalidation trigger removes the same vector
		// first and the erasure receipt understates what this request deleted.
		names, err := removeEntityNamesTx(ctx, tx, schema, scope, sel.subject, sel.sources)
		if err != nil {
			return err
		}
		out.Deleted["entity_name_receipt"] = names
		var nameResidual int
		if err := tx.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.entity_name_receipt r JOIN {schema}.observation o
            ON o.scope=r.scope AND o.observation_id=r.source_observation_id WHERE o.scope=$1 AND `+sel.observations("o.")), scope, arg).Scan(&nameResidual); err != nil {
			return err
		}
		out.Residual["entity_name_receipt"] = nameResidual

		// A guard, and it is meant to catch nothing.
		//
		// This used to say a shared fact survives on another observation's support, and that its
		// erased source's quote must not survive with it. A fact cannot be shared that way: its
		// identity hashes the source observation and the ordinal it came from (sourceClaimID), so
		// two turns saying the same thing are two facts, and both recovery paths refuse a receipt
		// whose source does not match the fact. Every evidence row therefore names its own fact's
		// turn, and fact_evidence cascades from the fact.
		//
		// So anything this removes escaped that cascade — a quote whose fact went by some path
		// around it. That is worth seeing rather than discarding: an erasure that quietly removed
		// words nobody counted is the failure this receipt exists to prevent. It is counted, and
		// it appears in the receipt only when it found something, because a key that is always
		// zero teaches a reader to skip it.
		orphaned, err := tx.Exec(ctx, schema.SQL(`DELETE FROM {schema}.fact_evidence e USING {schema}.observation o
            WHERE o.observation_id=e.source_observation_id AND o.scope=$1 AND `+sel.observations("o.")), scope, arg)
		if err != nil {
			return fmt.Errorf("erase source evidence: %w", err)
		}
		if escaped := int(orphaned.RowsAffected()); escaped > 0 {
			out.Deleted["fact_evidence_orphaned"] = escaped
		}
		for _, table := range []struct{ name, alias string }{{"record_retraction", "r"}, {"curated_claim", "c"}} {
			tag, err := tx.Exec(ctx, schema.SQL(`DELETE FROM {schema}.`+table.name+` `+table.alias+` USING {schema}.observation o
            WHERE `+table.alias+`.scope=o.scope AND `+table.alias+`.source_observation_id=o.observation_id AND o.scope=$1 AND `+sel.observations("o.")), scope, arg)
			if err != nil {
				return err
			}
			out.Deleted[table.name] = int(tag.RowsAffected())
			var residual int
			if err := tx.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.`+table.name+` `+table.alias+` JOIN {schema}.observation o
            ON `+table.alias+`.scope=o.scope AND `+table.alias+`.source_observation_id=o.observation_id WHERE o.scope=$1 AND `+sel.observations("o.")), scope, arg).Scan(&residual); err != nil {
				return err
			}
			out.Residual[table.name] = residual
		}

		// Count source-owned outcomes before cascading their parent rows. A transition is owned
		// by both the original source and the later causal source, like fact history itself.
		for _, table := range []string{"fact_receipt_history", "fact_receipt"} {
			predicate := receiptOwnershipSQLFor(table, sel.owned())
			tag, err := tx.Exec(ctx, schema.SQL("DELETE FROM {schema}."+table+" r WHERE "+predicate), scope, arg)
			if err != nil {
				return err
			}
			out.Deleted[table] = int(tag.RowsAffected())
			var remaining int
			if err := tx.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+table+" r WHERE "+predicate), scope, arg).Scan(&remaining); err != nil {
				return err
			}
			out.Residual[table] = remaining
		}

		pins, err := tx.Exec(ctx, schema.SQL(`DELETE FROM {schema}.source_extraction r USING {schema}.observation o
            WHERE o.scope=r.scope AND o.observation_id=r.source_observation_id AND o.scope=$1 AND `+sel.observations("o.")), scope, arg)
		if err != nil {
			return err
		}
		out.Deleted["source_extraction"] = int(pins.RowsAffected())
		var pinsResidual int
		if err := tx.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.source_extraction r JOIN {schema}.observation o
            ON o.scope=r.scope AND o.observation_id=r.source_observation_id WHERE o.scope=$1 AND `+sel.observations("o.")), scope, arg).Scan(&pinsResidual); err != nil {
			return err
		}
		out.Residual["source_extraction"] = pinsResidual

		// The person's own words, and the quotes behind their facts. Both go by cascade — messages
		// from the observation, evidence from the fact — so neither was ever named, and a receipt
		// that counts three observations while saying nothing about the messages inside them
		// understates the one thing a reader most wants counted. Counted here, before
		// the roots below take them.
		var messages int
		if err := tx.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.turn_message m
            JOIN {schema}.observation o ON o.observation_id=m.observation_id
            WHERE o.scope=$1 AND `+sel.observations("o.")), scope, arg).Scan(&messages); err != nil {
			return err
		}
		// Named as the export names it, because the two are meant to be read side by side: the
		// export's "message" section and this count answer the same question.
		out.Deleted["message"] = messages
		var quotes int
		if err := tx.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact_evidence e
            JOIN {schema}.observation o ON o.observation_id=e.source_observation_id AND o.scope=e.scope
            WHERE o.scope=$1 AND `+sel.observations("o.")), scope, arg).Scan(&quotes); err != nil {
			return err
		}
		out.Deleted["fact_evidence"] = quotes

		// Last, because the cascade takes the registrations with it and the count above needs them.
		// The observation is the root: after this, the turn's messages are gone too, and a rebuild
		// derives only from what survived rather than repairing what did not.
		observations, err := tx.Exec(ctx, schema.SQL(`DELETE FROM {schema}.observation WHERE scope = $1 AND `+sel.observations("")), scope, arg)
		if err != nil {
			return fmt.Errorf("delete observations: %w", err)
		}
		out.Deleted["observation"] = int(observations.RowsAffected())
		if !sel.bySource() {
			subjects, err := tx.Exec(ctx, schema.SQL(`DELETE FROM {schema}.data_subject WHERE scope=$1 AND subject_id=$2`), scope, arg)
			if err != nil {
				return err
			}
			out.Deleted["data_subject"] = int(subjects.RowsAffected())
			var remainingSubjects int
			if err := tx.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.data_subject WHERE scope=$1 AND subject_id=$2`), scope, arg).Scan(&remainingSubjects); err != nil {
				return err
			}
			out.Residual["data_subject"] = remainingSubjects
		}

		// The same predicate after the deletes, as every other count here: a residual of zero is what
		// says the cascade actually took them.
		for table, join := range map[string]string{
			"message":       `{schema}.turn_message m JOIN {schema}.observation o ON o.observation_id=m.observation_id`,
			"fact_evidence": `{schema}.fact_evidence e JOIN {schema}.observation o ON o.observation_id=e.source_observation_id AND o.scope=e.scope`,
		} {
			var remaining int
			if err := tx.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM `+join+`
                WHERE o.scope=$1 AND `+sel.observations("o.")), scope, arg).Scan(&remaining); err != nil {
				return err
			}
			out.Residual[table] = remaining
		}

		residual, err := json.Marshal(out.Residual)
		if err != nil {
			return err
		}
		out.CompletedAt = time.Now().UTC()
		if _, err := tx.Exec(ctx, schema.SQL(closeErasureSQL),
			out.RequestID, out.CompletedAt, residual); err != nil {
			return fmt.Errorf("close erasure request: %w", err)
		}
		return nil
	})
	return out, err
}

// declaredKinds reads the projection registry.
//
// Read per erasure rather than cached, because it is four rows and because a cache would be a copy
// of the one thing that must not be stale: a kind added by a migration and missing from an eraser's
// memory is exactly the silent gap this table exists to close.
func declaredKinds(ctx context.Context, tx pgx.Tx, schema Schema) ([]projectionKind, error) {
	rows, err := tx.Query(ctx, schema.SQL(`
		SELECT kind, projection_table, id_column, survives_sharing
		  FROM {schema}.projection_kind ORDER BY kind`))
	if err != nil {
		return nil, fmt.Errorf("read projection kinds: %w", err)
	}
	defer rows.Close()

	var out []projectionKind
	for rows.Next() {
		var k projectionKind
		if err := rows.Scan(&k.kind, &k.table, &k.idCol, &k.survivesSharing); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

const openErasureSQL = `
INSERT INTO {schema}.erasure_request (request_id, scope, selector, reason)
VALUES ($1, $2, $3::jsonb, $4)`

const closeErasureSQL = `
UPDATE {schema}.erasure_request SET completed_at = $2, residual = $3 WHERE request_id = $1`

// deleteProjectionSQL removes the rows of one kind that belong to the selection alone.
//
// The table and column are interpolated because PostgreSQL parameters are values and never
// identifiers. They come from `projection_kind`, whose CHECK constraints permit nothing but
// `[a-z][a-z0-9_]*` — so the names could not have been anything else, rather than being made safe on
// the way out.
//
// `NOT EXISTS` is the sharing rule: a projection with any registration belonging to a different
// subject — or to none — stays. An entity is the case that matters, and removing a shared one would
// take a node out of everybody else's graph to satisfy one person's request.
func deleteProjectionSQL(schema Schema, k projectionKind, sel sourceSelection) string {
	return schema.SQL(fmt.Sprintf(`
DELETE FROM {schema}.%s t
 WHERE t.scope = $1
   AND t.%s::text IN (%s)`, k.table, k.idCol, selectErasableSQL(k, sel)))
}

// selectErasableSQL is the predicate that decides what an erasure removes, and it is used verbatim by
// the count afterwards.
//
// # The clause a kind can switch off
//
// A projection another subject also registered is kept — `Ensera` is registered by everybody who
// mentioned it, and removing it because one of them left would take a node out of everybody else's
// graph. That is right for a shared IDENTITY, whose row means the same thing to each registrant.
//
// It is wrong for shared TEXT. A report written from several people's words contains each of them, so
// keeping it because somebody else also contributed leaves the departing subject's material in a row
// the erasure walked past and counted as correctly retained. The kind declares which it is
// (`survives_sharing`), so the question is answered where the table name is rather than in the eraser,
// and a kind added later cannot avoid answering it.
func selectErasableSQL(k projectionKind, sel sourceSelection) string {
	shared := `
           AND NOT EXISTS (
                SELECT 1 FROM {schema}.projection_dependency other
                 WHERE other.projection_kind = d.projection_kind
                   AND other.projection_id   = d.projection_id
                   AND ` + sel.foreign() + `)`
	if !k.survivesSharing {
		shared = ""
	}
	return fmt.Sprintf(`
        SELECT d.projection_id
          FROM {schema}.projection_dependency d
         WHERE d.scope = $1 AND d.projection_kind = %s AND %s%s`,
		quoteLiteral(k.kind), sel.registrations(), shared)
}

// countResidualSQL counts, against the same predicate, what is still there.
//
// The same predicate is the whole point. A count with a different one measures something else and
// reports it under the name of this erasure — and it would report zero for all the usual reasons a
// query returns nothing.
func countResidualSQL(schema Schema, k projectionKind, sel sourceSelection) string {
	return schema.SQL(fmt.Sprintf(`
SELECT count(*)
  FROM {schema}.%s t
 WHERE t.scope = $1
   AND t.%s::text IN (%s)`, k.table, k.idCol, selectErasableSQL(k, sel)))
}

// quoteLiteral renders a kind as a SQL string literal.
//
// The kind is also constrained to `[a-z][a-z0-9_]*` at its source, so this cannot encounter a quote
// to double. It doubles them anyway: a defence that depends on a constraint in another file being
// read correctly is one edit away from not being a defence.
func quoteLiteral(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'')
		}
		out = append(out, s[i])
	}
	return string(append(out, '\''))
}

// Erasures returns the receipts recorded for a scope, newest first.
//
// A receipt is a row rather than a log line because it is the artefact the product is sold on: an
// erasure in flight has no completion time, and one with a non-zero residual is an erasure that did
// not do what it claimed. Both have to be findable by asking, not by grepping.
func (e *Eraser) Erasures(ctx context.Context, schema Schema, scope string) ([]domain.Erasure, error) {
	rows, err := e.pool.Query(ctx, schema.SQL(selectErasuresSQL), scope)
	if err != nil {
		return nil, fmt.Errorf("read erasures: %w", err)
	}
	defer rows.Close()

	var out []domain.Erasure
	for rows.Next() {
		var record domain.Erasure
		var residual, selector []byte
		var completed *time.Time
		if err := rows.Scan(&record.RequestID, &record.Scope, &selector,
			&record.Reason, &completed, &residual); err != nil {
			return nil, err
		}
		var picked struct {
			Subject string   `json:"data_subject_id"`
			Sources []string `json:"source_observation_ids"`
		}
		if err := json.Unmarshal(selector, &picked); err != nil {
			return nil, fmt.Errorf("decode selector: %w", err)
		}
		record.DataSubjectID, record.SourceObservationIDs = picked.Subject, picked.Sources
		if completed != nil {
			record.CompletedAt = *completed
		}
		if residual != nil {
			if err := json.Unmarshal(residual, &record.Residual); err != nil {
				return nil, fmt.Errorf("decode residual: %w", err)
			}
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

const selectErasuresSQL = `
SELECT request_id::text, scope, selector, reason, completed_at, residual
  FROM {schema}.erasure_request
 WHERE scope = $1
 ORDER BY requested_at DESC`
