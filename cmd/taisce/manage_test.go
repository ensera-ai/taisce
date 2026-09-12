// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// captureStdout runs fn with stdout redirected, because the operator commands print for a person
// and a script, not for a logger.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := fn()
	os.Stdout = old
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out), runErr
}

// The manage role serves the management surface on its own listener with the administrative
// connection, and nothing else: no memory route answers there, and the door opens for an operator
// credential only.
func TestTheManageRoleServesTheManagementSurfaceAndNothingElse(t *testing.T) {
	ctx := context.Background()
	env := runtimeConfiguration(t)
	env[envRole] = roleManage
	// The manage role holds the administrative connection; the serving identities cannot read
	// the registry, which is the point.
	env[envAdminDSN] = os.Getenv("TAISCE_TEST_DSN")
	env[envManageAddr] = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	withEnv(t, env)
	runCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan error, 1)
	go func() { stopped <- run(runCtx, quiet()) }()
	defer func() {
		cancel()
		select {
		case err := <-stopped:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("the manage role did not stop")
		}
	}()
	base := "http://" + env[envManageAddr]
	if !reachable(base+"/health", 5*time.Second) {
		t.Fatal("the management surface did not start")
	}
	pool, err := pgxpool.New(ctx, os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	schema, _ := pg.NewSchema(env[envSchema])
	credentials := credential.NewStore(pool, string(migrate.ControlSchema))
	operator, _, err := credentials.IssueOperator(ctx, "cmd-manage-role")
	if err != nil {
		t.Fatal(err)
	}
	project, _, err := credentials.Issue(ctx, "cmd-manage-project", "p1")
	if err != nil {
		t.Fatal(err)
	}
	post := func(path, token string) int {
		req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if status := post(api.ManagementPrefix+"/projects/list", operator); status != http.StatusOK {
		t.Fatalf("an operator credential must open the management surface, got %d", status)
	}
	if status := post(api.ManagementPrefix+"/projects/list", project); status != http.StatusUnauthorized {
		t.Fatalf("a project credential must be refused at the management door, got %d", status)
	}
	if status := post(api.ManagementPrefix+"/projects/list", ""); status != http.StatusUnauthorized {
		t.Fatalf("no credential must be refused, got %d", status)
	}
	if status := post("/v1/recalls", project); status != http.StatusNotFound {
		t.Fatalf("the manage role serves no memory route, got %d", status)
	}
	if err := probeCommand(ctx, []string{"--manage"}); err != nil {
		t.Fatalf("the management listener must answer its probe: %v", err)
	}
	// The portal's routes do not exist unless it is switched on.
	if resp, err := http.Get(base + api.PortalPrefix + "/login"); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the portal must be absent by default, got %v %v", resp, err)
	}
	t.Setenv(envPortal, "on")
	if resp, err := http.Get(base + api.PortalPrefix + "/login"); err != nil {
		t.Fatal(err)
	} else if handler := managementHandler(api.NewManagementServer(credentials, api.ManagementStores{Audit: pg.NewAuditStore(pool, schema)}, schema, nil), nil); handler == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a running listener does not grow the portal after the fact, got %d", resp.StatusCode)
	}
	on := httptest.NewServer(managementHandler(api.NewManagementServer(credentials, api.ManagementStores{Audit: pg.NewAuditStore(pool, schema), Projects: pg.NewProjectStore(pool, schema), Observations: pg.NewObservationStore(pool)}, schema, nil), nil))
	defer on.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if resp, err := client.Get(on.URL + api.PortalPrefix + "/"); err != nil || resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("switched on, the portal sends a browser to sign in, got %v %v", resp, err)
	}
	if err := probeCommand(ctx, []string{"--manage", "--worker", "extra"}); err == nil {
		t.Fatal("probe accepts no positional arguments")
	}
}

