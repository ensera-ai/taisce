// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func artifactFixture(t *testing.T, name string) (*pgxpool.Pool, pg.Schema, *pg.ArtifactStore) {
	t.Helper()
	pool := testPool(t)
	schema := tenant(t, pool, name)
	if _, err := pool.Exec(context.Background(), schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES('p1','p1'),('p2','p2') ON CONFLICT DO NOTHING`)); err != nil {
		t.Fatal(err)
	}
	return pool, schema, pg.NewArtifactStore(pool, schema)
}
func artifactRequest() pg.ArtifactPut {
	return pg.ArtifactPut{ID: uuid.NewString(), DataSubjectID: "owner-1", Kind: "file", Name: "notes.bin", Content: []byte{0, 255, 128, 'a', '\n', 0}}
}
func TestAgentArtifactsRoundTripOpaqueBytesAndEraseThroughTheProjectionRegistry(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := artifactFixture(t, "artifact_lifecycle")
	r := artifactRequest()
	actor := uuid.NewString()
	first, err := store.Put(ctx, "p1", actor, r)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.AuthoredBy != actor || first.Bytes != len(r.Content) || first.ExpiresAt.Sub(first.CreatedAt) > 31*24*time.Hour {
		t.Fatal("invalid artifact receipt")
	}
	got, err := store.Get(ctx, "p1", r.ID, "")
	if err != nil || !bytes.Equal(got.Content, r.Content) {
		t.Fatalf("bytes changed: %v", err)
	}
	replay, err := store.Put(ctx, "p1", uuid.NewString(), r)
	if err != nil || !replay.Replayed || replay.Version != first.Version || replay.AuthoredBy != actor {
		t.Fatal("create retry changed stored state")
	}
	for _, table := range []string{"turn_message", "chunk", "fact", "fact_receipt", "ingestion_reservation"} {
		var n int
		if err := pool.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+table)).Scan(&n); err != nil || n != 0 {
			t.Fatalf("artifact entered %s: %d %v", table, n, err)
		}
	}
	obs := pg.NewObservationStore(pool)
	if _, found, err := obs.NextUnformed(ctx, schema, "p1", time.Second); err != nil || found {
		t.Fatal("artifact was offered for extraction")
	}
	fresh, err := obs.Freshness(ctx, schema, "p1")
	if err != nil || !fresh.HasFormed || fresh.Formed != fresh.Stored {
		t.Fatal("completed storage left a freshness gap")
	}
	exported, err := pg.NewExporter(pool, schema).Export(ctx, schema, "p1", r.DataSubjectID)
	if err != nil || len(exported.Sections["agent_artifact"]) != 1 {
		t.Fatalf("export: %v", err)
	}
	var row struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(exported.Sections["agent_artifact"][0], &row); err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(row.Content, `\x`))
	if err != nil || !bytes.Equal(raw, r.Content) {
		t.Fatal("export omitted opaque bytes")
	}
	other, err := store.Put(ctx, "p2", actor, r)
	if err != nil || other.SourceObservationID == first.SourceObservationID {
		t.Fatal("identity crossed projects")
	}
	erased, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", r.DataSubjectID, "requested")
	if err != nil || erased.Deleted["agent_artifact"] != 1 || erased.Residual["agent_artifact"] != 0 {
		t.Fatalf("counted erasure: %+v %v", erased, err)
	}
	if _, err := store.Get(ctx, "p1", r.ID, ""); !errors.Is(err, pg.ErrArtifactNotFound) {
		t.Fatal("erased content survives")
	}
	if _, err := store.Put(ctx, "p1", actor, r); !errors.Is(err, pg.ErrArtifactConflict) {
		t.Fatal("late create recreated erased bytes")
	}
	if got, err := store.Get(ctx, "p2", r.ID, ""); err != nil || !bytes.Equal(got.Content, r.Content) {
		t.Fatal("erasure crossed project")
	}
	limits, err := store.Limits(ctx, "p1")
	if err != nil || limits.UsedBytes != 0 || limits.UsedObjects != 0 {
		t.Fatal("erasure failed to release allowance")
	}
	var receipts int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.observation_retry WHERE scope='p1' AND (observation_id IS NOT NULL OR request_digest IS NOT NULL)`)).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("erasure retained retry payload fingerprint")
	}
}

