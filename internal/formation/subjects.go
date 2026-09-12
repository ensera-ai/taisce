// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation

import (
	"context"
	"fmt"

	"github.com/ensera-ai/taisce/internal/community"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/report"
)

// SubjectStore is what the subject pass needs from the database.
//
// An interface here for the reason the recall store's is one: what the pass DOES is separable from
// where the rows are, and the ordering rules can be read without reading SQL.
type SubjectStore interface {
	Graph(ctx context.Context, scope string) ([]community.Edge, error)
	Replace(ctx context.Context, scope string, h community.Hierarchy) error
	Unwritten(ctx context.Context, scope string, limit int, writer string) ([]pg.Stored, error)
	Children(ctx context.Context, scope, parentID string) ([]pg.Stored, error)
	Material(ctx context.Context, scope string, entityIDs []string) ([]string, []report.Fact, error)
	Report(ctx context.Context, scope, communityID string) (report.Report, bool, error)
	Write(ctx context.Context, scope, communityID string, r report.Report, c report.Context, writer string) error
}

// ReportBudget is how much material one report is written from, in characters.
//
// PROVISIONAL, like the clustering parameters, and for the same reason. What it decides is when
// a parent stops being described from its own facts and starts being described from its children's
// reports — so too small makes every parent a summary of summaries, and too large sends more material
// than a model attends to and pays for it on every rebuild.
const ReportBudget = 12000

// Subjects forms the subjects in a scope and writes what has not been written.
//
// # Why this is a pass rather than a read
//
// Partitioning a scope reads every current fact in it and writing a report is a model call. A question
// that triggered either would make one caller pay for the whole scope, and would put inference on the
// read path — which is the cost anchoring already refuses by resolving names with a lookup.
//
// # Why forming and writing are one pass and not two
//
// Replacing the partition deletes the reports of communities that no longer exist, by cascade. A pass
// that formed without writing would leave a scope with subjects and nothing said about them until the
// next tick, and a thematic question in between would find nothing and be unable to say why.
type Subjects struct {
	store SubjectStore
	// writer is the model hop. Nil is valid and means reports are not written: a deployment with no
	// model still forms subjects, which is what the portal needs to show the shape of a memory, and
	// the alternative is a pass that fails on every tick in a configuration that is otherwise fine.
	writer *report.Writer
}

// writerIdentity is what wrote a report, or the empty string when nothing can say.
//
// A model that cannot identify itself gets no identity rather than a made-up one: an unidentified
// writer must not be evidence that two reports were written under the same rules, which is the same
// position extraction takes about an unidentified proposer.
// WriterIdentity is what this pass stamps on the reports it writes, and what the driver asks with
// when it looks for projects owing one.
func (s *Subjects) WriterIdentity() string {
	if s.writer == nil {
		return ""
	}
	if identified, ok := s.writer.Model().(interface{ ReportIdentity() string }); ok {
		return identified.ReportIdentity()
	}
	return ""
}

// NewSubjects builds the pass. A nil writer forms subjects and writes no reports.
func NewSubjects(store SubjectStore, writer *report.Writer) *Subjects {
	return &Subjects{store: store, writer: writer}
}

// SubjectPass is what one run did, so the driver can log something a person can act on.
type SubjectPass struct {
	Entities    int
	Communities int
	Written     int
	Failed      int
}

// Run partitions a scope and writes the reports that are missing.
//
// `limit` bounds the model calls one pass makes. A scope whose partition produced two hundred subjects
// would otherwise hold the worker for two hundred model calls on its first tick, while every other
// scope waits — so the work is spread across ticks and the unwritten ones are picked up next time.
func (s *Subjects) Run(ctx context.Context, scope string, limit int) (SubjectPass, error) {
	edges, err := s.store.Graph(ctx, scope)
	if err != nil {
		return SubjectPass{}, err
	}
	if len(edges) == 0 {
		// No relation between two entities means no graph and no subjects. Not an error: it is every
		// scope's first hour, and replacing an empty partition with an empty one is work for nothing.
		return SubjectPass{}, nil
	}

	graph, err := community.NewGraph(edges)
	if err != nil {
		return SubjectPass{}, fmt.Errorf("build the graph: %w", err)
	}
	hierarchy := community.Build(graph)
	if err := s.store.Replace(ctx, scope, hierarchy); err != nil {
		return SubjectPass{}, err
	}

	out := SubjectPass{Entities: graph.Order(), Communities: len(hierarchy.Communities)}
	if s.writer == nil {
		return out, nil
	}

	unwritten, err := s.store.Unwritten(ctx, scope, limit, s.WriterIdentity())
	if err != nil {
		return out, err
	}
	for _, c := range unwritten {
		if err := s.write(ctx, scope, c); err != nil {
			// One community that could not be described does not stop the others. A model refusing
			// one context and a provider being down look identical from here, and the second is
			// transient — so the pass records it and the next tick tries again, which is what
			// `Unwritten` selecting on the absence of a report already arranges.
			out.Failed++
			continue
		}
		out.Written++
	}
	return out, nil
}

func (s *Subjects) write(ctx context.Context, scope string, c pg.Stored) error {
	entities, facts, err := s.store.Material(ctx, scope, c.Members)
	if err != nil {
		return err
	}
	if len(facts) == 0 {
		// A community with no relation between its own members is a group the partition made from
		// edges that all left it. There is nothing to write a report from, and asking a model to
		// describe a list of names produces a paragraph that sounds like a subject.
		return fmt.Errorf("community %s has no relations among its members", c.ID)
	}

	owned, reports, err := s.children(ctx, scope, c)
	if err != nil {
		return err
	}
	material := report.BuildContext(entities, facts, owned, reports, ReportBudget)
	written, err := s.writer.Write(ctx, material)
	if err != nil {
		return err
	}
	return s.store.Write(ctx, scope, c.ID, written, material, s.WriterIdentity())
}

// children collects what a parent may substitute: for each child, the facts that belong to it and the
// report already written about it.
//
// A child with no report cannot be substituted — replacing its facts with nothing would remove the
// subject rather than compress it — so it is offered without one and the roll-up leaves it alone. That
// happens when a child's own writing failed on an earlier tick, and it is the reason the pass writes
// deepest first: by the time a parent is reached, its children usually have reports.
func (s *Subjects) children(ctx context.Context, scope string, parent pg.Stored) (
	map[string][]report.Fact, map[string]report.Report, error) {

	kids, err := s.store.Children(ctx, scope, parent.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(kids) == 0 {
		return nil, nil, nil
	}
	owned := make(map[string][]report.Fact, len(kids))
	reports := make(map[string]report.Report, len(kids))
	for _, kid := range kids {
		_, facts, err := s.store.Material(ctx, scope, kid.Members)
		if err != nil {
			return nil, nil, err
		}
		owned[kid.ID] = facts
		written, ok, err := s.store.Report(ctx, scope, kid.ID)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			reports[kid.ID] = written
		}
	}
	return owned, reports, nil
}
