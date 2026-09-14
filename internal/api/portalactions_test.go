// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

// acting is a signed-in browser against a portal that can act.
type acting struct {
	*managed
	server  *httptest.Server
	browser *http.Client
}

func signedInPortal(t *testing.T, tenant string) *acting {
	t.Helper()
	m := newManaged(t, tenant)
	mux := http.NewServeMux()
	mux.Handle("/", m.manageServer.Handler())
	api.NewPortal(m.manageServer).Mount(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := browser.PostForm(server.URL+"/portal/login", url.Values{"token": {m.operator}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign-in answered %d", resp.StatusCode)
	}
	return &acting{managed: m, server: server, browser: browser}
}

// suspended and revoked read the registry directly, because what a page reports and what the
// database holds are two different claims and the second is the one that matters here.
func (a *acting) suspended(t *testing.T) bool {
	t.Helper()
	var at *string
	if err := a.pool.QueryRow(context.Background(), a.schema.SQL(
		`SELECT suspended_at::text FROM {schema}.project WHERE scope='p1'`)).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at != nil
}

func (a *acting) revoked(t *testing.T, id string) bool {
	t.Helper()
	var at *string
	if err := a.pool.QueryRow(context.Background(),
		`SELECT revoked_at::text FROM control.credential WHERE credential_id=$1::uuid`, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at != nil
}

var guardPattern = regexp.MustCompile(`name="guard" value="([0-9a-f]+)"`)

// page fetches a page and returns its body and the session guard rendered into it.
func (a *acting) page(t *testing.T, path string) (string, string) {
	t.Helper()
	resp, err := a.browser.Get(a.server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	found := guardPattern.FindStringSubmatch(body)
	if found == nil {
		return body, ""
	}
	return body, found[1]
}

func (a *acting) act(t *testing.T, path string, form url.Values) int {
	t.Helper()
	resp, err := a.browser.PostForm(a.server.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_ = readAll(t, resp)
	return resp.StatusCode
}

// ── A portal action must prove it came from the portal ────────────────────────────────────────
//
// A request that changes something must prove it came from the page. The cookie is SameSite=Strict
// and the page's policy is `form-action 'self'`, so a cross-site post is refused twice before
// reaching the guard — but both of those are promises the browser makes, and a value the attacker
// cannot read does not depend on the browser behaving.
func TestThePortalRefusesAnActionThatDidNotComeFromThePortal(t *testing.T) {
	a := signedInPortal(t, "portal_guard")
	_, guard := a.page(t, "/portal/")
	if guard == "" {
		t.Fatal("no guard was rendered into the page, so no form can act")
	}

	for name, form := range map[string]url.Values{
		"no guard at all":   {"project": {"p1"}},
		"an empty guard":    {"project": {"p1"}, "guard": {""}},
		"somebody else's":   {"project": {"p1"}, "guard": {strings.Repeat("a", 64)}},
		"the right length":  {"project": {"p1"}, "guard": {strings.Repeat("0", len(guard))}},
		"a truncated guard": {"project": {"p1"}, "guard": {guard[:len(guard)-1]}},
	} {
		t.Run(name, func(t *testing.T) {
			if status := a.act(t, "/portal/actions/suspend", form); status != http.StatusBadRequest {
				t.Fatalf("answered %d, want 400", status)
			}
			if a.suspended(t) {
				t.Fatal("a request that did not come from the portal suspended a project")
			}
		})
	}

	// And the real one works, so the refusals above are the guard and not the action being broken.
	if status := a.act(t, "/portal/actions/suspend", url.Values{"project": {"p1"}, "guard": {guard}}); status != http.StatusSeeOther {
		t.Fatalf("the genuine request answered %d", status)
	}
	if !a.suspended(t) {
		t.Fatal("the genuine request did not suspend the project")
	}
}

// ── #53 ──────────────────────────────────────────────────────────────────────────────────────
//
// A request that did not come from the page is refused before its action runs, and the ledger names
// the action it attempted. The ledger answers "who tried what". A cross-site attempt recorded under
// an unrelated read, as it once was under formation.status, is the one row nobody finds.
//
// Walked over every action, and forged both ways the guard check refuses: no guard, and a wrong one.
func TestARequestThatDidNotComeFromThePortalIsRecordedUnderTheActionItAttempted(t *testing.T) {
	a := signedInPortal(t, "portal_forged_ledger")
	ctx := context.Background()
	_, guard := a.page(t, "/portal/")
	refused := func(operation string) int {
		t.Helper()
		var n int
		if err := a.pool.QueryRow(ctx, a.schema.SQL(
			`SELECT count(*) FROM {schema}.audit_entry WHERE principal_kind='operator' AND operation=$1 AND outcome='refused'`),
			operation).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	actions := map[string]string{
		"/portal/actions/create":  domain.AuditProjectCreate,
		"/portal/actions/suspend": domain.AuditProjectSuspend,
		"/portal/actions/resume":  domain.AuditProjectResume,
		"/portal/actions/unpark":  domain.AuditFormationUnpark,
		"/portal/actions/issue":   domain.AuditCredentialIssue,
		"/portal/actions/revoke":  domain.AuditCredentialRevoke,
		"/portal/actions/seal":    domain.AuditAuditSeal,
	}
	if len(actions) != api.PortalActionCount() {
		t.Fatalf("%d actions are forged and the portal declares %d; an action nobody forges is one whose "+
			"refusal nobody has watched recorded", len(actions), api.PortalActionCount())
	}
	for path, operation := range actions {
		for how, form := range map[string]url.Values{
			"no guard":    {"project": {"p1"}},
			"wrong guard": {"project": {"p1"}, "guard": {strings.Repeat("0", len(guard))}},
		} {
			before := refused(operation)
			if status := a.act(t, path, form); status != http.StatusBadRequest {
				t.Fatalf("%s with %s answered %d, want 400", path, how, status)
			}
			if got := refused(operation) - before; got != 1 {
				t.Fatalf("%s with %s left %d refused %s rows, want 1", path, how, got, operation)
			}
		}
	}
	if n := refused(domain.AuditFormationStatus); n != 0 {
		t.Fatalf("%d refusals were recorded as a formation status read", n)
	}
}

// Every action is a management operation that already had a ledger name, and every one of them
// records the operator as principal. Walked as a set rather than checked one at a time, so an
// action added later without a row fails here rather than in production.
func TestEveryPortalActionRecordsTheOperatorOnTheLedger(t *testing.T) {
	a := signedInPortal(t, "portal_ledger")
	ctx := context.Background()
	_, guard := a.page(t, "/portal/")

	// A parked turn to retry, and a credential to revoke.
	a.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}, http.StatusCreated, nil)
	if _, err := a.pool.Exec(ctx, a.schema.SQL(
		`UPDATE {schema}.observation SET parked_at=now(), formation_attempts=2 WHERE scope='p1' AND formed_at IS NULL`)); err != nil {
		t.Fatal(err)
	}
	var parked string
	if err := a.pool.QueryRow(ctx, a.schema.SQL(
		`SELECT observation_id::text FROM {schema}.observation WHERE scope='p1' AND parked_at IS NOT NULL LIMIT 1`)).Scan(&parked); err != nil {
		t.Fatal(err)
	}
	doomed, doomedGrant, err := credential.NewStore(a.pool, string(migrate.ControlSchema)).
		Issue(ctx, "portal-revoke-me", "p1")
	if err != nil {
		t.Fatal(err)
	}
	_ = doomed

	actions := []struct {
		name string
		path string
		form url.Values
	}{
		{domain.AuditProjectCreate, "/portal/actions/create", url.Values{"project": {"portal_made"}}},
		{domain.AuditProjectSuspend, "/portal/actions/suspend", url.Values{"project": {"p1"}}},
		{domain.AuditProjectResume, "/portal/actions/resume", url.Values{"project": {"p1"}}},
		{domain.AuditFormationUnpark, "/portal/actions/unpark", url.Values{"project": {"p1"}, "observation": {parked}}},
		{domain.AuditCredentialIssue, "/portal/actions/issue", url.Values{"project": {"p1"}, "name": {"portal-issued"}}},
		{domain.AuditCredentialRevoke, "/portal/actions/revoke", url.Values{"project": {"p1"},
			"id": {doomedGrant.CredentialID}, "confirm": {doomedGrant.CredentialID}}},
		{domain.AuditAuditSeal, "/portal/actions/seal", url.Values{}},
	}
	if len(actions) != api.PortalActionCount() {
		t.Fatalf("%d actions are exercised and the portal declares %d; an action nobody walks is one "+
			"whose ledger row nobody has watched", len(actions), api.PortalActionCount())
	}
	for _, action := range actions {
		form := action.form
		form.Set("guard", guard)
		if status := a.act(t, action.path, form); status != http.StatusSeeOther {
			t.Fatalf("%s answered %d", action.name, status)
		}
	}

	entries, err := pg.NewAuditStore(a.pool, a.schema).Recent(ctx, 300)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]domain.AuditEntry{}
	rows := map[string]int{}
	for _, entry := range entries {
		if entry.PrincipalKind == domain.PrincipalOperator && entry.Outcome == domain.OutcomeAllowed {
			seen[entry.Operation] = entry
			rows[entry.Operation]++
		}
	}
	for _, action := range actions {
		entry, ok := seen[action.name]
		if !ok {
			t.Fatalf("%s left no allowed row attributed to the operator", action.name)
		}
		if entry.Principal == "" {
			t.Fatalf("%s recorded no principal: %+v", action.name, entry)
		}
		// One request is one operation, so one row. A second row for the same request is an operation
		// counted twice on the overview and in the breakdown (#57).
		if rows[action.name] != 1 {
			t.Fatalf("%s left %d allowed rows for one request, want 1", action.name, rows[action.name])
		}
	}
}

// Revoking cannot be undone from the page, so it asks for the identifier to be typed back. A
// checkbox is a thing people tick; typing an identifier is a thing people only do on purpose.
func TestAnIrreversiblePortalActionIsRefusedWithoutItsTypedConfirmation(t *testing.T) {
	a := signedInPortal(t, "portal_confirm")
	ctx := context.Background()
	_, guard := a.page(t, "/portal/")
	_, grant, err := credential.NewStore(a.pool, string(migrate.ControlSchema)).Issue(ctx, "portal-keep-me", "p1")
	if err != nil {
		t.Fatal(err)
	}

	for name, confirm := range map[string]string{
		"nothing typed":      "",
		"whitespace":         "   ",
		"a different id":     uuid.NewString(),
		"nearly the id":      grant.CredentialID[:len(grant.CredentialID)-1],
		"the word confirm":   "confirm",
		"the project's name": "p1",
	} {
		t.Run(name, func(t *testing.T) {
			status := a.act(t, "/portal/actions/revoke", url.Values{"guard": {guard},
				"project": {"p1"}, "id": {grant.CredentialID}, "confirm": {confirm}})
			if status != http.StatusSeeOther {
				t.Fatalf("answered %d", status)
			}
			if a.revoked(t, grant.CredentialID) {
				t.Fatalf("the credential was revoked without its confirmation (%q)", confirm)
			}
		})
	}

	// Typed exactly, it goes.
	if status := a.act(t, "/portal/actions/revoke", url.Values{"guard": {guard},
		"project": {"p1"}, "id": {grant.CredentialID}, "confirm": {grant.CredentialID}}); status != http.StatusSeeOther {
		t.Fatalf("the confirmed revocation answered %d", status)
	}
	if !a.revoked(t, grant.CredentialID) {
		t.Fatal("the confirmed revocation did not revoke")
	}
}

// ── #52 ──────────────────────────────────────────────────────────────────────────────────────
//
// The Revoke form sits on one project's page, so it revokes only that project's keys. From p1's page:
//   - another project's key is refused and stays live;
//   - the signed-in operator's own key is refused, stays live, and the browser stays signed in;
//   - a key of p1 is revoked, which shows the refusals came from the scope and not from a form that
//     never works.
func TestThePortalRevokesOnlyKeysOfTheProjectWhosePageItIs(t *testing.T) {
	a := signedInPortal(t, "portal_revoke_scope")
	ctx := context.Background()
	_, guard := a.page(t, "/portal/")
	if err := migrate.ProvisionScope(ctx, a.pool, a.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	keys := credential.NewStore(a.pool, string(migrate.ControlSchema))
	_, ofP2, err := keys.Issue(ctx, "portal-other-project", "p2")
	if err != nil {
		t.Fatal(err)
	}
	_, ofP1, err := keys.Issue(ctx, "portal-this-project", "p1")
	if err != nil {
		t.Fatal(err)
	}
	signedIn, err := keys.ResolveOperator(ctx, a.operator)
	if err != nil {
		t.Fatal(err)
	}
	revocations := func(outcome string) int {
		t.Helper()
		var n int
		if err := a.pool.QueryRow(ctx, a.schema.SQL(
			`SELECT count(*) FROM {schema}.audit_entry WHERE principal_kind='operator' AND operation=$1 AND outcome=$2`),
			domain.AuditCredentialRevoke, outcome).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	revokeFromP1 := func(id string) string {
		t.Helper()
		if status := a.act(t, "/portal/actions/revoke", url.Values{"guard": {guard},
			"project": {"p1"}, "id": {id}, "confirm": {id}}); status != http.StatusSeeOther {
			t.Fatalf("revoke answered %d", status)
		}
		body, _ := a.page(t, "/portal/projects/p1")
		return body
	}
	refusedBefore, allowedBefore := revocations("refused"), revocations("allowed")

	if body := revokeFromP1(ofP2.CredentialID); !strings.Contains(body, "Not done.") {
		t.Fatal("the page did not refuse another project's key")
	}
	if a.revoked(t, ofP2.CredentialID) {
		t.Fatal("a form on p1's page revoked a key of p2")
	}

	if body := revokeFromP1(signedIn.CredentialID); !strings.Contains(body, "Not done.") {
		t.Fatal("the page did not refuse the operator's own key")
	}
	if a.revoked(t, signedIn.CredentialID) {
		t.Fatal("a form on a project's page revoked an operator key")
	}
	if _, stillSignedIn := a.page(t, "/portal/"); stillSignedIn == "" {
		t.Fatal("the browser was signed out by a revoke that was refused")
	}

	if body := revokeFromP1(ofP1.CredentialID); !strings.Contains(body, "Done.") || strings.Contains(body, "Not done.") {
		t.Fatal("the page did not revoke a key of the project whose page it is")
	}
	if !a.revoked(t, ofP1.CredentialID) {
		t.Fatal("p1's key was not revoked")
	}

	if refused := revocations("refused") - refusedBefore; refused != 2 {
		t.Fatalf("%d refused credential.revoke rows, want 2", refused)
	}
	if allowed := revocations("allowed") - allowedBefore; allowed != 1 {
		t.Fatalf("%d allowed credential.revoke rows, want 1", allowed)
	}
}

// The ops centre is not for reading memory. Every panel and every action is about the instance, the
// projects, the ledger and the keys — and an operator credential cannot reach a person's records at
// all, which is the boundary that lets somebody run this system without being able to read it.
func TestThePortalShowsNoMemoryAndCannotReachIt(t *testing.T) {
	a := signedInPortal(t, "portal_no_memory")
	a.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, http.StatusCreated, nil)
	a.form(t)

	for _, path := range []string{"/portal/", "/portal/ledger", "/portal/projects/p1"} {
		body, _ := a.page(t, path)
		for _, word := range []string{"Ensera", "Dublin", "I work at", "subject-1"} {
			if strings.Contains(body, word) {
				t.Fatalf("%s disclosed %q", path, word)
			}
		}
	}

	// And there is no route that would: the actions are the declared set, and a memory operation
	// is not among them. Asking for one is a 404 rather than a refusal, because it does not exist.
	for _, path := range []string{"/portal/actions/purge", "/portal/actions/recall",
		"/portal/actions/assert", "/portal/actions/erase", "/portal/actions/configure"} {
		resp, err := a.browser.PostForm(a.server.URL+path, url.Values{})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s answered %d, want 404: the portal must not grow a memory route", path, resp.StatusCode)
		}
	}
}

// The panel an operator opens the page for. Every number is an aggregate; none of it is about a
// person; and the connection headroom is there because a deployment which runs out
// of sessions loses writes, which this one found out the hard way.
func TestThePortalReportsHowTheInstanceIsRunning(t *testing.T) {
	a := signedInPortal(t, "portal_health")
	body, _ := a.page(t, "/portal/")
	for _, expected := range []string{"How it is running", "Formation backlog", "Headroom", "Connections"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("the health panel is missing %q", expected)
		}
	}
}

// Every action checks what it was given before it touches a store. A form field is a thing anybody
// can post; the page it came from does not make it safe, and every one of these values is used as a
// scope, an identifier or a redirect target. So each action is sent something it cannot act on, and
// each must refuse, change nothing, and leave a refused row — one refusal shape for every kind of bad
// input, so a guess at what exists learns nothing from which refusal came back.
func TestEveryPortalActionRefusesATargetItCannotActOn(t *testing.T) {
	a := signedInPortal(t, "portal_bad_targets")
	ctx := context.Background()
	_, guard := a.page(t, "/portal/")
	before := a.refusedRows(t)

	long := strings.Repeat("a", 64)
	cases := []struct {
		name, path string
		form       url.Values
	}{
		{"create with no name", "/portal/actions/create", url.Values{"project": {""}}},
		{"create with a capital", "/portal/actions/create", url.Values{"project": {"Project"}}},
		{"create starting with a digit", "/portal/actions/create", url.Values{"project": {"1project"}}},
		{"create with a hyphen", "/portal/actions/create", url.Values{"project": {"my-project"}}},
		{"create past the length", "/portal/actions/create", url.Values{"project": {long}}},
		{"suspend a bad name", "/portal/actions/suspend", url.Values{"project": {"not a project"}}},
		{"resume a bad name", "/portal/actions/resume", url.Values{"project": {"../p1"}}},
		{"unpark without an id", "/portal/actions/unpark", url.Values{"project": {"p1"}, "observation": {""}}},
		{"unpark with a bad id", "/portal/actions/unpark", url.Values{"project": {"p1"}, "observation": {"not-a-uuid"}}},
		{"unpark with an upper-case id", "/portal/actions/unpark", url.Values{"project": {"p1"},
			"observation": {strings.ToUpper(uuid.NewString())}}},
		{"issue with no label", "/portal/actions/issue", url.Values{"project": {"p1"}, "name": {""}}},
		{"issue with a long label", "/portal/actions/issue", url.Values{"project": {"p1"}, "name": {strings.Repeat("x", 129)}}},
		{"issue to a bad project", "/portal/actions/issue", url.Values{"project": {"P1"}, "name": {"ok"}}},
		{"revoke a bad id", "/portal/actions/revoke", url.Values{"project": {"p1"}, "id": {"nope"}, "confirm": {"nope"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form := c.form
			form.Set("guard", guard)
			if status := a.act(t, c.path, form); status != http.StatusSeeOther {
				t.Fatalf("answered %d", status)
			}
			body, _ := a.page(t, "/portal/")
			if !strings.Contains(body, "Not done.") {
				t.Fatalf("the page did not say the action was refused")
			}
			// One refusal for every kind of bad input: nothing about what the store said, and
			// nothing that would tell a caller which part of their guess was wrong.
			if !strings.Contains(body, "nothing was changed") {
				t.Fatalf("the refusal did not say nothing changed")
			}
		})
	}
	if after := a.refusedRows(t); after-before < len(cases) {
		t.Fatalf("%d refusals and %d refused ledger rows: a refused action must still be on the record",
			len(cases), after-before)
	}
	// Nothing was created, suspended or issued by any of it.
	if a.suspended(t) {
		t.Fatal("a refused action suspended the project")
	}
	var made int
	if err := a.pool.QueryRow(ctx, a.schema.SQL(
		`SELECT count(*) FROM {schema}.project WHERE scope <> 'p1'`)).Scan(&made); err != nil {
		t.Fatal(err)
	}
	if made != 0 {
		t.Fatalf("a refused create made %d projects", made)
	}
}

// Retrying a turn that is not parked is not an error — it was already retried, by another operator
// or by the driver — and saying so is more useful than a success message about work that did not
// happen.
func TestRetryingATurnThatIsNotParkedSaysSoRatherThanClaimingSuccess(t *testing.T) {
	a := signedInPortal(t, "portal_not_parked")
	ctx := context.Background()
	_, guard := a.page(t, "/portal/")
	// A real turn that exists and is not parked. A random identifier would be a turn that does not
	// exist, which is a different case — refused as a bad target — and not the one this is about.
	a.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1",
		"messages": []map[string]any{{"role": "user", "content": "I cycle to work."}}}, http.StatusCreated, nil)
	var live string
	if err := a.pool.QueryRow(ctx, a.schema.SQL(
		`SELECT observation_id::text FROM {schema}.observation WHERE scope='p1' AND parked_at IS NULL LIMIT 1`)).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if status := a.act(t, "/portal/actions/unpark", url.Values{"guard": {guard},
		"project": {"p1"}, "observation": {live}}); status != http.StatusSeeOther {
		t.Fatalf("answered %d", status)
	}
	body, _ := a.page(t, "/portal/projects/p1")
	if !strings.Contains(body, "was not parked") {
		t.Fatalf("the page claimed a retry that did not happen")
	}
}

// ── #57 ──────────────────────────────────────────────────────────────────────────────────────
//
// The store writes an unpark's ledger row in the same transaction as the change, so the portal writes
// none of its own for what the store recorded, and one Retry is one row. Every outcome is counted:
//   - a parked turn retried: allowed, magnitude 1;
//   - the same turn retried again, no longer parked: allowed, magnitude 0, because nothing changed;
//   - a turn the project never held: refused, recorded by the store;
//   - an identifier that is not one: refused before the store is reached, so the portal records it.
func TestOneRetryFromThePortalIsOneRowOnTheLedger(t *testing.T) {
	a := signedInPortal(t, "portal_unpark_rows")
	ctx := context.Background()
	_, guard := a.page(t, "/portal/")
	a.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1",
		"messages": []map[string]any{{"role": "user", "content": "I swim before work."}}}, http.StatusCreated, nil)
	if _, err := a.pool.Exec(ctx, a.schema.SQL(
		`UPDATE {schema}.observation SET parked_at=now(), formation_attempts=2 WHERE scope='p1' AND formed_at IS NULL`)); err != nil {
		t.Fatal(err)
	}
	var parked string
	if err := a.pool.QueryRow(ctx, a.schema.SQL(
		`SELECT observation_id::text FROM {schema}.observation WHERE scope='p1' AND parked_at IS NOT NULL LIMIT 1`)).Scan(&parked); err != nil {
		t.Fatal(err)
	}

	type row struct {
		outcome   string
		magnitude int
	}
	ledger := func() []row {
		t.Helper()
		rows, err := a.pool.Query(ctx, a.schema.SQL(
			`SELECT outcome, magnitude FROM {schema}.audit_entry
			  WHERE principal_kind='operator' AND operation=$1 ORDER BY entry_id`), domain.AuditFormationUnpark)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.outcome, &r.magnitude); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		return out
	}
	retry := func(observation string) {
		t.Helper()
		if status := a.act(t, "/portal/actions/unpark", url.Values{"guard": {guard},
			"project": {"p1"}, "observation": {observation}}); status != http.StatusSeeOther {
			t.Fatalf("retry answered %d", status)
		}
	}

	steps := []struct {
		what        string
		observation string
		want        row
	}{
		{"a parked turn", parked, row{"allowed", 1}},
		{"the same turn, no longer parked", parked, row{"allowed", 0}},
		{"a turn the project never held", uuid.NewString(), row{"refused", 0}},
		{"an identifier that is not one", "not-a-uuid", row{"refused", 0}},
	}
	for i, step := range steps {
		retry(step.observation)
		got := ledger()
		if len(got) != i+1 {
			t.Fatalf("after retrying %s there are %d formation.unpark rows, want %d: one retry is one row",
				step.what, len(got), i+1)
		}
		if got[i] != step.want {
			t.Fatalf("retrying %s recorded %+v, want %+v", step.what, got[i], step.want)
		}
	}
}

