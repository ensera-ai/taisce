// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	preview(t, "overview-with-work", home)
	preview(t, "project-with-work", project)
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
	preview(t, "ledger-with-work", ledger)
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

// A page whose table cannot answer is an error page, never an empty panel that reads as "nothing to
// report"; a sign-in whose form cannot be read is refused; a session the process no longer holds
// is sent to sign in.
func TestThePortalSaysWhenATableCannotAnswerAndRefusesWhatItCannotRead(t *testing.T) {
	m := newManaged(t, "api_portal_broken")
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.Handle("/", m.manageServer.Handler())
	api.NewPortal(m.manageServer).Mount(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if resp, err := browser.PostForm(server.URL+"/portal/login", url.Values{"token": {m.operator}}); err != nil || resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign in: %v %v", err, resp)
	}
	status := func(path string) int {
		t.Helper()
		resp, err := browser.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_ = readAll(t, resp)
		return resp.StatusCode
	}
	moved := func(table string, path string) {
		t.Helper()
		if _, err := m.pool.Exec(ctx, m.schema.SQL(`ALTER TABLE {schema}.`+table+` RENAME TO `+table+`_moved`)); err != nil {
			t.Fatal(err)
		}
		if got := status(path); got != http.StatusInternalServerError {
			t.Errorf("%s without %s answered %d, not an error page", path, table, got)
		}
		if _, err := m.pool.Exec(ctx, m.schema.SQL(`ALTER TABLE {schema}.`+table+`_moved RENAME TO `+table)); err != nil {
			t.Fatal(err)
		}
	}
	moved("project", "/portal/")
	moved("rejected_claim", "/portal/projects/p1")
	moved("erasure_request", "/portal/projects/p1")
	moved("observation", "/portal/projects/p1")
	moved("observation", "/portal/")
	moved("audit_entry", "/portal/ledger")
	// A form that cannot be read is refused without a query.
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/portal/login", strings.NewReader("token=%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an unreadable form must be refused, got %v %v", resp, err)
	}
	// A cookie naming a session the process does not hold is a stranger's.
	stranger := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/portal/", nil)
	req.AddCookie(&http.Cookie{Name: "taisce_portal", Value: "0000"})
	if resp, err := stranger.Do(req); err != nil || resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("an unknown session must be sent to sign in, got %v %v", resp, err)
	}
	// Signing out without a session is harmless.
	if resp, err := stranger.Post(server.URL+"/portal/logout", "application/x-www-form-urlencoded", nil); err != nil || resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("signing out without a session sends to the form, got %v %v", resp, err)
	}
}

