// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"strings"
	"testing"
)

// The saved UUID belongs to the source, not the losable chunk projection. Its dedicated unique
// index both prevents ambiguous references and supports direct lookup without a generation scan.
func TestMessageReferenceSurvivesProjectionLossAndHasAnUnambiguousIndex(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "message_reference_identity")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	source, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: "source survives its projection"}, domain.Message{Ordinal: 1, Role: domain.RoleAssistant, Content: "neighbor"}))
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT chunk_id::text FROM {schema}.turn_message WHERE observation_id=$1::uuid AND ordinal=0`), source.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.chunk WHERE source_observation_id=$1::uuid`), source.ID); err != nil {
		t.Fatal(err)
	}
	store := pg.NewCitationStore(pool, schema)
	message, err := store.Message(ctx, "p1", pg.MessageWindowQuery{ChunkID: id, Limit: 4096})
	if err != nil || !message.Complete || message.SourceID != source.ID || message.Text != "source survives its projection" {
		t.Fatalf("source reference lost %+v %v", message, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.turn_message SET chunk_id=$1::uuid WHERE observation_id=$2::uuid AND ordinal=1`), id, source.ID); err == nil {
		t.Fatal("authoritative message reference became ambiguous")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan=off"); err != nil {
		t.Fatal(err)
	}
	var plan string
	if err := tx.QueryRow(ctx, schema.SQL(`EXPLAIN (FORMAT JSON) SELECT m.content FROM {schema}.turn_message m JOIN {schema}.observation o USING(observation_id) WHERE o.scope=$1 AND m.chunk_id=$2::uuid`), "p1", id).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "message_chunk_lookup_unique") {
		t.Fatalf("UUID lookup index ineligible: %s", plan)
	}
	// This establishes index eligibility on a small fixture, not an operating-envelope measurement.
	if _, err := store.Message(ctx, "p2", pg.MessageWindowQuery{ChunkID: id, Limit: 4}); !errors.Is(err, pg.ErrMessageNotFound) {
		t.Fatal("foreign source exposed")
	}
	if _, err := store.Message(ctx, "invalid-scope", pg.MessageWindowQuery{ChunkID: id, Limit: 4}); !errors.Is(err, pg.ErrInvalidMessageWindow) {
		t.Fatal("invalid scope accepted")
	}
	empty, err := store.Message(ctx, "p1", pg.MessageWindowQuery{ChunkID: id, ByteStart: message.MessageBytes, Limit: 4, ExpectedDigest: message.ContentDigest})
	if err != nil || empty.Text != "" || empty.NextByteStart != nil || empty.ByteEnd != message.MessageBytes {
		t.Fatalf("end window %+v %v", empty, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if result, err := store.Message(cancelled, "p1", pg.MessageWindowQuery{ChunkID: id, Limit: 4}); err == nil || result.Text != "" {
		t.Fatal("cancelled source read returned content")
	}
}
