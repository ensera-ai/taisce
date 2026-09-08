// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

type rebuildModel struct{}

func (rebuildModel) ExtractionIdentity() string { return "rebuild-test-model" }

func (rebuildModel) Propose(ctx context.Context, message domain.Message, vocabulary domain.Ontology) ([]extract.Proposal, error) {
	return scriptedModel{}.Propose(ctx, message, vocabulary)
}

type rebuildFailModel struct{}

func (rebuildFailModel) ExtractionIdentity() string { return "rebuild-failing-model" }

func (rebuildFailModel) Propose(context.Context, domain.Message, domain.Ontology) ([]extract.Proposal, error) {
	return nil, errors.New("synthetic provider failure")
}

type rebuildManyModel struct{}

func (rebuildManyModel) ExtractionIdentity() string { return "rebuild-many-model" }

func (rebuildManyModel) Propose(_ context.Context, message domain.Message, _ domain.Ontology) ([]extract.Proposal, error) {
	proposals := make([]extract.Proposal, 0, pg.MaxGenerationClaims+1)
	for i := 0; i < pg.MaxGenerationClaims+1; i++ {
		proposals = append(proposals, extract.Proposal{
			Subject: "I", Predicate: "works_at", Object: fmt.Sprintf("Company-%d", i), Statement: message.Content,
			Quote: message.Content, Polarity: domain.PolarityAsserted, Tense: domain.TensePresent,
		})
	}
	return proposals, nil
}

func TestRebuilderPublishesAndReplaysOneSourceWithoutAnotherModelCall(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	schema := tenant(t, p, "formation_rebuild")
	vocabulary, err := pg.LoadOntology(ctx, p, schema)
	if err != nil {
		t.Fatal(err)
	}
	observations := pg.NewObservationStore(p)
	stored, err := observations.Append(ctx, schema, domain.Turn{
		Scope:         "p1",
		DataSubjectID: "subject-1",
		OccurredAt:    time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC),
		Messages: []domain.Message{{
			Ordinal: 0,
			Role:    domain.RoleUser,
			Content: "I work at Ensera and commutes by bicycle.",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	rebuilder := formation.NewRebuilder(pg.NewFactStore(p), extract.New(rebuildModel{}, vocabulary))
	first, err := rebuilder.Rebuild(ctx, schema, "p1", stored.ID, "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002")
	if err != nil {
		t.Fatal(err)
	}
	if first.Messages != 1 || first.Asserted != 1 || first.Rejected != 1 || first.Replayed {
		t.Fatalf("unexpected first generation result: %+v", first)
	}
	var formed time.Time
	if err := p.QueryRow(ctx, schema.SQL(`SELECT formed_at FROM {schema}.observation WHERE observation_id=$1::uuid`), stored.ID).Scan(&formed); err != nil {
		t.Fatal(err)
	}
	if formed.IsZero() {
		t.Fatal("published source was not marked formed")
	}

	former := formation.NewFormer(observations, pg.NewFactStore(p), extract.New(rebuildModel{}, vocabulary))
	replayed, err := former.Form(ctx, schema, domain.Observation{
		ID:            stored.ID,
		Scope:         "p1",
		DataSubjectID: "subject-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.MessagesRead != first.Messages || replayed.FactsAsserted != first.Asserted ||
		replayed.ClaimsRejected != first.Rejected || replayed.ClaimsRetracted != first.Retracted ||
		replayed.ClaimsRefusedByRole != first.RefusedRole {
		t.Fatalf("former did not replay the published generation: report=%+v generation=%+v", replayed, first)
	}

	second, err := rebuilder.Rebuild(ctx, schema, "p1", stored.ID, "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.ID != first.ID || second.Asserted != first.Asserted || second.Rejected != first.Rejected {
		t.Fatalf("published generation was not replayed: first=%+v second=%+v", first, second)
	}
}

func TestRebuilderDoesNotPublishWhenInferenceFails(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	schema := tenant(t, p, "formation_rebuild_failure")
	observations := pg.NewObservationStore(p)
	stored, err := observations.Append(ctx, schema, domain.Turn{
		Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now().UTC(),
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	vocabulary, err := pg.LoadOntology(ctx, p, schema)
	if err != nil {
		t.Fatal(err)
	}
	rebuilder := formation.NewRebuilder(pg.NewFactStore(p), extract.New(rebuildFailModel{}, vocabulary))
	if _, err := rebuilder.Rebuild(ctx, schema, "p1", stored.ID, "00000000-0000-0000-0000-000000000011", "00000000-0000-0000-0000-000000000012"); !errors.Is(err, formation.ErrGenerationInference) {
		t.Fatalf("provider failure was not classified: %v", err)
	}
	var generations int
	if err := p.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact_generation`)).Scan(&generations); err != nil {
		t.Fatal(err)
	}
	if generations != 0 {
		t.Fatal("failed inference published a generation")
	}
}

func TestRebuilderBoundsModelOutputBeforeCutover(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	schema := tenant(t, p, "formation_rebuild_bound")
	observations := pg.NewObservationStore(p)
	stored, err := observations.Append(ctx, schema, domain.Turn{
		Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now().UTC(),
		Messages: []domain.Message{{Ordinal: 0, Role: domain.RoleUser, Content: "I work at Ensera"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	vocabulary, err := pg.LoadOntology(ctx, p, schema)
	if err != nil {
		t.Fatal(err)
	}
	rebuilder := formation.NewRebuilder(pg.NewFactStore(p), extract.New(rebuildManyModel{}, vocabulary))
	if _, err := rebuilder.Rebuild(ctx, schema, "p1", stored.ID, uuid.NewString(), uuid.NewString()); !errors.Is(err, pg.ErrGenerationLimit) {
		t.Fatalf("oversized model output was not bounded: %v", err)
	}
	var generations int
	if err := p.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact_generation`)).Scan(&generations); err != nil {
		t.Fatal(err)
	}
	if generations != 0 {
		t.Fatal("oversized output published a generation")
	}
}
