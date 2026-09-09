// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestChunkRecoveryPreservesMessageIdentityAcrossPagesAndErasure(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "chunk_recovery_pages")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewObservationStore(pool)
	source, err := store.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "مرحبا Dublin"}, domain.Message{Ordinal: 1, Role: domain.RoleAssistant, Content: "Hello"}))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() []string {
		rows, err := pool.Query(ctx, schema.SQL(`SELECT row_to_json(c)::text FROM {schema}.chunk c WHERE scope='p1' ORDER BY source_message_ordinal`))
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.chunk; DELETE FROM {schema}.projection_dependency WHERE projection_kind='chunk'`)); err != nil {
		t.Fatal(err)
	}
	actor := uuid.NewString()
	start := pg.ChunkRecoveryCursor{Offset: -1, Ordinal: -1}
	page, err := store.RecoverChunks(ctx, schema, "p1", source.ID, actor, start, 1)
	if err != nil || page.Restored != 1 || page.Examined != 1 || page.Next == nil {
		t.Fatalf("first: %+v %v", page, err)
	}
	page, err = store.RecoverChunks(ctx, schema, "p1", source.ID, actor, *page.Next, 1)
	if err != nil || page.Restored != 1 || page.Examined != 1 || page.Next != nil {
		t.Fatalf("last: %+v %v", page, err)
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("identity/content changed\nbefore %v\nafter %v", before, after)
	}
	page, err = store.RecoverChunks(ctx, schema, "p1", "", actor, start, 100)
	if err != nil || page.Restored != 0 || page.Repaired != 0 || page.Examined != 2 {
		t.Fatalf("retry %+v %v", page, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.projection_dependency WHERE projection_kind='chunk'`)); err != nil {
		t.Fatal(err)
	}
	page, err = store.RecoverChunks(ctx, schema, "p1", "", actor, start, 100)
	if err != nil || page.Repaired != 2 || page.Restored != 0 {
		t.Fatalf("registration repair %+v %v", page, err)
	}
	page, err = store.RecoverChunks(ctx, schema, "p2", source.ID, actor, start, 100)
	if err != nil || page.Examined != 0 {
		t.Fatalf("foreign source %+v %v", page, err)
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "test"); err != nil {
		t.Fatal(err)
	}
	page, err = store.RecoverChunks(ctx, schema, "p1", source.ID, actor, start, 100)
	if err != nil || page.Examined != 0 || len(snapshot()) != 0 {
		t.Fatalf("revived erased source %+v %v", page, err)
	}
}