func (a *acting) refusedRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := a.pool.QueryRow(context.Background(), a.schema.SQL(
		`SELECT count(*) FROM {schema}.audit_entry WHERE principal_kind='operator' AND outcome='refused'`)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A refused action records a refusal, and the project it records is one that validated or none at
// all. The ledger is permanent and sealed, and its project column takes any text; a form field is
// caller-controlled. Recording the posted value would put "../p1" or a sentence into the record for
// good — the failure the anonymous-audit test exists to prevent, arriving through a signed-in page.
func TestARefusedPortalActionNeverWritesTheCallersTextIntoTheLedger(t *testing.T) {
	a := signedInPortal(t, "portal_ledger_text")
	ctx := context.Background()
	_, guard := a.page(t, "/portal/")
	hostile := []string{"../p1", "not a project", "p1; DROP TABLE x", "<script>", strings.Repeat("z", 200)}
	for _, value := range hostile {
		a.act(t, "/portal/actions/suspend", url.Values{"guard": {guard}, "project": {value}})
	}
	rows, err := a.pool.Query(ctx, a.schema.SQL(
		`SELECT project FROM {schema}.audit_entry WHERE principal_kind='operator' AND outcome='refused'`))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var project string
		if err := rows.Scan(&project); err != nil {
			t.Fatal(err)
		}
		seen++
		for _, value := range hostile {
			if project == value {
				t.Fatalf("the ledger recorded the caller's text %q as a project", value)
			}
		}
		if project != "" && project != "p1" {
			t.Fatalf("the ledger recorded a project that never validated: %q", project)
		}
	}
	if seen < len(hostile) {
		t.Fatalf("%d refusals and %d refused rows: a refusal must still be on the record", len(hostile), seen)
	}
}

