package pg_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// A seal covers only what is final. An entry that drew its id and has not committed is not
// sealed around: the seal waits for it, then covers it, and the chain verifies. Before 0064 the seal
// took the highest id it could see, a lower one committed after it, and verification reported
// tampering that never happened.
func TestASealWaitsForAnEntryStillBeingWritten(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "audit_seal_race")
	store := pg.NewAuditStore(pool, schema)
	entry := func(principal string) domain.AuditEntry {
		return domain.AuditEntry{Operation: domain.AuditRecall, Principal: principal,
			PrincipalKind: domain.PrincipalCredential, Project: "p1", Outcome: domain.OutcomeAllowed}
	}

	// A draws its id and does not commit; B draws a higher one and does.
	slow, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Rollback(ctx)
	if _, err := slow.Exec(ctx, schema.SQL(`INSERT INTO {schema}.audit_entry (operation, principal, principal_kind, project, outcome)
		VALUES ('recall', 'slow', 'credential', 'p1', 'allowed')`)); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, entry("fast")); err != nil {
		t.Fatal(err)
	}

	type sealed struct {
		seal domain.AuditSeal
		err  error
	}
	done := make(chan sealed, 1)
	go func() {
		s, err := store.Seal(ctx)
		done <- sealed{s, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("the seal did not wait for an entry still being written: %+v", r)
	case <-time.After(500 * time.Millisecond):
	}
	if err := slow.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var r sealed
	select {
	case r = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the seal never finished after the writer committed")
	}
	if r.err != nil || r.seal.Entries != 2 {
		t.Fatalf("the seal did not cover both entries: %+v, %v", r.seal, r.err)
	}
	v, err := store.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Valid || v.Unsealed != 0 {
		t.Fatalf("a seal around a late commit does not verify: %+v", v)
	}

	// Nothing more to seal is an ordinary answer, not an error.
	if s, err := store.Seal(ctx); err != nil || s.Entries != 0 {
		t.Fatalf("sealing nothing: %+v, %v", s, err)
	}
	// Neither table can be emptied with TRUNCATE, which row triggers never saw.
	for _, table := range []string{"audit_entry", "audit_seal"} {
		if _, err := pool.Exec(ctx, schema.SQL(`TRUNCATE {schema}.`+table)); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("TRUNCATE %s was not refused as append-only: %v", table, err)
		}
	}
}