func TestChunkRecoveryRefusesConflictsAndRollsBackTheWholePage(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "chunk_recovery_conflict")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewObservationStore(pool)
	_, err := store.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "one"}, domain.Message{Ordinal: 1, Role: domain.RoleAssistant, Content: "two"}))
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, schema.SQL(sql)); err != nil {
			t.Fatal(err)
		}
	}
	exec(`DELETE FROM {schema}.chunk WHERE source_message_ordinal=0; UPDATE {schema}.chunk SET text='conflicting text' WHERE source_message_ordinal=1`)
	start := pg.ChunkRecoveryCursor{Offset: -1, Ordinal: -1}
	actor := uuid.NewString()
	page, err := store.RecoverChunks(ctx, schema, "p1", "", actor, start, 100)
	if !errors.Is(err, pg.ErrInvalidReceipt) || page.Examined != 0 {
		t.Fatalf("conflict accepted %+v %v", page, err)
	}
	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.chunk`)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("partial page committed %d %v", n, err)
	}
	exec(`UPDATE {schema}.chunk SET text='two'; UPDATE {schema}.projection_dependency SET pipeline_version='unknown' WHERE projection_kind='chunk'`)
	if _, err := store.RecoverChunks(ctx, schema, "p1", "", actor, start, 100); !errors.Is(err, pg.ErrInvalidReceipt) {
		t.Fatalf("unknown pipeline accepted %v", err)
	}
	exec(`UPDATE {schema}.projection_dependency SET pipeline_version='v1'; ALTER TABLE {schema}.audit_entry ADD CONSTRAINT reject_chunk_audit CHECK(operation <> 'formation.recover')`)
	if _, err := store.RecoverChunks(ctx, schema, "p1", "", actor, start, 100); err == nil {
		t.Fatal("audit failure accepted")
	}
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.chunk`)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit failure committed %d %v", n, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.chunk SET chunk_id=gen_random_uuid()`)); err == nil {
		t.Fatal("foreign chunk identity accepted")
	}
	exec(`ALTER TABLE {schema}.audit_entry DROP CONSTRAINT reject_chunk_audit; DELETE FROM {schema}.chunk`)
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.turn_message SET content=$1 WHERE ordinal=0`), strings.Repeat("x", pg.MaxChunkRecoveryBytes+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecoverChunks(ctx, schema, "p1", "", actor, start, 100); !errors.Is(err, pg.ErrChunkRecoveryLimit) {
		t.Fatalf("oversized page accepted %v", err)
	}
	for _, v := range []struct {
		scope, source, actor string
		cursor               pg.ChunkRecoveryCursor
		limit                int
	}{
		{"", "", actor, start, 1}, {"p1", "bad", actor, start, 1}, {"p1", "", "", start, 1}, {"p1", "", uuid.Nil.String(), start, 1}, {"p1", "", actor, start, 0}, {"p1", "", actor, start, 101}, {"p1", "", actor, pg.ChunkRecoveryCursor{Offset: -2, Ordinal: -1}, 1}, {"p1", "", actor, pg.ChunkRecoveryCursor{Offset: 0, Ordinal: -1}, 1},
	} {
		if _, err := store.RecoverChunks(ctx, schema, v.scope, v.source, v.actor, v.cursor, v.limit); !errors.Is(err, pg.ErrInvalidRecovery) {
			t.Fatalf("invalid request %+v: %v", v, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.RecoverChunks(canceled, schema, "p1", "", actor, start, 1); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestConcurrentChunkRecoveryPreservesEmbeddingsAndSerializesErasure(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "chunk_recovery_concurrent")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewObservationStore(pool)
	source, err := store.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "source"}))
	if err != nil {
		t.Fatal(err)
	}
	embeddings := pg.NewMessageEmbeddingStore(pool, schema)
	actor := uuid.NewString()
	model := pg.EmbeddingModel{Name: "test", Revision: "v1", EndpointHash: strings.Repeat("a", 64), Dimensions: 1024}
	generation, err := embeddings.Start(ctx, "p1", uuid.NewString(), actor, model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := embeddings.BuildPage(ctx, "p1", generation.ID, actor, model, 64, func(context.Context, []string) ([][]float32, error) {
		v := make([]float32, 1024)
		for i := range v {
			v[i] = 0.5
		}
		return [][]float32{v}, nil
	}); err != nil {
		t.Fatal(err)
	}
	start := pg.ChunkRecoveryCursor{Offset: -1, Ordinal: -1}
	if _, err := store.RecoverChunks(ctx, schema, "p1", source.ID, uuid.NewString(), start, 100); err != nil {
		t.Fatal(err)
	}
	var preserved bool
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT embedding=array_fill(0.5::real,ARRAY[1024])::vector FROM {schema}.message_embedding`)).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("embedding changed %v %v", preserved, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.message_embedding; DELETE FROM {schema}.chunk`)); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	results := make(chan error, 5)
	for i := 0; i < 4; i++ {
		go func() {
			<-ready
			_, err := store.RecoverChunks(ctx, schema, "p1", source.ID, uuid.NewString(), start, 100)
			results <- err
		}()
	}
	go func() {
		<-ready
		_, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "concurrent recovery")
		results <- err
	}()
	close(ready)
	for i := 0; i < 5; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT (SELECT count(*) FROM {schema}.chunk)+(SELECT count(*) FROM {schema}.turn_message)+(SELECT count(*) FROM {schema}.projection_dependency)`)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("erasure residual %d %v", n, err)
	}
}
