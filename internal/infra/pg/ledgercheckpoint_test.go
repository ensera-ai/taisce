// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/ledgerarchive"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ledgerOf(t *testing.T, name string) (context.Context, *pgxpool.Pool, pg.Schema, *pg.AuditStore, func(int), func(string, ...any) int) {
	t.Helper()
	ctx := context.Background()
	pool := testPool(t)
	// The roles first, so that the namespace is granted to the memory role as a deployment's is and
	// a statement made as that role is refused for the reason a deployment would refuse it.
	if err := migrate.EstablishPlanes(ctx, pool, "taisce-test-control", "taisce-test-data"); err != nil {
		t.Fatal(err)
	}
	schema := tenant(t, pool, name)
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP SCHEMA "+schema.String()+" CASCADE") })
	audit := pg.NewAuditStore(pool, schema)
	write := func(n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			if err := audit.Append(ctx, domain.AuditEntry{Operation: domain.AuditRecall, Principal: "credential-1",
				PrincipalKind: domain.PrincipalCredential, Project: "p1", Magnitude: i, Outcome: domain.OutcomeAllowed}); err != nil {
				t.Fatal(err)
			}
		}
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, schema.SQL(query), args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	return ctx, pool, schema, audit, write, count
}

// TestASealedRangeLeavesTheLedgerUnderACheckpointAndTheChainStillVerifies is the journey that
// bounds the ledger without weakening it.
//
// A range that is sealed is exported, checked from the file alone against a digest written down
// when it was sealed, and only then removed. What remains verifies from the checkpoint the range
// left behind, to the same head as before; the next seal chains to that checkpoint; and the next
// range to leave follows the first. At no point is an entry both out of the ledger and out of a
// file that verifies.
func TestASealedRangeLeavesTheLedgerUnderACheckpointAndTheChainStillVerifies(t *testing.T) {
	ctx, pool, schema, audit, write, count := ledgerOf(t, "ledger_checkpoint")
	write(5)
	first, err := audit.Seal(ctx)
	if err != nil || first.Entries != 5 {
		t.Fatalf("first seal: %+v %v", first, err)
	}
	write(3)
	second, err := audit.Seal(ctx)
	if err != nil || second.Entries != 3 {
		t.Fatalf("second seal: %+v %v", second, err)
	}
	write(2)
	whole, err := audit.Verify(ctx)
	if err != nil || !whole.Valid || whole.Seals != 2 || whole.Entries != 8 || whole.Unsealed != 2 || !bytes.Equal(whole.Head, second.Digest) || whole.Archived != 0 {
		t.Fatalf("before anything leaves: %+v %v", whole, err)
	}

	// Without a checkpoint nothing leaves, exactly as before there were checkpoints.
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.audit_entry`)); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("an entry was deleted with no checkpoint: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.audit_seal`)); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("a seal was deleted with no checkpoint: %v", err)
	}

	// Nothing was sealed an hour ago; everything sealed is at least no age at all. A negative age
	// is not an age.
	if _, err := audit.Export(ctx, time.Hour, io.Discard); !errors.Is(err, pg.ErrNothingToArchive) {
		t.Fatalf("an export with a cutoff before every seal: %v", err)
	}
	if _, err := audit.Export(ctx, -time.Second, io.Discard); err == nil {
		t.Fatal("an export beyond a negative age was accepted")
	}
	var file bytes.Buffer
	archive, err := audit.Export(ctx, 0, &file)
	if err != nil || archive.Seals != 2 || archive.Entries != 8 || archive.ThroughEntry != second.To ||
		!bytes.Equal(archive.Head, second.Digest) || !bytes.Equal(archive.PreviousDigest, make([]byte, 32)) {
		t.Fatalf("export: %+v %v", archive, err)
	}
	// The file is checked with no database, against the head the service logged when it sealed.
	offline, err := ledgerarchive.Verify(bytes.NewReader(file.Bytes()), second.Digest)
	if err != nil || !offline.Valid || offline.Seals != 2 || offline.Entries != 8 || offline.LastEntry != second.To {
		t.Fatalf("the exported file does not verify on its own: %+v %v", offline, err)
	}
	// Exporting removed nothing.
	if count(`SELECT count(*) FROM {schema}.audit_entry`) != 10 || count(`SELECT count(*) FROM {schema}.audit_seal`) != 2 {
		t.Fatal("an export removed something")
	}

	fileDigest := sha256.Sum256(file.Bytes())
	principal := uuid.NewString()
	if err := audit.Checkpoint(ctx, archive, fileDigest[:], principal); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	// The range is gone, the two unsealed entries are not, and the act is on the ledger after them.
	if count(`SELECT count(*) FROM {schema}.audit_entry WHERE entry_id <= $1`, second.To) != 0 || count(`SELECT count(*) FROM {schema}.audit_seal`) != 0 {
		t.Fatal("the archived range is still in the ledger")
	}
	if count(`SELECT count(*) FROM {schema}.audit_entry`) != 3 ||
		count(`SELECT count(*) FROM {schema}.audit_entry WHERE operation=$1 AND principal=$2 AND principal_kind='operator' AND magnitude=8`, domain.AuditAuditArchive, principal) != 1 {
		t.Fatal("the ledger does not hold the unsealed entries and one entry for the archive")
	}
	kept, err := audit.Verify(ctx)
	if err != nil || !kept.Valid || kept.Seals != 0 || kept.Entries != 0 || kept.Unsealed != 3 || kept.Archived != 8 || !bytes.Equal(kept.Head, second.Digest) {
		t.Fatalf("after the range left, the ledger should verify from the checkpoint to the same head: %+v %v", kept, err)
	}

	// The next seal chains to the checkpoint, not to zero.
	third, err := audit.Seal(ctx)
	if err != nil || third.Entries != 3 || third.From != second.To+1 {
		t.Fatalf("the seal after the checkpoint: %+v %v", third, err)
	}
	var previous []byte
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT previous_digest FROM {schema}.audit_seal`)).Scan(&previous); err != nil || !bytes.Equal(previous, second.Digest) {
		t.Fatalf("the seal after the checkpoint chains to %x, not to the checkpoint's %x (%v)", previous, second.Digest, err)
	}
	onward, err := audit.Verify(ctx)
	if err != nil || !onward.Valid || onward.Seals != 1 || onward.Entries != 3 || onward.Unsealed != 0 || !bytes.Equal(onward.Head, third.Digest) {
		t.Fatalf("after the next seal: %+v %v", onward, err)
	}

	// What a checkpoint does not cover still cannot leave.
	write(1)
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.audit_entry WHERE entry_id > $1`), second.To); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("an entry no checkpoint covers was deleted: %v", err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.audit_seal`)); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("a seal no checkpoint covers was deleted: %v", err)
	}

	// The second range to leave follows the first: its file begins where the first one ended.
	var next bytes.Buffer
	following, err := audit.Export(ctx, 0, &next)
	if err != nil || following.Seals != 1 || !bytes.Equal(following.PreviousDigest, second.Digest) || !bytes.Equal(following.Head, third.Digest) {
		t.Fatalf("the second export: %+v %v", following, err)
	}
	if v, err := ledgerarchive.Verify(bytes.NewReader(next.Bytes()), third.Digest); err != nil || !v.Valid || !bytes.Equal(v.PreviousDigest, second.Digest) {
		t.Fatalf("the second file: %+v %v", v, err)
	}
	nextDigest := sha256.Sum256(next.Bytes())
	if err := audit.Checkpoint(ctx, following, nextDigest[:], principal); err != nil {
		t.Fatal(err)
	}
	if v, err := audit.Verify(ctx); err != nil || !v.Valid || v.Archived != 11 || !bytes.Equal(v.Head, third.Digest) {
		t.Fatalf("after two ranges left: %+v %v", v, err)
	}
}

// TestACheckpointCoversOnlyWhatIsSealedAndOnlyAnOperatorWritesOne holds the rules the schema keeps
// whoever asks: a checkpoint names a real seal by its digest and runs forward, it is never changed,
// and the role that serves traffic can neither write one nor remove an entry.
func TestACheckpointCoversOnlyWhatIsSealedAndOnlyAnOperatorWritesOne(t *testing.T) {
	ctx, pool, schema, audit, write, count := ledgerOf(t, "ledger_checkpoint_rules")
	write(4)
	seal, err := audit.Seal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	write(2)
	insert := `INSERT INTO {schema}.audit_checkpoint (through_seal, through_entry, digest, entries, seals, archive_digest) VALUES ($1,$2,$3,$4,1,$5)`
	file := make([]byte, 32)
	for name, args := range map[string][]any{
		"a seal that does not exist":    {int64(99), seal.To, seal.Digest, 4, file},
		"a seal by another digest":      {int64(1), seal.To, make([]byte, 32), 4, file},
		"a seal by another last entry":  {int64(1), seal.To + 2, seal.Digest, 4, file},
		"a range of nothing":            {int64(1), seal.To, seal.Digest, 0, file},
		"a digest that is not a digest": {int64(1), seal.To, seal.Digest, 4, []byte("short")},
	} {
		if _, err := pool.Exec(ctx, schema.SQL(insert), args...); err == nil {
			t.Errorf("a checkpoint naming %s was written", name)
		}
	}
	if count(`SELECT count(*) FROM {schema}.audit_checkpoint`) != 0 || count(`SELECT count(*) FROM {schema}.audit_entry`) != 6 {
		t.Fatal("a refused checkpoint left something behind")
	}

	// The role that serves traffic writes no checkpoint and removes no entry, with or without one.
	asMemory := func(statement string, args ...any) error {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{migrate.DataRole}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, schema.SQL(statement), args...)
		return err
	}
	if err := asMemory(insert, int64(1), seal.To, seal.Digest, 4, file); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("the memory role wrote a checkpoint: %v", err)
	}

	var exported bytes.Buffer
	archive, err := audit.Export(ctx, 0, &exported)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Checkpoint(ctx, archive, file, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := asMemory(`DELETE FROM {schema}.audit_entry`); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("the memory role removed an entry: %v", err)
	}
	// A checkpoint is a record too.
	for _, statement := range []string{
		`UPDATE {schema}.audit_checkpoint SET entries = 1`,
		`DELETE FROM {schema}.audit_checkpoint`,
		`TRUNCATE {schema}.audit_checkpoint`,
	} {
		if _, err := pool.Exec(ctx, schema.SQL(statement)); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: %v", statement, err)
		}
	}
	// And it only runs forward: the same range cannot be checkpointed again.
	if err := audit.Checkpoint(ctx, archive, file, uuid.NewString()); err == nil {
		t.Error("one range was checkpointed twice")
	}
}

// TestALedgerThatDoesNotVerifyIsNotArchived holds the export to refusing a range that was altered.
// Archiving it would move the alteration into a file whose seals nobody would think to doubt.
func TestALedgerThatDoesNotVerifyIsNotArchived(t *testing.T) {
	ctx, pool, schema, audit, write, count := ledgerOf(t, "ledger_checkpoint_altered")
	write(4)
	if _, err := audit.Seal(ctx); err != nil {
		t.Fatal(err)
	}
	// Altered the only way it can be: as the owner, with the triggers set aside.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, schema.SQL(`SET session_replication_role = replica; UPDATE {schema}.audit_entry SET magnitude = 999 WHERE entry_id = (SELECT min(entry_id) FROM {schema}.audit_entry); RESET session_replication_role`)); err != nil {
		t.Fatal(err)
	}
	conn.Release()
	if v, err := audit.Verify(ctx); err != nil || v.Valid {
		t.Fatalf("the altered ledger verifies, so this test alters nothing: %+v %v", v, err)
	}
	if _, err := audit.Export(ctx, 0, io.Discard); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("an altered range was exported: %v", err)
	}
	if count(`SELECT count(*) FROM {schema}.audit_checkpoint`) != 0 {
		t.Fatal("a refused export left a checkpoint")
	}
}

// TestALedgerEntryTakesTheBytesItsBoundIsStatedIn holds the number the ledger's stated size is
// arithmetic on.
//
// The documents say what the ledger holds for a rate of traffic and a window, and they get there
// by multiplying by the size of one entry with its indexes. That size is a property of the table
// and its indexes, not of the machine, so it is measured here: entries shaped like a deployment's,
// with a credential's identifier as the principal. An index added to the ledger, or a wider row,
// moves it — and should move the documents with it.
//
// It is a size. It says nothing about how fast an entry is written or a range is removed.
func TestALedgerEntryTakesTheBytesItsBoundIsStatedIn(t *testing.T) {
	ctx, pool, schema, _, _, _ := ledgerOf(t, "ledger_entry_size")
	const entries = 50000
	if _, err := pool.Exec(ctx, schema.SQL(`
		INSERT INTO {schema}.audit_entry (operation, principal, principal_kind, project, magnitude, outcome, occurred_at)
		SELECT (ARRAY['recall','observe','freshness','erase'])[1+i%4], gen_random_uuid()::text, 'credential',
		       'project-'||(i%20), i%500, 'allowed', now() - (i||' seconds')::interval
		  FROM generate_series(1,$1::int) i`), entries); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`VACUUM ANALYZE {schema}.audit_entry`)); err != nil {
		t.Fatal(err)
	}
	var table, indexes int64
	if err := pool.QueryRow(ctx, `SELECT pg_relation_size(to_regclass($1)), pg_indexes_size(to_regclass($1))`,
		schema.String()+".audit_entry").Scan(&table, &indexes); err != nil {
		t.Fatal(err)
	}
	each := float64(table+indexes) / entries
	t.Logf("%d entries: %d bytes of table and %d of indexes, %.0f bytes an entry", entries, table, indexes, each)
	if each > 300 || each < 150 {
		t.Fatalf("an entry takes %.0f bytes with its indexes; the documented bound is stated in about 261 and held under 300", each)
	}
}
