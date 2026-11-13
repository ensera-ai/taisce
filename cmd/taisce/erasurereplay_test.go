// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/erasurerecord"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// keptNamespace is a copy of every row a namespace held at one moment, and the way to put the
// namespace back to it.
//
// It stands in for a restore. What a restore does to this system, as far as an erasure is
// concerned, is return every table to what it held at an earlier moment, receipts included — and
// that is what putBack does, table by table, on the same database. What it does not exercise is
// the restore itself: the archive, the recovery, the time it takes. Those belong to a drill on a
// real cluster and are not claimed here.
type keptNamespace struct {
	pool         *pgxpool.Pool
	schema, copy string
	tables       []string
}

func keepNamespace(t *testing.T, pool *pgxpool.Pool, schema string) keptNamespace {
	t.Helper()
	ctx := context.Background()
	kept := keptNamespace{pool: pool, schema: schema, copy: schema + "_kept"}
	// Ordinary tables only: a partitioned table holds no rows of its own, and its partitions are
	// ordinary tables.
	rows, err := pool.Query(ctx, `SELECT relname FROM pg_class WHERE relnamespace=to_regnamespace($1) AND relkind='r' ORDER BY 1`, schema)
	if err != nil {
		t.Fatal(err)
	}
	if kept.tables, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil || len(kept.tables) < 40 {
		t.Fatalf("the namespace has %d tables to keep (%v)", len(kept.tables), err)
	}
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+kept.copy+" CASCADE; CREATE SCHEMA "+kept.copy); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+kept.copy+" CASCADE") })
	for _, table := range kept.tables {
		name := pgx.Identifier{table}.Sanitize()
		if _, err := pool.Exec(ctx, "CREATE TABLE "+kept.copy+"."+name+" AS TABLE "+schema+"."+name); err != nil {
			t.Fatalf("keep %s: %v", table, err)
		}
	}
	return kept
}

func (k keptNamespace) putBack(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	conn, err := k.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	// As a restore does: rows are written as they were, and no trigger or reference is asked
	// whether it agrees. The setting is this session's and is put back before the session is.
	if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, `RESET session_replication_role`)
	var all []string
	for _, table := range k.tables {
		all = append(all, k.schema+"."+pgx.Identifier{table}.Sanitize())
	}
	if _, err := conn.Exec(ctx, "TRUNCATE "+strings.Join(all, ", ")+" CASCADE"); err != nil {
		t.Fatalf("empty the namespace: %v", err)
	}
	for _, table := range k.tables {
		// A generated column is computed from the others and cannot be written.
		rows, err := conn.Query(ctx, `SELECT quote_ident(attname) FROM pg_attribute
			WHERE attrelid=to_regclass($1) AND attnum>0 AND NOT attisdropped AND attgenerated='' ORDER BY attnum`,
			k.schema+"."+pgx.Identifier{table}.Sanitize())
		if err != nil {
			t.Fatal(err)
		}
		columns, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		list, name := strings.Join(columns, ","), pgx.Identifier{table}.Sanitize()
		if _, err := conn.Exec(ctx, "INSERT INTO "+k.schema+"."+name+" ("+list+") OVERRIDING SYSTEM VALUE SELECT "+list+" FROM "+k.copy+"."+name); err != nil {
			t.Fatalf("put back %s: %v", table, err)
		}
	}
}