// ── #51 ──────────────────────────────────────────────────────────────────────────────────────
//
// The portal and the management API are two ways to reach one set of powers, so the portal refuses
// to issue what the API refuses.
//   - **A name no project uses:** its credential would wait for a project of that name and reach it
//     the moment one was created.
//   - **A suspended project:** its credential would authenticate and reach nothing until somebody
//     resumed the project.
//
// Resuming the project and issuing again is what shows the refusals came from the project's state,
// not from a form that never works.
func TestThePortalIssuesNoCredentialForAProjectThatDoesNotExistOrIsSuspended(t *testing.T) {
	a := signedInPortal(t, "portal_issue_active")
	ctx := context.Background()
	projects := pg.NewProjectStore(a.pool, a.schema)
	_, guard := a.page(t, "/portal/")

	// Counted by label, because the registry is shared by every test that issues a credential.
	issued := func(label string) int {
		t.Helper()
		var n int
		if err := a.pool.QueryRow(ctx,
			`SELECT count(*) FROM `+string(migrate.ControlSchema)+`.credential WHERE name=$1`, label).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	ledger := func(outcome string) int {
		t.Helper()
		var n int
		if err := a.pool.QueryRow(ctx, a.schema.SQL(
			`SELECT count(*) FROM {schema}.audit_entry WHERE principal_kind='operator' AND operation=$1 AND outcome=$2`),
			domain.AuditCredentialIssue, outcome).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	issue := func(project, label string) string {
		t.Helper()
		form := url.Values{"guard": {guard}, "project": {project}, "name": {label}}
		if status := a.act(t, "/portal/actions/issue", form); status != http.StatusSeeOther {
			t.Fatalf("issue answered %d", status)
		}
		body, _ := a.page(t, "/portal/")
		return body
	}
	suffix := uuid.NewString()[:8]
	refusedBefore, allowedBefore := ledger("refused"), ledger("allowed")

	nowhere, label := "nowhere_"+suffix, "never_"+suffix
	if body := issue(nowhere, label); !strings.Contains(body, "Not done.") {
		t.Fatal("the page did not refuse a project that does not exist")
	}
	if n := issued(label); n != 0 {
		t.Fatalf("a project that does not exist was issued %d credentials", n)
	}

	if err := projects.Suspend(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	label = "suspended_" + suffix
	if body := issue("p1", label); !strings.Contains(body, "Not done.") {
		t.Fatal("the page did not refuse a suspended project")
	}
	if n := issued(label); n != 0 {
		t.Fatalf("a suspended project was issued %d credentials", n)
	}

	if err := projects.Resume(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	label = "resumed_" + suffix
	if body := issue("p1", label); !strings.Contains(body, "Done.") || strings.Contains(body, "Not done.") {
		t.Fatal("the page did not issue for the project once it was resumed")
	}
	if n := issued(label); n != 1 {
		t.Fatalf("the resumed project was issued %d credentials, want 1", n)
	}

	if refused := ledger("refused") - refusedBefore; refused != 2 {
		t.Fatalf("%d refused credential.issue rows, want 2: a refusal must be on the record", refused)
	}
	if allowed := ledger("allowed") - allowedBefore; allowed != 1 {
		t.Fatalf("%d allowed credential.issue rows, want 1", allowed)
	}
}