// The operator commands speak the management surface when it is configured, print the same lines
// the direct path prints, and refuse to speak it without an operator credential.
func TestTheOperatorCommandsSpeakTheManagementSurfaceWhenConfigured(t *testing.T) {
	ctx := context.Background()
	h := newIngestHarness(t)
	credentials := credential.NewStore(h.pool, string(migrate.ControlSchema))
	operator, _, err := credentials.IssueOperator(ctx, "cmd-manage-cli")
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
	t.Setenv(envOperatorToken, "")
	if err := dispatch(ctx, quiet(), []string{"project", "list"}); err == nil || !strings.Contains(err.Error(), envOperatorToken) {
		t.Fatalf("the surface must not be spoken without an operator credential, got %v", err)
	}
	t.Setenv(envOperatorToken, operator)
	run := func(args ...string) string {
		t.Helper()
		out, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), args) })
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	if out := run("project", "create", "cli_lc"); !strings.Contains(out, `project "cli_lc" created`) {
		t.Fatalf("unexpected output %q", out)
	}
	if out := run("project", "list"); !strings.Contains(out, "cli_lc") || !strings.Contains(out, "retention=indefinite") {
		t.Fatalf("unexpected output %q", out)
	}
	issued := run("credential", "issue", "app", "--project", "cli_lc", "--read-only")
	if !strings.Contains(issued, "reaches project cli_lc with read_only access") || !strings.Contains(issued, "token: tsk_") {
		t.Fatalf("unexpected output %q", issued)
	}
	id := regexp.MustCompile(`credential app \(([0-9a-f-]+)\)`).FindStringSubmatch(issued)
	if id == nil {
		t.Fatalf("no credential id in %q", issued)
	}
	if out := run("credential", "list", "--project", "cli_lc"); !strings.Contains(out, id[1]) || strings.Contains(out, strings.TrimSpace(strings.Split(strings.Split(issued, "token: ")[1], "\n")[0])) {
		t.Fatalf("the listing names the credential and never the token: %q", out)
	}
	if out := run("credential", "revoke", id[1]); !strings.Contains(out, "revoked") {
		t.Fatalf("unexpected output %q", out)
	}
	if out := run("credential", "list", "--project", "cli_lc"); !strings.Contains(out, "[revoked") {
		t.Fatalf("a revoked credential is listed as revoked: %q", out)
	}
	if out := run("project", "suspend", "cli_lc"); !strings.Contains(out, "suspended") {
		t.Fatalf("unexpected output %q", out)
	}
	if out := run("project", "resume", "cli_lc"); !strings.Contains(out, "resumed") {
		t.Fatalf("unexpected output %q", out)
	}
	// The same lines the direct path prints, and the same failure: a script trusting the exit status
	// must not pass a broken ledger because it asked over HTTP.
	if out := run("audit", "seal"); !strings.Contains(out, "sealed ") && !strings.Contains(out, "nothing to seal") {
		t.Fatalf("the seal is not reported as the direct path reports it: %q", out)
	}
	if out := run("audit", "verify"); !strings.HasPrefix(out, "verified: ") || strings.Contains(out, "{") {
		t.Fatalf("the verification is not reported as the direct path reports it: %q", out)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.audit_entry DISABLE TRIGGER audit_no_update`)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.audit_entry SET magnitude = magnitude + 999
		 WHERE entry_id = (SELECT min(entry_id) FROM {schema}.audit_entry)`)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.audit_entry ENABLE TRIGGER audit_no_update`)); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), []string{"audit", "verify"}) })
	if err == nil || !strings.HasPrefix(out, "FAILED: ") {
		t.Fatalf("a tampered ledger verified over the management surface: %v %q", err, out)
	}
	// Refusals reach the person as the surface's own code and message.
	if _, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), []string{"project", "create", "not a name!"}) }); err == nil || !strings.Contains(err.Error(), "invalid_project") {
		t.Fatalf("a refusal must carry the surface's code, got %v", err)
	}
	// Retention is a governance decision, so it is on the surface beside suspension, and both paths
	// print the same line.
	if out := run("project", "retention", "cli_lc", "30"); !strings.Contains(out, "keeps new turns for 30 days") {
		t.Fatalf("unexpected output %q", out)
	}
	if out := run("project", "retention", "cli_lc", "indefinite"); !strings.Contains(out, "keeps new turns for indefinite") {
		t.Fatalf("unexpected output %q", out)
	}
	if _, err := captureStdout(t, func() error {
		return dispatch(ctx, quiet(), []string{"project", "retention", "cli_lc", "0"})
	}); err == nil {
		t.Fatal("a retention of zero was accepted")
	}

	// Formation's operations are on the surface too: with no database configured, they still
	// answer, in the shape the direct path prints.
	t.Setenv(envAdminDSN, "")
	t.Setenv(envMemoryDSN, "")
	var page pg.ParkedPage
	if out := run("formation", "parked", "--project", "cli_lc", "--limit", "5"); json.Unmarshal([]byte(out), &page) != nil || len(page.Items) != 0 {
		t.Fatalf("the parked page over the surface: %q", out)
	}
	if _, err := captureStdout(t, func() error {
		return dispatch(ctx, quiet(), []string{"formation", "unpark", "--project", "cli_lc", uuid.NewString()})
	}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("unparking a turn that is not parked must carry the surface's refusal, got %v", err)
	}
	for _, bad := range [][]string{{"project"}, {"project", "create"}, {"project", "explode"}, {"credential"}, {"credential", "issue"}, {"credential", "issue", "app", "stray"},
		{"credential", "issue", "app", "--nope"}, {"credential", "list", "--nope"}, {"credential", "explode"}, {"credential", "revoke"}, {"audit"}, {"audit", "burn"},
		{"formation"}, {"formation", "parked"}, {"formation", "unpark", "--project", "cli_lc", "not-a-uuid"}} {
		if _, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), bad) }); err == nil {
			t.Fatalf("%v must be refused", bad)
		}
	}
}

// A management surface that cannot be reached is an error a person can read, not a hang.
func TestAnUnreachableManagementSurfaceIsAnError(t *testing.T) {
	t.Setenv(envManageAPI, "http://127.0.0.1:1")
	t.Setenv(envOperatorToken, "tsk_x")
	if err := dispatch(context.Background(), quiet(), []string{"project", "list"}); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("expected an unreachable error, got %v", err)
	}
}

// An operator credential is minted over the administrative connection, and it opens the operator
// door: the one operation that exists before the surface can be spoken to.
func TestOperatorIssueMintsAnOperatorCredentialOverTheAdministrativeConnection(t *testing.T) {
	ctx := context.Background()
	env := complete(t)
	withEnv(t, env)
	out, err := captureStdout(t, func() error { return dispatch(ctx, quiet(), []string{"operator", "issue", "cmd-ops"}) })
	if err != nil || !strings.Contains(out, "operator credential cmd-ops") || !strings.Contains(out, "token: tsk_") {
		t.Fatalf("unexpected %v %q", err, out)
	}
	token := strings.TrimSpace(strings.Split(strings.Split(out, "token: ")[1], "\n")[0])
	pool, err := pgxpool.New(ctx, env[envMemoryDSN])
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := credential.NewStore(pool, string(migrate.ControlSchema)).ResolveOperator(ctx, token); err != nil {
		t.Fatalf("the minted token must open the operator door: %v", err)
	}
	if err := dispatch(ctx, quiet(), []string{"operator"}); err == nil {
		t.Fatal("operator without issue must be refused")
	}
}

// A surface that answers with something other than its own refusal shape is reported by status,
// so a proxy's error page does not read as the surface's word.
func TestAManagementAnswerThatIsNotARefusalIsReportedByStatus(t *testing.T) {
	odd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	}))
	defer odd.Close()
	t.Setenv(envManageAPI, odd.URL)
	t.Setenv(envOperatorToken, "tsk_x")
	err := dispatch(context.Background(), quiet(), []string{"project", "list"})
	if err == nil || !strings.Contains(err.Error(), "answered 502") {
		t.Fatalf("expected the status to be reported, got %v", err)
	}
}

// The manage role holds the administrative connection or does not start. Falling back to the
// memory login, as one-shot commands do, would start a surface that then fails on every operation.
func TestTheManageRoleRefusesToStartWithoutTheAdministrativeConnection(t *testing.T) {
	env := runtimeConfiguration(t)
	env[envRole] = roleManage
	env[envAdminDSN] = ""
	env[envManageAddr] = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	withEnv(t, env)
	if err := run(context.Background(), quiet()); err == nil || !strings.Contains(err.Error(), envAdminDSN) {
		t.Fatalf("the manage role started, or refused without naming %s: %v", envAdminDSN, err)
	}
}
