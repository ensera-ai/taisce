// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestDirectObservationCallersCannotBypassMessageAdmission(t *testing.T) {
	store := pg.NewObservationStore(nil)
	for _, tc := range []struct {
		turn domain.Turn
		want error
	}{
		{turnOf(make([]domain.Message, domain.MaxObservationMessages+1)...), domain.ErrMessageCount},
		{turnOf(domain.Message{Role: domain.RoleUser, Content: strings.Repeat("x", domain.MaxMessageBytes+1)}), domain.ErrMessageSize},
	} {
		if _, err := store.Append(context.Background(), "unused", tc.turn); !errors.Is(err, tc.want) {
			t.Fatalf("direct write reached storage: %v", err)
		}
	}
}

func TestConcurrentStoreRetriesRemainAtomicWithoutTheHTTPGate(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "retry_without_admission")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewObservationStore(pool)
	turn := turnOf(domain.Message{Role: domain.RoleUser, Content: "same logical observation"})
	type result struct {
		observation domain.Observation
		err         error
	}
	out := make(chan result, 24)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, _, err := store.AppendIdempotent(ctx, schema, turn, "5b3148b7-a660-48a5-b75c-7754d3e6ce86")
			out <- result{o, err}
		}()
	}
	wg.Wait()
	close(out)
	var id string
	for r := range out {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if id == "" {
			id = r.observation.ID
		}
		if r.observation.ID != id || r.observation.LogOffset != 0 {
			t.Fatalf("inconsistent receipt: %+v", r.observation)
		}
	}
	for _, table := range []string{"observation", "turn_message", "chunk", "observation_retry"} {
		var n int
		if err := pool.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+table)).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s=%d %v", table, n, err)
		}
	}
}
