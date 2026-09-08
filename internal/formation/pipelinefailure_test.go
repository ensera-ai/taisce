// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestFailedAndEmptyExtractionAttemptsKeepTheirPinnedConfiguration(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "formation_pipeline_failure")
	var calls atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"claims\":[]}"}}]}`))
	}))
	defer server.Close()
	v, err := pg.LoadVocabulary(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	makeFormer := func(name string) *formation.Former {
		return formation.NewFormer(obs, facts, extract.NewWith(inference.NewModel(inference.Config{Endpoint: server.URL, Model: name}), v))
	}
	a, b := makeFormer("a"), makeFormer("b")
	source, err := obs.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "quiet"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Form(ctx, schema, source); err == nil {
		t.Fatal("failed model succeeded")
	}
	if _, err := b.Form(ctx, schema, source); !errors.Is(err, pg.ErrExtractionChanged) || calls.Load() != 1 {
		t.Fatal("failed attempt forgot its configuration", err)
	}
	fail.Store(false)
	if r, err := a.Form(ctx, schema, source); err != nil || r.FactsAsserted != 0 || r.ClaimsRejected != 0 {
		t.Fatal("original retry failed", err)
	}
	if _, err := b.Form(ctx, schema, source); !errors.Is(err, pg.ErrExtractionChanged) || calls.Load() != 2 {
		t.Fatal("empty result lost its pin", err)
	}
	var unformed bool
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT formed_at IS NULL FROM {schema}.observation WHERE observation_id=$1`), source.ID).Scan(&unformed); err != nil || !unformed {
		t.Fatal("pin was mistaken for formation completion", err)
	}
}