// ── The console loads nothing it does not carry ───────────────────────────────────────────────
//
// Every page the portal renders references nothing outside its own origin: no script, no remote
// stylesheet or font, no image. Its fonts come from the binary, the page's policy says so, and the
// font route answers without a session because the sign-in page needs it first. A page an operator
// leaves open should not tell a third party when it was opened, and should keep working when the
// host has no route out.
//
// Setting TAISCE_PORTAL_PREVIEW to a directory also writes each rendered page, and the fonts, into
// it — the way to look at the console without signing a browser in.
func TestThePortalLoadsNothingFromOutsideItsOwnOrigin(t *testing.T) {
	a := signedInPortal(t, "portal_origin")
	a.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "someone",
		"messages": []map[string]any{{"role": "user", "content": "A turn for the ledger to count."}}}, http.StatusCreated, nil)
	a.form(t)

	for name, path := range map[string]string{
		"login": "/portal/login", "overview": "/portal/", "project": "/portal/projects/p1", "ledger": "/portal/ledger",
	} {
		resp, err := a.browser.Get(a.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body := readAll(t, resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s answered %d", path, resp.StatusCode)
		}
		policy := resp.Header.Get("Content-Security-Policy")
		for _, clause := range []string{"default-src 'none'", "font-src 'self'", "frame-ancestors 'none'", "base-uri 'none'"} {
			if !strings.Contains(policy, clause) {
				t.Fatalf("%s's policy lacks %q: %s", path, clause, policy)
			}
		}
		for _, outside := range []string{`src="http`, `href="http`, "url(http", "url(//", "@import", "<script", "<img", "<link"} {
			if strings.Contains(body, outside) {
				t.Fatalf("%s reaches outside its origin with %q", path, outside)
			}
		}
		if !strings.Contains(body, "/portal/assets/manrope-latin-400-normal.woff2") {
			t.Fatalf("%s does not load its own typeface", path)
		}
		preview(t, name, body)
	}

	// The fonts and their licences, without a session: a fresh client carries no cookie.
	anonymous := &http.Client{}
	for file, kind := range map[string]string{
		"manrope-latin-400-normal.woff2":       "font/woff2",
		"space-grotesk-latin-700-normal.woff2": "font/woff2",
		"Manrope-OFL.txt":                      "text/plain; charset=utf-8",
		"SpaceGrotesk-OFL.txt":                 "text/plain; charset=utf-8",
	} {
		resp, err := anonymous.Get(a.server.URL + "/portal/assets/" + file)
		if err != nil {
			t.Fatal(err)
		}
		body := readAll(t, resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != kind || len(body) == 0 ||
			resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: %d %q, %d bytes", file, resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
		}
		if dir := os.Getenv("TAISCE_PORTAL_PREVIEW"); dir != "" {
			if err := os.MkdirAll(filepath.Join(dir, "portal", "assets"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "portal", "assets", file), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Only the portal's own files, and nothing that is not a font or a licence.
	for _, file := range []string{"login.html", "styles.html", "missing.woff2", "..%2Fportal.go", "manrope-latin-400-normal.woff2.bak"} {
		resp, err := anonymous.Get(a.server.URL + "/portal/assets/" + file)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("/portal/assets/%s answered %d, want 404", file, resp.StatusCode)
		}
	}
}

// preview writes a rendered page where TAISCE_PORTAL_PREVIEW says, and does nothing otherwise.
func preview(t *testing.T, name, body string) {
	t.Helper()
	dir := os.Getenv("TAISCE_PORTAL_PREVIEW")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(dir, "portal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "portal", name+".html"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ── What the instance has been doing ──────────────────────────────────────────────────────────
//
// The overview counts the ledger for the window and the project the operator asks for, and only
// those: a window is one the page offers and a project is one the instance holds, anything else is
// the default. Reading the counts is on the ledger with the operator as principal, narrowed to the
// project it was narrowed to. The ledger's rows narrow the same way. A ledger that cannot be counted
// loses the activity panel and says so, and the rest of the page still answers.
func TestTheOverviewCountsTheLedgerForTheWindowAndProjectAsked(t *testing.T) {
	a := signedInPortal(t, "portal_activity")
	ctx := context.Background()
	for _, name := range []string{"billing", "support"} {
		a.manageDo(t, "/projects/create", map[string]any{"name": name}, a.operator, http.StatusCreated, nil)
	}
	// A week of work, written into the ledger at known times: turns observed and recalled across
	// three projects, and now and then a refusal.
	now := time.Now().UTC()
	at := func(operation, project, outcome string, magnitude int, ago time.Duration) {
		t.Helper()
		if _, err := a.pool.Exec(ctx, a.schema.SQL(`
			INSERT INTO {schema}.audit_entry (operation, principal, principal_kind, project, magnitude, outcome, occurred_at)
			VALUES ($1, 'c-seeded', 'credential', $2, $3, $4, $5)`), operation, project, magnitude, outcome, now.Add(-ago)); err != nil {
			t.Fatal(err)
		}
	}
	for h := 1; h < 7*24; h++ {
		wave := 1 + (h%24)/4
		for i := 0; i < wave; i++ {
			at("observe", "p1", "allowed", 2, time.Duration(h)*time.Hour+time.Duration(i)*time.Minute)
		}
		if h%2 == 0 {
			at("recall", "billing", "allowed", 5, time.Duration(h)*time.Hour)
			at("context.assemble", "p1", "allowed", 8, time.Duration(h)*time.Hour+30*time.Minute)
		}
		if h%9 == 0 {
			at("recall", "support", "refused", 0, time.Duration(h)*time.Hour)
		}
		if h%40 == 0 {
			at("erase", "support", "allowed", 12, time.Duration(h)*time.Hour)
		}
	}

	week, _ := a.page(t, "/portal/")
	for _, want := range []string{`aria-current="page">7d<`, "Turns stored", "Recalls served", "Refused", "Erasures",
		"Busiest projects", `href="/portal/?range=7d&amp;project=billing"`, "vs the previous 7d", `class="kpi alert"`, "View breakdown"} {
		if !strings.Contains(week, want) {
			t.Fatalf("the week's overview lacks %q", want)
		}
	}
	preview(t, "dashboard", week)
	day, _ := a.page(t, "/portal/?range=24h")
	if !strings.Contains(day, `aria-current="page">24h<`) || strings.Count(day, `class="bar"`) != 24 {
		t.Fatalf("a day is 24 hourly bars, got %d", strings.Count(day, `class="bar"`))
	}
	if other, _ := a.page(t, "/portal/?range=1y&project=nobody"); !strings.Contains(other, `aria-current="page">7d<`) ||
		!strings.Contains(other, `<span class="switch-label">All projects</span>`) {
		t.Fatal("a window or a project the page does not offer was not the default")
	}
	narrowed, _ := a.page(t, "/portal/?range=30d&project=p1")
	if !strings.Contains(narrowed, `<span class="switch-label">p1</span>`) || !strings.Contains(narrowed, "Busiest operations") ||
		strings.Contains(narrowed, "Busiest projects") {
		t.Fatal("the overview narrowed to p1 does not rank p1's operations")
	}
	preview(t, "dashboard-p1", narrowed)

	entries, err := pg.NewAuditStore(a.pool, a.schema).RecentIn(ctx, "p1", 50)
	if err != nil {
		t.Fatal(err)
	}
	var readNarrowed bool
	for _, e := range entries {
		if e.Operation == domain.AuditAuditActivity && e.PrincipalKind == domain.PrincipalOperator {
			readNarrowed = true
		}
	}
	if !readNarrowed {
		t.Fatal("reading p1's activity left no ledger row for p1 with the operator as principal")
	}

	ledger, _ := a.page(t, "/portal/ledger?project=support")
	if !strings.Contains(ledger, "project support") || strings.Contains(ledger, `<span class="muted">instance</span>`) ||
		!strings.Contains(ledger, `<span class="switch-label">support</span>`) {
		t.Fatal("the ledger narrowed to one project shows another's rows, or does not say it is narrowed")
	}
	preview(t, "ledger-support", ledger)
	project, _ := a.page(t, "/portal/projects/billing")
	if !strings.Contains(project, `<span class="switch-label">billing</span>`) || !strings.Contains(project, `id="credentials"`) {
		t.Fatal("a project page does not say which project it is in the switcher")
	}

	if _, err := a.pool.Exec(ctx, a.schema.SQL(`ALTER TABLE {schema}.audit_entry RENAME TO audit_entry_moved`)); err != nil {
		t.Fatal(err)
	}
	resp, err := a.browser.Get(a.server.URL + "/portal/")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	resp.Body.Close()
	if _, err := a.pool.Exec(ctx, a.schema.SQL(`ALTER TABLE {schema}.audit_entry_moved RENAME TO audit_entry`)); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "The ledger could not be counted") || !strings.Contains(body, "Projects and formation") {
		t.Fatalf("an uncountable ledger took the page with it, or went unsaid: %d", resp.StatusCode)
	}
}
