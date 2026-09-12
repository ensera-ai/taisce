// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// An attempt is counted when the turn is claimed, so an attempt that takes the worker with it still
// costs one. Counted on failure instead, a turn that reliably killed its worker came
// back with the same count for ever and was never parked.
func TestAnAttemptIsCountedWhenTheTurnIsClaimed(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "form_claimed")
	observations := pg.NewObservationStore(pool)
	stored, err := observations.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "I work at Ensera."}))
	if err != nil {
		t.Fatal(err)
	}
	attempts := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, schema.SQL(`SELECT formation_attempts FROM {schema}.observation WHERE observation_id=$1`), stored.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The claim itself, with nothing written afterwards: this is the worker dying mid-extraction.
	if _, found, err := observations.NextUnformed(ctx, schema, "p1", time.Minute); err != nil || !found {
		t.Fatalf("the turn was not offered: found=%v err=%v", found, err)
	}
	if got := attempts(); got != 1 {
		t.Fatalf("a claimed turn must cost an attempt, got %d", got)
	}
	// And it is not offered again immediately, because the claim stamped the time the backoff
	// measures from.
	if _, found, err := observations.NextUnformed(ctx, schema, "p1", time.Minute); err != nil || found {
		t.Fatalf("a turn claimed a moment ago was offered again: found=%v err=%v", found, err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.observation SET formation_failed_at = now() - interval '1 hour' WHERE observation_id=$1`), stored.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := observations.NextUnformed(ctx, schema, "p1", time.Minute); err != nil || !found {
		t.Fatalf("the turn was not offered after its wait: found=%v err=%v", found, err)
	}
	if got := attempts(); got != 2 {
		t.Fatalf("the second claim must cost an attempt too, got %d", got)
	}
	// Recording why it failed does not count it a second time.
	if _, err := observations.RecordFormationFailure(ctx, schema, stored.ID, "the provider refused"); err != nil {
		t.Fatal(err)
	}
	if got := attempts(); got != 2 {
		t.Fatalf("writing the reason must not count another attempt, got %d", got)
	}
}

// A model that names one ordinary end and one the store cannot keep, from the same message.
type longNameModel struct{}

func (longNameModel) Propose(_ context.Context, message domain.Message, _ domain.Ontology) ([]extract.Proposal, error) {
	i := strings.Index(message.Content, "work at Ensera")
	if i < 0 {
		return nil, nil
	}
	quote := message.Content[i : i+len("work at Ensera")]
	return []extract.Proposal{
		{Subject: "user", Predicate: "works_at", Polarity: domain.PolarityAsserted, Tense: domain.TensePresent,
			Object: strings.Repeat("a", 5000), Statement: "The user works at a place with an unkeepable name.",
			Confidence: 0.9, Quote: quote},
		{Subject: "user", Predicate: "works_at", Polarity: domain.PolarityAsserted, Tense: domain.TensePresent,
			Object: "Ensera", Statement: "The user works at Ensera.", Confidence: 0.9, Quote: quote},
	}, nil
}

// A name the store cannot keep is a refusal, not a failed turn: the rest of the turn forms, the
// claim is recorded with its reason, and nothing is retried.
func TestANameTheStoreCannotKeepIsARefusalNotAFailedTurn(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "form_namelimit")
	observations := pg.NewObservationStore(pool)
	worker := workerWith(t, pool, schema, longNameModel{}, formationPolicyForTest())
	if _, err := observations.Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "I work at Ensera."})); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Drain(ctx, schema, "p1"); err != nil {
		t.Fatalf("the turn must form despite the name: %v", err)
	}
	var parked, unformed int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FILTER (WHERE parked_at IS NOT NULL), count(*) FILTER (WHERE formed_at IS NULL) FROM {schema}.observation WHERE scope='p1'`)).Scan(&parked, &unformed); err != nil {
		t.Fatal(err)
	}
	if parked != 0 || unformed != 0 {
		t.Fatalf("the turn was parked or left unformed: parked=%d unformed=%d", parked, unformed)
	}
	var reason string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT reason FROM {schema}.rejected_claim WHERE scope='p1'`)).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != domain.ReasonEntityNameLimit {
		t.Fatalf("the refusal was recorded as %q", reason)
	}
	var kept int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact WHERE scope='p1'`)).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Fatalf("the claim beside it must still be a fact, got %d", kept)
	}
}

// formationPolicyForTest is the default policy with the waits shortened, so a test that forms a
// turn does not sit through a production backoff.
func formationPolicyForTest() formation.Policy {
	policy := formation.DefaultPolicy()
	policy.RetryAfter = 10 * time.Millisecond
	return policy
}
