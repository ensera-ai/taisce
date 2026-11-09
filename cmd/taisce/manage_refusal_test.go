// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// Every operator command that speaks the management surface hands the surface's refusal to the
// operator, with its status and code, rather than printing a success line or nothing. A credential
// the surface does not know is the refusal every command can be made to meet; what each command
// prints when it is accepted is held by TestTheOperatorCommandsSpeakTheManagementSurfaceWhenConfigured.
func TestEveryOperatorCommandSurfacesTheManagementSurfacesRefusal(t *testing.T) {
	ctx := context.Background()
	h := newIngestHarness(t)
	credentials := credential.NewStore(h.pool, string(migrate.ControlSchema))
	operator, _, err := credentials.IssueOperator(ctx, "cmd-manage-refusals")
	if err != nil {
		t.Fatal(err)
	}
	manage := httptest.NewServer(api.NewManagementServer(credentials, api.ManagementStores{
		Projects: pg.NewProjectStore(h.pool, h.schema),
		Provision: func(ctx context.Context, scope string) error {
			return migrate.ProvisionScope(ctx, h.pool, h.schema.String(), scope)
		},
		Observations: pg.NewObservationStore(h.pool),
		Audit:        pg.NewAuditStore(h.pool, h.schema),
		Refusals:     pg.NewRefusalStore(h.pool, h.schema),
		Eraser:       pg.NewEraser(h.pool),
	}, h.schema, nil).Handler())
	defer manage.Close()
	t.Setenv(envManageAPI, manage.URL)

	// Accepted first, so the listing can show a suspended project the way an operator reads one.
	t.Setenv(envOperatorToken, operator)
	for _, args := range [][]string{{"project", "create", "cli_paused"}, {"project", "suspend", "cli_paused"}} {
		if _, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), args) }); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	listed, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), []string{"project", "list"}) })
	if err != nil || !strings.Contains(listed, "cli_paused") || !strings.Contains(listed, "[suspended]") {
		t.Fatalf("a suspended project is not listed as suspended: %q %v", listed, err)
	}

	t.Setenv(envOperatorToken, "tsk_"+strings.Repeat("x", 43))
	for _, args := range [][]string{
		{"project", "create", "cli_refused"},
		{"project", "suspend", "cli_refused"},
		{"project", "resume", "cli_refused"},
		{"project", "retention", "cli_refused", "30"},
		{"project", "list"},
		{"credential", "issue", "app", "--project", "cli_refused"},
		{"credential", "list", "--project", "cli_refused"},
		{"credential", "revoke", uuid.NewString()},
		{"formation", "parked", "--project", "cli_refused", "--after", "3"},
		{"audit", "seal"},
		{"audit", "verify"},
	} {
		out, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), args) })
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Errorf("%v with an unknown operator credential: err=%v out=%q", args, err, out)
		}
		if strings.Contains(out, "created") || strings.Contains(out, "revoked") || strings.Contains(out, "token:") {
			t.Errorf("%v printed a success line after a refusal: %q", args, out)
		}
	}
}