func TestAgentArtifactVersionsPreventLostUpdatesAndRetainOnlyCurrentBytes(t *testing.T) {
	ctx := context.Background()
	_, _, store := artifactFixture(t, "artifact_versions")
	r := artifactRequest()
	actor := uuid.NewString()
	first, err := store.Put(ctx, "p1", actor, r)
	if err != nil {
		t.Fatal(err)
	}
	update := r
	update.ExpectedVersion = first.Version
	update.Name = "updated"
	update.Content = []byte("updated opaque bytes")
	next, err := store.Put(ctx, "p1", actor, update)
	if err != nil || next.Version == first.Version || !next.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatalf("overwrite: %+v %v", next, err)
	}
	replay, err := store.Put(ctx, "p1", actor, update)
	if err != nil || !replay.Replayed || replay.Version != next.Version {
		t.Fatal("lost acknowledgement repeated update")
	}
	update.Content = []byte("competing update")
	if _, err := store.Put(ctx, "p1", actor, update); !errors.Is(err, pg.ErrArtifactConflict) {
		t.Fatal("stale version overwrote newer content")
	}
	replay, err = store.Put(ctx, "p1", actor, r)
	if err != nil || !replay.Replayed || replay.Version != next.Version {
		t.Fatal("creation retry did not return current metadata")
	}
	got, err := store.Get(ctx, "p1", r.ID, "")
	if err != nil || string(got.Content) != "updated opaque bytes" {
		t.Fatal("creation retry restored old payload")
	}
	for _, changed := range []pg.ArtifactPut{
		{ID: r.ID, DataSubjectID: "owner-2", Kind: r.Kind, Content: r.Content},
		{ID: r.ID, DataSubjectID: r.DataSubjectID, Kind: "state", Content: r.Content},
		{ID: r.ID, DataSubjectID: r.DataSubjectID, Kind: r.Kind, Name: "different-create", Content: r.Content},
	} {
		if _, err := store.Put(ctx, "p1", actor, changed); !errors.Is(err, pg.ErrArtifactConflict) {
			t.Fatal("creation identity or ownership changed")
		}
	}
	if err := store.Delete(ctx, "p1", actor, r.ID, first.Version, ""); !errors.Is(err, pg.ErrArtifactConflict) {
		t.Fatal("stale deletion accepted")
	}
	if err := store.Delete(ctx, "p2", actor, r.ID, next.Version, ""); !errors.Is(err, pg.ErrArtifactNotFound) {
		t.Fatal("foreign object existence leaked")
	}
	if err := store.Delete(ctx, "p1", actor, r.ID, next.Version, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "p1", actor, r.ID, next.Version, ""); !errors.Is(err, pg.ErrArtifactNotFound) {
		t.Fatal("absent deletion not reported")
	}
	if _, err := store.Put(ctx, "p1", actor, r); !errors.Is(err, pg.ErrArtifactConflict) {
		t.Fatal("delete allowed identity resurrection")
	}
	if _, err := store.Put(ctx, "p1", actor, update); !errors.Is(err, pg.ErrArtifactNotFound) {
		t.Fatal("update recreated deleted object")
	}
}

func TestConcurrentArtifactCreatesAndQuotaReservationsAreAtomic(t *testing.T) {
	ctx := context.Background()
	_, _, store := artifactFixture(t, "artifact_concurrency")
	actor := uuid.NewString()
	r := artifactRequest()
	if err := store.ConfigureLimits(ctx, "p1", actor, pg.ArtifactLimits{MaxObjectBytes: 6, MaxBytes: 6, MaxObjects: 1, MaxAgeHours: 1}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	created := make(chan bool, 12)
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := store.Put(ctx, "p1", actor, r)
			created <- !out.Replayed
			failures <- err
		}()
	}
	wg.Wait()
	close(created)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for fresh := range created {
		if fresh {
			count++
		}
	}
	if count != 1 {
		t.Fatal("retries spent quota more than once")
	}
	got, _ := store.Get(ctx, "p1", r.ID, "")
	if err := store.Delete(ctx, "p1", actor, r.ID, got.Version, ""); err != nil {
		t.Fatal(err)
	}
	failures = make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			request := artifactRequest()
			_, err := store.Put(ctx, "p1", actor, request)
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	success, refused := 0, 0
	for err := range failures {
		if err == nil {
			success++
		} else if errors.Is(err, pg.ErrArtifactCapacity) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || refused != 11 {
		t.Fatalf("quota admitted %d, refused %d", success, refused)
	}
	limits, err := store.Limits(ctx, "p1")
	if err != nil || limits.UsedBytes != 6 || limits.UsedObjects != 1 {
		t.Fatalf("quota drift: %+v %v", limits, err)
	}
}

