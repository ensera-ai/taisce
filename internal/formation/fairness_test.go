// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// busyModel records the order messages reach the model, and keeps one project busy: every time it
// is asked about a message from the busy project it stores another turn there, the way a project
// written to as fast as it forms never runs out of work.
type busyModel struct {
	mu     sync.Mutex
	order  []string
	more   int
	append func(n int)
}

func (m *busyModel) Propose(_ context.Context, message domain.Message, _ domain.Ontology) ([]extract.Proposal, error) {
	m.mu.Lock()
	project, _, _ := strings.Cut(message.Content, ":")
	m.order = append(m.order, project)
	feed := project == "busy" && m.more > 0
	if feed {
		m.more--
	}
	n := m.more
	m.mu.Unlock()
	if feed {
		m.append(n)
	}
	return nil, nil
}

func (m *busyModel) seen() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.order...)
}

// A project with a deep backlog does not hold the worker while other projects wait.
//
// A worker took one project and stayed until that project had nothing left to form. A deep backlog
// — or a project written to as fast as it formed, which never empties — held the worker for the
// whole of it, and every other project's turns sat stored and unformed behind it with nothing wrong
// with them. With as many busy projects as workers, the rest did not form at all.
//
// A worker now forms a bounded number of turns for a project and moves on, so every project with
// work is reached in every pass. The project names are chosen so the busy one sorts first, which is
// the order that used to starve the others.
func TestAProjectWithADeepBacklogDoesNotHoldTheWorkerWhileOthersWait(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "fm_fairness")
	observations := pg.NewObservationStore(pool)
	for _, scope := range []string{"busy", "quiet_one", "quiet_two"} {
		if err := migrate.ProvisionScope(ctx, pool, schema.String(), scope); err != nil {
			t.Fatalf("provision %s: %v", scope, err)
		}
	}
	store := func(scope, content string) {
		t.Helper()
		turn := turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: scope + ": " + content})
		turn.Scope = scope
		if _, err := observations.Append(ctx, schema, turn); err != nil {
			t.Fatalf("append to %s: %v", scope, err)
		}
	}
	// The busy project arrives first and deepest, so it is first by name and by age.
	for i := 0; i < 5; i++ {
		store("busy", fmt.Sprintf("turn %d", i))
	}
	store("quiet_one", "the only turn")
	store("quiet_two", "the only turn")

	model := &busyModel{more: 40}
	model.append = func(n int) { store("busy", fmt.Sprintf("arrived while forming, %d to go", n)) }
	policy := impatient()
	policy.TurnsPerScope = 2
	driver := formation.NewDriver(workerWith(t, pool, schema, model, policy),
		observations, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, policy, quietLogger())

	pass, err := driver.Once(ctx)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	// One pass: a slice of the busy project, then each quiet one — not forty-five turns of the busy
	// project first.
	if got, want := strings.Join(model.seen(), " "), "busy busy quiet_one quiet_two"; got != want {
		t.Fatalf("the model was asked about, in order: %s\nwant: %s", got, want)
	}
	if pass.Formed != 4 {
		t.Fatalf("the pass formed %d turns, want 4: two of the busy project's and one for each of the others", pass.Formed)
	}
	if !pass.More {
		t.Fatal("the pass left turns waiting and does not say so, so the driver would wait out its interval with work in hand")
	}
	for _, scope := range []string{"quiet_one", "quiet_two"} {
		fresh, err := observations.Freshness(ctx, schema, scope)
		if err != nil {
			t.Fatal(err)
		}
		if !fresh.HasFormed {
			t.Errorf("%s has not formed after a pass in which the worker reached it", scope)
		}
	}

	// And the busy project is not starved in turn: a driver left running gets through all of it,
	// without waiting out its interval between slices. The interval here is an hour, so finishing
	// at all means the driver went straight back to work.
	policy.Interval = time.Hour
	running, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = formation.NewDriver(workerWith(t, pool, schema, model, policy),
			observations, pg.NewRetentionStore(pool, schema), pg.NewAuditStore(pool, schema), schema, policy, quietLogger()).Run(running)
	}()
	deadline := time.Now().Add(30 * time.Second)
	for {
		waiting, err := observations.ScopesWithBacklog(ctx, schema)
		if err != nil {
			t.Fatal(err)
		}
		if len(waiting) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("turns are still waiting in %v after 30s; a driver with work in hand is waiting for its interval", waiting)
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	<-done
}