// TestAnErasureARestoreUndidIsAppliedAgainFromItsTrace is the journey the trace exists for.
//
// A person is erased and so is a set of turns. The database is put back to before either. Both are
// then in it again and neither receipt is — which is the defect: a system holding somebody it was
// told to forget, with no record that it was told. The replay is given the process log and nothing
// else, and has to find them, erase them with a residual of zero, and bring each receipt back
// under the identifier it had.
//
// Around that journey, each way a replay must refuse or do nothing is made to happen: records
// written under another key, a record the log lost part of, a project the database does not have,
// a person it never held, a line that is not a record, and a second run.
func TestAnErasureARestoreUndidIsAppliedAgainFromItsTrace(t *testing.T) {
	ctx := context.Background()
	const name = "cmd_erasure_replay"
	env := complete(t)
	env[envSchema] = name
	env[envAdminDSN] = env[envMemoryDSN]
	env[envErasureRecordKey] = strings.Repeat("k", 40)
	withEnv(t, env)
	pool, err := pgxpool.New(ctx, env[envAdminDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dropSchema(t, name)
	defer pool.Exec(ctx, "DROP SCHEMA "+name+" CASCADE")
	if err := migrate.ProvisionMemorySchema(ctx, pool, name); err != nil {
		t.Fatal(err)
	}
	if err := migrate.ProvisionScope(ctx, pool, name, "p1"); err != nil {
		t.Fatal(err)
	}
	schema, _ := pg.NewSchema(name)
	observations, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	said := func(subject, content string) string {
		t.Helper()
		stored, err := observations.Append(ctx, schema, domain.Turn{Scope: "p1", DataSubjectID: subject, OccurredAt: time.Now().UTC(),
			Messages: []domain.Message{{Role: domain.RoleUser, Content: content, OccurredAt: time.Now().UTC()}}})
		if err != nil {
			t.Fatal(err)
		}
		return stored.ID
	}
	// Ada, who will be erased as a person, with a fact formed from what she said.
	adaTurn := said("ada", "Ada works at Ensera")
	if _, err := facts.Assert(ctx, schema, "p1", adaTurn, domain.RoleUser, "ada", domain.Claim{Subject: "Ada", Predicate: "works_at", Object: "Ensera",
		Statement: "Ada works at Ensera", Quote: "Ada works at Ensera", ByteEnd: len("Ada works at Ensera"), Cardinality: domain.CardinalityMany}); err != nil {
		t.Fatal(err)
	}
	// Lin, whose turns will be erased by their own identifiers: enough of them for a record of two lines.
	var linTurns []string
	for i := 0; i < 51; i++ {
		linTurns = append(linTurns, said("lin", fmt.Sprintf("Lin said thing %d", i)))
	}
	// Grace, who asked for nothing and must lose nothing.
	said("grace", "Grace is still here")
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, schema.SQL(query), args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	held := func(subject string) int {
		return count(`SELECT (SELECT count(*) FROM {schema}.observation WHERE scope='p1' AND data_subject_id=$1)
			+ (SELECT count(*) FROM {schema}.projection_dependency WHERE scope='p1' AND data_subject_id=$1)
			+ (SELECT count(*) FROM {schema}.data_subject WHERE scope='p1' AND subject_id=$1)`, subject)
	}

	backup := keepNamespace(t, pool, name)

	// The erasures, by a serving process whose log is this buffer.
	var logged bytes.Buffer
	recorder, err := erasureRecorder(slog.New(slog.NewJSONHandler(&logged, nil)))
	if err != nil || recorder == nil {
		t.Fatalf("a process holding a key has no recorder: %v", err)
	}
	eraser := pg.NewEraser(pool).Recording(recorder)
	person, err := eraser.Erase(ctx, schema, "p1", "ada", "she asked")
	if err != nil || !person.Clean() {
		t.Fatalf("erase ada: %+v %v", person, err)
	}
	turns, err := eraser.EraseSources(ctx, schema, "p1", linTurns, "they were sent in error")
	if err != nil || !turns.Clean() {
		t.Fatalf("erase lin's turns: %+v %v", turns, err)
	}
	if held("ada") != 0 || count(`SELECT count(*) FROM {schema}.observation WHERE scope='p1' AND data_subject_id='lin'`) != 0 {
		t.Fatal("the erasures left something, so the rest of this test would show nothing")
	}
	for _, private := range []string{"ada", "she asked", "sent in error", linTurns[0]} {
		if strings.Contains(logged.String(), private) {
			t.Fatalf("the log holds %q", private)
		}
	}

	// The restore. Everybody is back, and the database has forgotten being told.
	backup.putBack(t)
	if held("ada") == 0 || count(`SELECT count(*) FROM {schema}.observation WHERE scope='p1' AND data_subject_id='lin'`) != 51 {
		t.Fatal("after the restore the erased rows are not back, so there is nothing for a replay to do")
	}
	if count(`SELECT count(*) FROM {schema}.erasure_request`) != 0 {
		t.Fatal("after the restore the database still holds a receipt")
	}

	records := filepath.Join(t.TempDir(), "taisce.log")
	if err := os.WriteFile(records, logged.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	replay := func(input io.Reader, args ...string) (replayResult, error) {
		t.Helper()
		var out bytes.Buffer
		err := erasureCommand(ctx, append([]string{"replay"}, args...), input, &out)
		var result replayResult
		if out.Len() > 0 {
			if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
				t.Fatalf("the report is not JSON: %v\n%s", decodeErr, out.String())
			}
		}
		for _, private := range []string{"ada", linTurns[0]} {
			if strings.Contains(out.String(), private) {
				t.Fatalf("the report names %q", private)
			}
		}
		return result, err
	}
	state := func(result replayResult, requestID string) replayedErasure {
		t.Helper()
		for _, e := range result.Erasures {
			if e.RequestID == requestID {
				return e
			}
		}
		t.Fatalf("the report does not mention erasure %s: %+v", requestID, result)
		return replayedErasure{}
	}

	// Under another key the digests match nobody. That is refused, not reported as nothing held.
	t.Setenv(envErasureRecordKey, strings.Repeat("j", 40))
	result, err := replay(nil, "--records", records)
	if err == nil || state(result, person.RequestID).State != replayRefused || !strings.Contains(state(result, person.RequestID).Why, "different key") {
		t.Fatalf("records written under another key: %+v %v", result, err)
	}
	t.Setenv(envErasureRecordKey, env[envErasureRecordKey])

	// A dry run says what it would do and does none of it.
	result, err = replay(nil, "--records", records, "--dry-run")
	if err != nil || !result.DryRun || result.AuditPrincipal != "" || result.Records != 3 ||
		state(result, person.RequestID).State != replayWouldApply || state(result, person.RequestID).Matched != 1 ||
		state(result, turns.RequestID).State != replayWouldApply || state(result, turns.RequestID).Matched != 51 {
		t.Fatalf("dry run: %+v %v", result, err)
	}
	if held("ada") == 0 || count(`SELECT count(*) FROM {schema}.erasure_request`) != 0 {
		t.Fatal("a dry run erased something or wrote a receipt")
	}

	// A record the log lost a line of is refused whole: half of an erasure is not one.
	var partial []string
	for _, line := range strings.Split(strings.TrimSpace(logged.String()), "\n") {
		if !strings.Contains(line, `"part":2`) {
			partial = append(partial, line)
		}
	}
	result, err = replay(strings.NewReader(strings.Join(partial, "\n")), "--records", "-", "--dry-run")
	if err == nil || state(result, turns.RequestID).State != replayRefused || !strings.Contains(state(result, turns.RequestID).Why, "missing") ||
		state(result, person.RequestID).State != replayWouldApply {
		t.Fatalf("a record missing a line: %+v %v", result, err)
	}

	// The replay.
	result, err = replay(nil, "--records", records)
	if err != nil {
		t.Fatalf("replay: %+v %v", result, err)
	}
	for _, erased := range []replayedErasure{state(result, person.RequestID), state(result, turns.RequestID)} {
		if erased.State != replayApplied || erased.Residual != 0 || erased.Deleted == 0 {
			t.Fatalf("an erasure was not applied again cleanly: %+v", erased)
		}
	}
	if held("ada") != 0 {
		t.Fatalf("after the replay ada is still held in %d rows", held("ada"))
	}
	if n := count(`SELECT count(*) FROM {schema}.observation WHERE scope='p1' AND data_subject_id='lin'`); n != 0 {
		t.Fatalf("after the replay %d of lin's turns are still held", n)
	}
	if held("grace") == 0 {
		t.Fatal("the replay removed somebody no erasure named")
	}
	// The receipts are back under the identifiers they had, each saying what it is.
	for _, id := range []string{person.RequestID, turns.RequestID} {
		var reason string
		var residual []byte
		if err := pool.QueryRow(ctx, schema.SQL(`SELECT reason,residual FROM {schema}.erasure_request WHERE request_id=$1 AND completed_at IS NOT NULL`), id).Scan(&reason, &residual); err != nil {
			t.Fatalf("the receipt of erasure %s did not come back: %v", id, err)
		}
		if reason != pg.ReappliedErasureReason || len(residual) == 0 {
			t.Fatalf("receipt %s: reason %q residual %s", id, reason, residual)
		}
	}
	// And the ledger says an operator did it.
	if n := count(`SELECT count(*) FROM {schema}.audit_entry WHERE operation=$1 AND principal=$2 AND principal_kind=$3`,
		domain.AuditErase, result.AuditPrincipal, domain.PrincipalOperator); n != 2 {
		t.Fatalf("the ledger holds %d entries for the replay, not 2", n)
	}

	// Run again, it finds both receipts and does nothing.
	result, err = replay(nil, "--records", records)
	if err != nil || state(result, person.RequestID).State != replayAlreadyHeld || state(result, turns.RequestID).State != replayAlreadyHeld {
		t.Fatalf("a second replay: %+v %v", result, err)
	}
	if count(`SELECT count(*) FROM {schema}.erasure_request`) != 2 {
		t.Fatal("a second replay wrote another receipt")
	}

	// Somebody this database never held, and a project it does not have.
	key, _ := erasurerecord.ParseKey(env[envErasureRecordKey])
	ghost, elsewhere := uuid.NewString(), uuid.NewString()
	strangers := key.Records(ghost, "p1", erasurerecord.KindSubject, []string{"never-here"})[0].Line() + "\n" +
		key.Records(elsewhere, "p9", erasurerecord.KindSubject, []string{"ada"})[0].Line()
	result, err = replay(strings.NewReader(strangers), "--records", "-")
	if err == nil || state(result, ghost).State != replayNothingHeld || state(result, elsewhere).State != replayRefused ||
		!strings.Contains(state(result, elsewhere).Why, "no such project") {
		t.Fatalf("a stranger and a missing project: %+v %v", result, err)
	}

	// What is not a replay at all.
	if _, err := replay(strings.NewReader("taisce-erasure-record:v1:bm90IGpzb24"), "--records", "-"); err == nil {
		t.Error("a line that is not a record was skipped")
	}
	if _, err := replay(nil, "--records", filepath.Join(t.TempDir(), "absent.log")); err == nil {
		t.Error("a file that is not there was accepted")
	}
	for _, args := range [][]string{nil, {"--records"}, {"--records", records, "extra"}, {"--unknown"}} {
		if _, err := replay(nil, args...); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("arguments %v: %v", args, err)
		}
	}
	if err := erasureCommand(ctx, nil, nil, io.Discard); err == nil {
		t.Error("a bare erasure command was accepted")
	}
	t.Setenv(envErasureRecordKey, "")
	if _, err := replay(nil, "--records", records); err == nil || !strings.Contains(err.Error(), envErasureRecordKey) {
		t.Errorf("a replay without the key: %v", err)
	}
}

// TestAProcessWithoutAKeySaysWhatARestoreWillDo holds the two ways a serving process can be without
// a usable key apart: none set is said once and served; one set and unusable stops the process.
func TestAProcessWithoutAKeySaysWhatARestoreWillDo(t *testing.T) {
	var logged bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logged, nil))
	t.Setenv(envErasureRecordKey, "")
	recorder, err := erasureRecorder(log)
	if err != nil || recorder != nil {
		t.Fatalf("no key set must serve without a recorder: %v", err)
	}
	if !strings.Contains(logged.String(), `"level":"WARN"`) || !strings.Contains(logged.String(), envErasureRecordKey) {
		t.Fatalf("a process with no key did not say so: %s", logged.String())
	}
	t.Setenv(envErasureRecordKey, "too-short")
	if _, err := erasureRecorder(log); err == nil || !strings.Contains(err.Error(), envErasureRecordKey) {
		t.Fatalf("a key that is set and unusable must stop the process: %v", err)
	}
}