func TestArtifactExpiryAndPolicyReductionDoNotExtendOrStrandStoredObjects(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := artifactFixture(t, "artifact_expiry")
	actor := uuid.NewString()
	r := artifactRequest()
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.project SET retention=interval '30 minutes' WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	first, err := store.Put(ctx, "p1", actor, r)
	if err != nil || first.ExpiresAt.Sub(first.CreatedAt) > 31*time.Minute {
		t.Fatalf("project retention ignored: %v", err)
	}
	if err := store.ConfigureLimits(ctx, "p1", actor, pg.ArtifactLimits{MaxObjectBytes: 1, MaxBytes: 1, MaxObjects: 1, MaxAgeHours: 1}); err != nil {
		t.Fatal(err)
	}
	// A reduced allowance must still permit shrinking a previously accepted object.
	r.ExpectedVersion = first.Version
	r.Content = []byte{0, 1}
	smaller, err := store.Put(ctx, "p1", actor, r)
	if err != nil || !smaller.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatal("lowering policy stranded or extended accepted data")
	}
	r.ExpectedVersion = smaller.Version
	r.Content = []byte{0, 1, 2}
	if _, err := store.Put(ctx, "p1", actor, r); !errors.Is(err, pg.ErrArtifactCapacity) {
		t.Fatal("growth above reduced allowance accepted")
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET retention_until=now()-interval '1 second' WHERE observation_id=$1::uuid`), first.SourceObservationID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "p1", r.ID, ""); !errors.Is(err, pg.ErrArtifactNotFound) {
		t.Fatal("expired object still readable before sweep")
	}
	page, err := store.List(ctx, "p1", "", "", 20)
	if err != nil || len(page.Artifacts) != 0 {
		t.Fatal("expired object still listed")
	}
	sweep, err := pg.NewRetentionStore(pool, schema).Sweep(ctx, "p1", 20)
	if err != nil || sweep.Deleted["agent_artifact"] != 1 {
		t.Fatalf("expiry missed artifact: %+v %v", sweep, err)
	}
	limits, _ := store.Limits(ctx, "p1")
	if limits.UsedBytes != 0 || limits.UsedObjects != 0 {
		t.Fatal("expiry leaked quota")
	}
	r.ExpectedVersion = ""
	r.Content = []byte{0}
	if _, err := store.Put(ctx, "p1", actor, r); !errors.Is(err, pg.ErrArtifactConflict) {
		t.Fatal("expiry allowed old identity to return")
	}
}

func TestArtifactPagesStayBoundedAndScopedWhenCursorRowsDisappear(t *testing.T) {
	ctx := context.Background()
	_, _, store := artifactFixture(t, "artifact_pages")
	actor := uuid.NewString()
	for i := 0; i < 3; i++ {
		r := artifactRequest()
		if _, err := store.Put(ctx, "p1", actor, r); err != nil {
			t.Fatal(err)
		}
	}
	r := artifactRequest()
	r.DataSubjectID = "owner-2"
	if _, err := store.Put(ctx, "p1", actor, r); err != nil {
		t.Fatal(err)
	}
	first, err := store.List(ctx, "p1", "owner-1", "", 1)
	if err != nil || len(first.Artifacts) != 1 || first.Next == nil {
		t.Fatalf("page one: %+v %v", first, err)
	}
	if err := store.Delete(ctx, "p1", actor, first.Artifacts[0].ID, first.Artifacts[0].Version, ""); err != nil {
		t.Fatal(err)
	}
	rest, err := store.List(ctx, "p1", "owner-1", first.Next.ID, 100)
	if err != nil || len(rest.Artifacts) != 2 || rest.Next != nil {
		t.Fatal("deleting cursor skipped or duplicated records")
	}
	other, err := store.List(ctx, "p2", "", first.Next.ID, 100)
	if err != nil || len(other.Artifacts) != 0 {
		t.Fatal("cursor bypassed project boundary")
	}
	for _, limit := range []int{0, 101} {
		if _, err := store.List(ctx, "p1", "", "", limit); !errors.Is(err, pg.ErrInvalidArtifact) {
			t.Fatal("unbounded page accepted")
		}
	}
	if _, err := store.List(ctx, "p1", "", "bad", 1); !errors.Is(err, pg.ErrInvalidArtifact) {
		t.Fatal("bad cursor accepted")
	}
	if _, err := store.List(ctx, "p1", strings.Repeat("s", 1025), "", 1); !errors.Is(err, pg.ErrInvalidArtifact) {
		t.Fatal("oversized owner accepted")
	}
}
