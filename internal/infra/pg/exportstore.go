// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/domain"
)

// Exporter answers "what do you hold about me".
//
// # Why it walks the same registry erasure does
//
// Erasure discovers what to delete from `projection_kind` rather than from a list in code, so a
// projection added by a migration is swept without anybody remembering to add it. Export has exactly
// the same obligation and the opposite failure mode: a projection missing from a deletion leaves data
// behind, which a residual count catches — a projection missing from an export leaves data out, and
// nothing catches that at all. An export nobody compares against anything looks complete.
//
// So it reads the same table. A kind that erasure would delete is a kind export must produce, and
// there is a test that fails when the two disagree.
//
// # Why it includes what was refused
//
// `rejected_claim` holds verbatim quotes from a person's messages — claims the vocabulary would not
// admit or whose quote could not be located. They were not stored as facts and they are unambiguously
// that person's words. An export that omitted them would understate what is held, which is the one
// direction an export must not err in.
type Exporter struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewExporter(pool *pgxpool.Pool, schema Schema) *Exporter {
	return &Exporter{pool: pool, schema: schema}
}

// Export gathers everything held about one data subject in one project.
//
// Read in a single transaction at one snapshot. An export assembled across several reads could show a
// fact whose evidence had been erased between them — internally inconsistent, and the inconsistency
// would look like a defect in the memory rather than in the export.
// MaxExportRows bounds one export. A subject with more than this is exported by source instead,
// a document or a conversation at a time.
//
// A variable rather than a constant so a test can lower it and watch the refusal happen; it is a
// bound this deployment holds, not a setting an operator turns up. Changing it at runtime would
// mean two exports of the same person disagreeing about what "too large" means.
//
// The number is a ceiling on rows across every section, not bytes: a row's size is the person's own
// words and cannot be predicted here, while a row count is what both the export and the erasure
// receipt already report, so a caller who hits this can see why in the numbers they already have.
var MaxExportRows = 50000

// ErrExportTooLarge is a subject whose export exceeds MaxExportRows. It carries no content, only
// the bound, because the caller's next move is to narrow the request rather than to read a number.
var ErrExportTooLarge = errors.New("the export exceeds the supported size; take it by source")

func (e *Exporter) Export(ctx context.Context, schema Schema, scope, dataSubjectID string) (domain.Export, error) {
	if dataSubjectID == "" {
		// The same refusal erasure makes, for the same reason: without a subject this is not an
		// export, it is a copy of the whole project wearing a governance label.
		return domain.Export{}, fmt.Errorf("an export names the person it is for")
	}
	return e.export(ctx, schema, scope, sourceSelection{subject: dataSubjectID})
}

// ExportSources answers what is held about a named set of turns, the way erasure by source deletes
// one. A document observed project-wide has no person, so a subject is not a way to reach it
// and, until now, neither was an export.
func (e *Exporter) ExportSources(ctx context.Context, schema Schema, scope string, sources []string) (domain.Export, error) {
	if len(sources) == 0 {
		return domain.Export{}, fmt.Errorf("an export by source names at least one observation")
	}
	for _, id := range sources {
		if _, err := uuid.Parse(id); err != nil {
			return domain.Export{}, fmt.Errorf("an export by source names observations by id: %q is not one", id)
		}
	}
	return e.export(ctx, schema, scope, sourceSelection{sources: sources})
}

