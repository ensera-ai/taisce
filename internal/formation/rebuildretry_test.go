// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

type interruptedRebuildModel struct {
	calls  int
	during func()
}

func (*interruptedRebuildModel) ExtractionIdentity() string { return "interrupted-rebuild-test" }

func (m *interruptedRebuildModel) Propose(ctx context.Context, message domain.Message, ontology domain.Ontology) ([]extract.Proposal, error) {
	m.calls++
	if m.during != nil {
		m.during()
	}
	return scriptedModel{}.Propose(ctx, message, ontology)
}

func TestRebuilderRetriesBookkeepingWithoutRepeatingInference(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	schema := tenant(t, p, "rebuild_finish_retry")
	stored, err := pg.NewObservationStore(p).Append(ctx, schema, domain.Turn{
		Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now().UTC(),
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "I work at Ensera and commutes by bicycle."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	vocabulary, err := pg.LoadOntology(ctx, p, schema)
	if err != nil {
		t.Fatal(err)
	}
	model := &interruptedRebuildModel{}
	rebuilder := formation.NewRebuilder(pg.NewFactStore(p), extract.New(model, vocabulary))
	key, actor := uuid.NewString(), uuid.NewString()
	if _, err := p.Exec(ctx, schema.SQL(`CREATE FUNCTION {schema}.refuse_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'finish interrupted'; END $$;
		CREATE TRIGGER refuse_finish BEFORE UPDATE OF formed_at ON {schema}.observation FOR EACH ROW EXECUTE FUNCTION {schema}.refuse_finish()`)); err != nil {
		t.Fatal(err)
	}
	first, err := rebuilder.Rebuild(ctx, schema, "p1", stored.ID, key, actor)
	if err == nil || first.ID == "" {
		t.Fatalf("expected committed publication and failed bookkeeping: %+v %v", first, err)
	}
	if _, err := p.Exec(ctx, schema.SQL(`DROP TRIGGER refuse_finish ON {schema}.observation`)); err != nil {
		t.Fatal(err)
	}
	retry, err := rebuilder.Rebuild(ctx, schema, "p1", stored.ID, key, actor)
	if err != nil || !retry.Replayed || retry.ID != first.ID || model.calls != 1 {
		t.Fatalf("bookkeeping retry repeated inference or publication: %+v calls=%d err=%v", retry, model.calls, err)
	}
	var formed bool
	if err := p.QueryRow(ctx, schema.SQL(`SELECT formed_at IS NOT NULL FROM {schema}.observation WHERE observation_id=$1`), stored.ID).Scan(&formed); err != nil || !formed {
		t.Fatalf("retry did not finish: %v", err)
	}
}

func TestRebuilderCannotRecreateSourceErasedDuringInference(t *testing.T) {
	ctx := context.Background()
	p := testPool(t)
	schema := tenant(t, p, "rebuild_erased_preparation")
	stored, err := pg.NewObservationStore(p).Append(ctx, schema, domain.Turn{
		Scope: "p1", DataSubjectID: "subject-1", OccurredAt: time.Now().UTC(),
		Messages: []domain.Message{{Role: domain.RoleUser, Content: "I work at Ensera and commutes by bicycle."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	vocabulary, err := pg.LoadOntology(ctx, p, schema)
	if err != nil {
		t.Fatal(err)
	}
	model := &interruptedRebuildModel{during: func() {
		result, err := pg.NewEraser(p).Erase(ctx, schema, "p1", "subject-1", "requested")
		if err != nil || !result.Clean() {
			t.Fatalf("erasure failed during model work: %+v %v", result, err)
		}
	}}
	rebuilder := formation.NewRebuilder(pg.NewFactStore(p), extract.New(model, vocabulary))
	if _, err := rebuilder.Rebuild(ctx, schema, "p1", stored.ID, uuid.NewString(), uuid.NewString()); !errors.Is(err, pg.ErrGenerationSource) {
		t.Fatalf("erased source was published: %v", err)
	}
	for _, table := range []string{"observation", "fact", "fact_generation", "source_extraction"} {
		var count int
		if err := p.QueryRow(ctx, schema.SQL("SELECT count(*) FROM {schema}."+table)).Scan(&count); err != nil || count != 0 {
			t.Fatalf("erased %s was recreated: count=%d err=%v", table, count, err)
		}
	}
	if _, err := rebuilder.Rebuild(ctx, schema, "p1", stored.ID, uuid.NewString(), uuid.NewString()); !errors.Is(err, pg.ErrGenerationSource) || model.calls != 1 {
		t.Fatalf("erased source was sent back to inference: calls=%d err=%v", model.calls, err)
	}
}
