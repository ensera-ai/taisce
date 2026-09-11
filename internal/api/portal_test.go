// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// The portal serves nothing to a browser that has not signed in, opens for an operator credential
// and no other, shows every panel from the tables that exist with the numbers those tables hold,
// leaves every panel on the ledger, and signs a revoked operator out at the registry.
func TestThePortalShowsNothingBeforeAnOperatorSignsInAndEveryPanelIsATableOnTheLedger(t *testing.T) {
	m := newManaged(t, "api_portal")
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.Handle("/", m.manageServer.Handler())
	api.NewPortal(m.manageServer).Mount(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) (int, string, string) {
		t.Helper()
		resp, err := browser.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body := readAll(t, resp)
		return resp.StatusCode, resp.Header.Get("Location"), body
	}
	post := func(path string, form url.Values) (int, string) {
		t.Helper()
		resp, err := browser.PostForm(server.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_ = readAll(t, resp)
		return resp.StatusCode, resp.Header.Get("Location")
	}
	// Something to show: a turn, a refusal, an erasure, a parked turn.
	m.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, http.StatusCreated, nil)
	m.form(t)
	m.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "subject-1", "reason": "the portal test"}, http.StatusOK, nil)
	m.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-2", "messages": []map[string]any{{"role": "user", "content": "I cycle to work."}}}, http.StatusCreated, nil)
	if _, err := m.pool.Exec(ctx, m.schema.SQL(`WITH refused AS (
		INSERT INTO {schema}.rejected_claim (rejected_claim_id, scope, source_observation_id, source_ordinal, predicate, statement, quote, reason, extractor_version, data_subject_id)
		SELECT gen_random_uuid(), 'p1', observation_id, 0, 'commutes_by', 'The user cycles.', 'I cycle to Ensera', 'unmapped_relation', 'test', 'subject-2' FROM {schema}.observation WHERE scope='p1' AND data_subject_id='subject-2' LIMIT 1
		RETURNING rejected_claim_id, source_observation_id)
		INSERT INTO {schema}.projection_dependency (source_observation_id, scope, projection_kind, projection_id, data_subject_id)
		SELECT source_observation_id, 'p1', 'rejected_claim', rejected_claim_id::text, 'subject-2' FROM refused`)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pool.Exec(ctx, m.schema.SQL(`UPDATE {schema}.observation SET parked_at=now(), formation_attempts=2 WHERE scope='p1' AND formed_at IS NULL`)); err != nil {
		t.Fatal(err)
	}
	// Nothing before signing in: every panel sends the browser to the form, and the form shows no data.
	for _, path := range []string{"/portal/", "/portal/ledger", "/portal/projects/p1"} {
		if status, location, body := get(path); status != http.StatusSeeOther || location != "/portal/login" || strings.Contains(body, "p1") {
			t.Fatalf("%s before signing in answered %d %q", path, status, location)
		}
	}
	if status, _, body := get("/portal/login"); status != http.StatusOK || !strings.Contains(body, `name="token"`) || strings.Contains(body, "subject-1") || strings.Contains(body, "Ensera") {
		t.Fatalf("the sign-in form shows a form and nothing else, got %d", status)
	}
	// A project credential and a stranger are refused alike; an operator is let in.
	for _, refused := range []string{m.token, "tsk_stranger", ""} {
		if status, _ := post("/portal/login", url.Values{"token": {refused}}); status != http.StatusUnauthorized {
			t.Fatalf("token %q must be refused at the portal, got %d", refused, status)
		}
		if status, _, _ := get("/portal/"); status != http.StatusSeeOther {
			t.Fatal("a refused sign-in must leave no session")
		}
	}
	if status, location := post("/portal/login", url.Values{"token": {m.operator}}); status != http.StatusSeeOther || location != "/portal/" {
		t.Fatalf("an operator must be signed in and sent home, got %d %q", status, location)
	}
	// Home: every project with its watermark, from the project table and the log.
	status, _, home := get("/portal/")
	if status != http.StatusOK || !strings.Contains(home, `/portal/projects/p1`) || !strings.Contains(home, "Not recorded") {
		t.Fatalf("home must list p1 and say what is not recorded, got %d: %s", status, home)
	}
	// The project: refusal counts without the words, the receipt, the parked turn, the credential.
	status, _, project := get("/portal/projects/p1")
	if status != http.StatusOK {
		t.Fatalf("project page answered %d", status)
	}
	for _, want := range []string{"unmapped_relation", "commutes_by", "the portal test", "segment", "test-api_portal"} {
		if !strings.Contains(project, want) {
			t.Fatalf("the project page must show %q: %s", want, project)
		}
	}
	for _, never := range []string{"I cycle to Ensera", "The user cycles.", "Dublin"} {
		if strings.Contains(project, never) {
			t.Fatalf("the project page must not show %q: a refusal is somebody's words", never)
		}
	}
	if strings.Contains(project, m.token) || strings.Contains(project, m.operator) {
		t.Fatal("a token must never appear on a page")
	}
	var parkedShown int
	if err := m.pool.QueryRow(ctx, m.schema.SQL(`SELECT count(*) FROM {schema}.observation WHERE scope='p1' AND parked_at IS NOT NULL`)).Scan(&parkedShown); err != nil {
		t.Fatal(err)
	}
	if parkedShown != 1 || !strings.Contains(project, "Parked turns") {
		t.Fatalf("expected the one parked turn on the page, table holds %d", parkedShown)
	}
	if status, _, _ := get("/portal/projects/not%20a%20name"); status != http.StatusNotFound {
		t.Fatalf("a name that is not a project is not found, got %d", status)
	}
	// The ledger page verifies the chain.
	status, _, ledger := get("/portal/ledger")
	if status != http.StatusOK || !strings.Contains(ledger, "Verified") {
		t.Fatalf("the ledger page must verify, got %d: %s", status, ledger)
	}
	// Every panel is on the ledger with the operator as principal.
	entries, err := pg.NewAuditStore(m.pool, m.schema).Recent(ctx, 300)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.PrincipalKind == domain.PrincipalOperator {
			seen[e.Operation] = true
		}
	}
	for _, op := range []string{domain.AuditProjectList, domain.AuditFormationStatus, domain.AuditRefusalSummary, domain.AuditErasureList, domain.AuditFormationParked, domain.AuditCredentialList, domain.AuditAuditVerify} {
		if !seen[op] {
			t.Fatalf("panel %s left no ledger row, recorded %v", op, seen)
		}
	}
	// A revoked operator is signed out by the registry on the next request; signing out is explicit too.
	credentials := credential.NewStore(m.pool, string(migrate.ControlSchema))
	listed, _ := credentials.List(ctx, "", 500)
	for _, c := range listed {
		// The registry outlives a test schema: only this run's live operator is revoked here.
		if c.Name == "ops-api_portal" && c.RevokedAt == nil {
			if err := credentials.Revoke(ctx, c.CredentialID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if status, location, _ := get("/portal/"); status != http.StatusSeeOther || location != "/portal/login" {
		t.Fatalf("a revoked operator must be sent to sign in, got %d %q", status, location)
	}
	if status, location := post("/portal/logout", nil); status != http.StatusSeeOther || location != "/portal/login" {
		t.Fatalf("signing out sends the browser to the form, got %d %q", status, location)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String()
}