func (e *Exporter) export(ctx context.Context, schema Schema, scope string, sel sourceSelection) (domain.Export, error) {
	arg := sel.arg()
	out := domain.Export{Scope: scope, DataSubjectID: sel.subject, SourceObservationIDs: sel.sources, Sections: map[string][]json.RawMessage{}}
	err := pgx.BeginTxFunc(ctx, e.pool, pgx.TxOptions{
		// Repeatable read, so every section is the same instant. See above.
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(tx pgx.Tx) error {
		kinds, err := declaredKinds(ctx, tx, schema)
		if err != nil {
			return err
		}

		// The observations themselves, which are the person's own words and the thing every
		// projection derives from. Not a projection kind — projections are what was DERIVED, and an
		// export that returned only derivations would omit the messages they came from.
		messages, err := exportRows(ctx, tx, schema, `
			SELECT to_jsonb(m)
			  FROM {schema}.turn_message m
			  JOIN {schema}.observation o ON o.observation_id = m.observation_id
			 WHERE o.scope = $1 AND `+sel.observations("o.")+`
			 ORDER BY o.log_offset, m.ordinal`, scope, arg)
		if err != nil {
			return err
		}
		out.Sections["message"] = messages

		// The quotes behind the facts. Not a projection kind and not on any hand-written list, so
		// until now an export handed a person their facts with the words stripped out — the one
		// part of a fact they can actually check. Evidence names its own fact's turn, so it
		// is selected the way every other source-owned row is.
		evidence, err := exportRows(ctx, tx, schema, `
			SELECT to_jsonb(e)
			  FROM {schema}.fact_evidence e
			  JOIN {schema}.observation o ON o.observation_id = e.source_observation_id AND o.scope = e.scope
			 WHERE o.scope = $1 AND `+sel.observations("o.")+`
			 ORDER BY e.fact_id, e.source_observation_id, e.byte_start`, scope, arg)
		if err != nil {
			return err
		}
		out.Sections["fact_evidence"] = evidence
		subjects, err := exportRows(ctx, tx, schema, `SELECT to_jsonb(s) FROM {schema}.data_subject s WHERE scope=$1 AND subject_id IN (`+sel.subjects()+`)`, scope, arg)
		if err != nil {
			return err
		}
		out.Sections["data_subject"] = subjects
		subjectRetries, err := exportRows(ctx, tx, schema, `SELECT to_jsonb(r) FROM {schema}.subject_retry r WHERE scope=$1 AND subject_id IN (`+sel.subjects()+`) ORDER BY key_digest`, scope, arg)
		if err != nil {
			return err
		}
		out.Sections["subject_retry"] = subjectRetries

		withdrawals, err := exportRows(ctx, tx, schema, `SELECT to_jsonb(r) FROM {schema}.record_retraction r
            JOIN {schema}.observation o ON o.scope=r.scope AND o.observation_id=r.source_observation_id
            WHERE o.scope=$1 AND `+sel.observations("o.")+` ORDER BY r.target_fact_id,r.source_observation_id,r.source_ordinal`, scope, arg)
		if err != nil {
			return err
		}
		out.Sections["record_retraction"] = withdrawals
		curated, err := exportRows(ctx, tx, schema, `SELECT to_jsonb(c) FROM {schema}.curated_claim c
            JOIN {schema}.observation o ON o.scope=c.scope AND o.observation_id=c.source_observation_id
            WHERE o.scope=$1 AND `+sel.observations("o.")+` ORDER BY c.source_observation_id`, scope, arg)
		if err != nil {
			return err
		}
		out.Sections["curated_claim"] = curated

		pins, err := exportRows(ctx, tx, schema, `SELECT to_jsonb(r) FROM {schema}.source_extraction r
            JOIN {schema}.observation o ON o.scope=r.scope AND o.observation_id=r.source_observation_id
            WHERE o.scope=$1 AND `+sel.observations("o.")+` ORDER BY r.source_observation_id`, scope, arg)
		if err != nil {
			return err
		}
		out.Sections["source_extraction"] = pins
		names, err := exportRows(ctx, tx, schema, `SELECT to_jsonb(r)-'name_hash' FROM {schema}.entity_name_receipt r
            JOIN {schema}.observation o ON o.scope=r.scope AND o.observation_id=r.source_observation_id
            WHERE o.scope=$1 AND `+sel.observations("o.")+` ORDER BY r.entity_id,r.name,r.source_observation_id`, scope, arg)
		if err != nil {
			return err
		}
		out.Sections["entity_name_receipt"] = names

		for _, table := range []string{"fact_generation", "fact_generation_record"} {
			items, err := exportRows(ctx, tx, schema, "SELECT to_jsonb(r) FROM {schema}."+table+" r WHERE r.scope=$1 AND r.source_observation_id IN ("+sel.owned()+") ORDER BY r.generation_id", scope, arg)
			if err != nil {
				return err
			}
			out.Sections[table] = items
		}

		for _, table := range []string{"fact_receipt", "fact_receipt_history"} {
			records, err := exportRows(ctx, tx, schema, "SELECT to_jsonb(r) FROM {schema}."+table+" r WHERE "+receiptOwnershipSQLFor(table, sel.owned())+" ORDER BY r.fact_id", scope, arg)
			if err != nil {
				return err
			}
			out.Sections[table] = records
		}

		for _, k := range kinds {
			// The same interpolation erasure uses, and safe for the same reason: these identifiers
			// come from `projection_kind`, whose constraints permit nothing that is not one.
			rows, err := exportRows(ctx, tx, schema, fmt.Sprintf(`
				SELECT to_jsonb(p)
				  FROM {schema}.%s p
				  JOIN {schema}.projection_dependency d
				    ON d.projection_id = p.%s::text AND d.projection_kind = '%s'
				 WHERE d.scope = $1 AND `+sel.registrations()+``, k.table, k.idCol, k.kind),
				scope, arg)
			if err != nil {
				return fmt.Errorf("export %s: %w", k.kind, err)
			}
			out.Sections[k.kind] = rows
		}
		total := 0
		for _, rows := range out.Sections {
			total += len(rows)
		}
		if total > MaxExportRows {
			// Refused after the walk rather than estimated before it: the count is exact, the
			// transaction is read-only, and a guess that refused an export a caller was entitled to
			// would be worse than the work spent finding out.
			return fmt.Errorf("%w: %d rows", ErrExportTooLarge, total)
		}
		return nil
	})
	if err != nil {
		return domain.Export{}, err
	}
	return out, nil
}

func exportRows(ctx context.Context, tx pgx.Tx, schema Schema, sql string, args ...any) ([]json.RawMessage, error) {
	rows, err := tx.Query(ctx, schema.SQL(sql), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Never nil. An absent section and an empty one are different statements — "we hold none of
	// this" versus "we did not look" — and a JSON null would say the second when it meant the first.
	out := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, rows.Err()
}
