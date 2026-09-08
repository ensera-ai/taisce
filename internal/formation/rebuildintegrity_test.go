// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Gates establish overlap without relying on scheduler sleeps. Model output is fixed because
// this test measures database ownership and serialization, not extraction quality.
type integrityGateModel struct {
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (*integrityGateModel) ExtractionIdentity() string { return "rebuild-test-model" }

func (m *integrityGateModel) Propose(ctx context.Context, message domain.Message, vocabulary domain.Ontology) ([]extract.Proposal, error) {
	m.once.Do(func() { close(m.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.release:
		return scriptedModel{}.Propose(ctx, message, vocabulary)
	}
}

func TestFormationRebuildAndErasurePreserveProjectRelationships(t *testing.T) {
	for _, eraseFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("erase_before_publication_%t", eraseFirst), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			pool := testPool(t)
			schema := tenant(t, pool, fmt.Sprintf("joint_rebuild_integrity_%t", eraseFirst))
			if err := migrate.ProvisionScope(ctx, pool, schema.String(), "p2"); err != nil {
				t.Fatal(err)
			}
			vocabulary, err := pg.LoadOntology(ctx, pool, schema)
			if err != nil {
				t.Fatal(err)
			}
			observations := pg.NewObservationStore(pool)
			facts := pg.NewFactStore(pool)
			plain := formation.NewFormer(observations, facts, extract.New(rebuildModel{}, vocabulary))
			var target domain.Observation
			for _, owner := range []struct{ project, subject string }{{"p1", "erased"}, {"p1", "survivor"}, {"p2", "erased"}} {
				stored, err := observations.Append(ctx, schema, domain.Turn{Scope: owner.project, DataSubjectID: owner.subject,
					OccurredAt: time.Now().UTC(), Messages: []domain.Message{{Role: domain.RoleUser, Content: "I work at Ensera"}}})
				if err != nil {
					t.Fatal(err)
				}
				observation := domain.Observation{ID: stored.ID, Scope: owner.project, DataSubjectID: owner.subject}
				if owner.project == "p1" && owner.subject == "erased" {
					target = observation
					continue
				}
				if _, err := plain.Form(ctx, schema, observation); err != nil {
					t.Fatal(err)
				}
			}
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			formModel := &integrityGateModel{entered: make(chan struct{}), release: release}
			rebuildModel := &integrityGateModel{entered: make(chan struct{}), release: release}
			former := formation.NewFormer(observations, facts, extract.New(formModel, vocabulary))
			jobs := pg.NewRebuildJobStore(pool)
			runner := formation.NewProjectRebuilder(formation.NewRebuilder(facts, extract.New(rebuildModel, vocabulary)), jobs)
			key, actor := uuid.NewString(), uuid.NewString()
			formed, rebuilt := make(chan error, 1), make(chan error, 1)
			go func() { _, err := former.Form(ctx, schema, target); formed <- err }()
			go func() { _, err := runner.RunPage(ctx, schema, "p1", key, actor, 10); rebuilt <- err }()
			for _, entered := range []chan struct{}{formModel.entered, rebuildModel.entered} {
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("both paths did not reach inference", ctx.Err())
				}
			}
			erased := make(chan error, 1)
			erase := func() {
				result, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "erased", "requested")
				if err == nil && !result.Clean() {
					err = fmt.Errorf("unclean erasure: %+v", result)
				}
				erased <- err
			}
			if eraseFirst {
				erase()
			} else {
				go erase()
			}
			unblock()
			if err := <-erased; err != nil {
				t.Fatal(err)
			}
			formErr := <-formed
			if eraseFirst && formErr == nil {
				t.Fatal("formation accepted an erased source")
			}
			if formErr != nil && !errors.Is(formErr, pg.ErrExtractionChanged) {
				var databaseError *pgconn.PgError
				if !errors.As(formErr, &databaseError) || databaseError.Code != "23503" {
					t.Fatalf("unexpected formation failure: %v", formErr)
				}
			}
			if err := <-rebuilt; err != nil && !errors.Is(err, pg.ErrGenerationConflict) {
				t.Fatalf("rebuild failed unexpectedly: %v", err)
			}
			// A refused stale snapshot remains restartable after the competing writes finish.
			job, err := runner.RunPage(ctx, schema, "p1", key, actor, 10)
			if err != nil || job.Status != "completed" {
				t.Fatalf("joint work stranded rebuild: %+v %v", job, err)
			}
			var residue, survivors, foreign int
			if err := pool.QueryRow(ctx, schema.SQL(`SELECT
				(SELECT count(*) FROM {schema}.observation WHERE scope='p1' AND data_subject_id='erased')+
				(SELECT count(*) FROM {schema}.fact WHERE scope='p1' AND data_subject_id='erased')+
				(SELECT count(*) FROM {schema}.fact_evidence WHERE source_observation_id=$1::uuid)+
				(SELECT count(*) FROM {schema}.source_extraction WHERE source_observation_id=$1::uuid)+
				(SELECT count(*) FROM {schema}.fact_generation WHERE source_observation_id=$1::uuid),
				(SELECT count(*) FROM {schema}.fact WHERE scope='p1' AND data_subject_id='survivor' AND upper_inf(known)),
				(SELECT count(*) FROM {schema}.fact WHERE scope='p2' AND data_subject_id='erased' AND upper_inf(known))`), target.ID).Scan(&residue, &survivors, &foreign); err != nil {
				t.Fatal(err)
			}
			if residue != 0 || survivors != 1 || foreign != 1 {
				t.Fatalf("ownership changed: residue=%d survivors=%d foreign=%d", residue, survivors, foreign)
			}
			var dangling int
			if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.fact f
				LEFT JOIN {schema}.entity s ON s.scope=f.scope AND s.entity_id=f.subject_entity_id
				LEFT JOIN {schema}.entity o ON o.scope=f.scope AND o.entity_id=f.object_entity_id
				WHERE (f.subject_entity_id IS NOT NULL AND s.entity_id IS NULL) OR (f.object_entity_id IS NOT NULL AND o.entity_id IS NULL)`)).Scan(&dangling); err != nil || dangling != 0 {
				t.Fatalf("dangling graph: %d %v", dangling, err)
			}
		})
	}
}
