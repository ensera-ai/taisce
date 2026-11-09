// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// The vocabulary verb extends the relation vocabulary over the management surface, and refuses
// every way of asking it wrongly.
//
// The refusals matter as much as the success. One of them is the whole reason this verb has no path
// over the database connection: a vocabulary change is recorded against the operator who made it,
// and without a surface to speak to there is no operator to record, so the command says which two
// variables to set rather than making an unattributable change.
//
// Nothing here checks a declaration before sending it: the name's shape and the closed axes are
// constraints on the table, and what the command is held to is passing the refusal back with the
// surface's own code — which is what tells an operator which of the five to fix.
func TestTheVocabularyCommandExtendsTheVocabularyOverTheSurfaceAndCarriesItsRefusals(t *testing.T) {
	ctx := context.Background()
	h := newIngestHarness(t)
	credentials := credential.NewStore(h.pool, string(migrate.ControlSchema))
	operator, _, err := credentials.IssueOperator(ctx, "cmd-vocabulary-cli")
	if err != nil {
		t.Fatal(err)
	}
	manage := httptest.NewServer(api.NewManagementServer(credentials, api.ManagementStores{
		Projects:     pg.NewProjectStore(h.pool, h.schema),
		Observations: pg.NewObservationStore(h.pool),
		Audit:        pg.NewAuditStore(h.pool, h.schema),
		Refusals:     pg.NewRefusalStore(h.pool, h.schema),
		Eraser:       pg.NewEraser(h.pool),
		Vocabulary:   pg.NewVocabularyStore(h.pool, h.schema),
	}, h.schema, nil).Handler())
	defer manage.Close()

	// Without the surface configured the verb is refused, and the refusal names what to set. The
	// other operator verbs fall back to the database here; this one has nothing to fall back to.
	t.Setenv(envManageAPI, "")
	t.Setenv(envOperatorToken, "")
	err = dispatch(ctx, quiet(), []string{"vocabulary", "list"})
	if err == nil || !strings.Contains(err.Error(), envManageAPI) || !strings.Contains(err.Error(), envOperatorToken) {
		t.Fatalf("the verb must name both variables it needs, got %v", err)
	}

	t.Setenv(envManageAPI, manage.URL)
	t.Setenv(envOperatorToken, operator)
	run := func(args ...string) string {
		t.Helper()
		out, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), args) })
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	declared := []string{"vocabulary", "add", "contracted_to", "-project", "p1",
		"-semantic-type", "identity", "-cardinality", "many", "-object-kind", "organisation",
		"-description", "An organisation the subject is contracted to."}
	out := run(declared...)
	if !strings.Contains(out, "contracted_to") || !strings.Contains(out, "organisation") {
		t.Fatalf("the relation was not reported as the table holds it: %q", out)
	}
	// The one consequence an operator has to know about, said where they will see it.
	if !strings.Contains(out, "extractor identity") {
		t.Fatalf("adding a relation did not say that the extractor identity changes: %q", out)
	}
	if out := run("vocabulary", "list", "-project", "p1"); !strings.Contains(out, "contracted_to") ||
		!strings.Contains(out, "works_at") || !strings.Contains(out, "[event]") {
		t.Fatalf("the listing is not the vocabulary: %q", out)
	}

	// A second declaration of the same relation is the surface's conflict, carried through.
	if _, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), declared) }); err == nil ||
		!strings.Contains(err.Error(), "predicate_conflict") {
		t.Fatalf("a duplicate must carry the surface's code, got %v", err)
	}
	// A declaration the table refuses comes back naming the constraint that refused it.
	spelled := append([]string{}, declared...)
	spelled[2] = "Contracted To"
	if _, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), spelled) }); err == nil ||
		!strings.Contains(err.Error(), "predicate_name_chk") {
		t.Fatalf("a refused name must carry the constraint that refused it, got %v", err)
	}
	// A declaration left out is refused by the column that required it, over the surface too.
	incomplete := append([]string{}, declared[:len(declared)-2]...)
	incomplete[2] = "missing_description"
	if _, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), incomplete) }); err == nil ||
		!strings.Contains(err.Error(), "description") {
		t.Fatalf("a missing declaration must name the column that required it, got %v", err)
	}

	for _, bad := range [][]string{
		{"vocabulary"},
		{"vocabulary", "add"},
		{"vocabulary", "explode"},
		{"vocabulary", "add", "contracted_to", "stray"},
		{"vocabulary", "add", "contracted_to", "-nope"},
		{"vocabulary", "list", "-nope"},
	} {
		if _, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), bad) }); err == nil {
			t.Fatalf("%v must be refused", bad)
		}
	}
}
