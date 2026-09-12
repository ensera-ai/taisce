// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// The portal: what an operator sees, read from the tables that exist, through the
// management surface's own stores, with the same credential the surface takes. It is served by the
// manage role only when switched on, on the same loopback listener, so it is neither exposed nor
// open by default. A browser session is a random id in this process mapped to the operator token,
// which is resolved again on every request so a revocation takes effect at once; nothing about a
// session is written anywhere.
//
// What it shows is what the management surface answers: projects and the watermark, refusal counts
// by reason and predicate without a word of anybody's, erasure receipts, parked turns, credentials
// by name and prefix, and the ledger's verification. Every panel is a management operation on the
// ledger with the operator as principal. What the model costs is not recorded and the panel says so
// rather than reading it out of a component the deployment may not have.
package api

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

//go:embed portal/*.html
var portalFiles embed.FS

// portalAssets are the portal's fonts and their licences, compiled in. See asset.
//
//go:embed portal/assets
var portalAssets embed.FS

var portalTemplates = template.Must(template.ParseFS(portalFiles, "portal/*.html"))

// PortalPrefix is where the portal lives on the manage role's listener.
const PortalPrefix = "/portal"

const portalCookie = "taisce_portal"

// portalSessionLife bounds a browser session; the token behind it is resolved on every request
// anyway, so this is a ceiling on how long a forgotten browser stays signed in, not the check.
const portalSessionLife = 12 * time.Hour

// Portal serves the operator's view over a ManagementServer's stores.
type Portal struct {
	m        *ManagementServer
	mu       sync.Mutex
	sessions map[string]portalSession
}

type portalSession struct {
	token   string
	expires time.Time
	// guard is this session's synchroniser token, rendered into every form that acts and required
	// back on every request that does.
	//
	// The cookie is already SameSite=Strict and the page's own policy is `form-action 'self'`, so a
	// cross-site form post is refused twice before reaching here. This is the third, and it is worth
	// having because the first two are promises made by the browser: an old one, a misconfigured
	// proxy that strips the header, or a future relaxation of what "same site" means, and the page
	// is acting on somebody else's request. A value the attacker cannot read does not depend on the
	// browser behaving.
	guard string
	// outcome is the last action's result, waiting for the page that reports it.
	outcome *portalOutcome
}

func NewPortal(m *ManagementServer) *Portal {
	return &Portal{m: m, sessions: map[string]portalSession{}}
}

// Mount registers the portal's routes on a mux the manage role serves.
func (p *Portal) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET "+PortalPrefix+"/{$}", p.signed(p.home))
	mux.HandleFunc("GET "+PortalPrefix+"/login", p.loginForm)
	mux.HandleFunc("POST "+PortalPrefix+"/login", p.login)
	mux.HandleFunc("POST "+PortalPrefix+"/logout", p.logout)
	mux.HandleFunc("GET "+PortalPrefix+"/ledger", p.signed(p.ledger))
	mux.HandleFunc("GET "+PortalPrefix+"/projects/{project}", p.signed(p.project))
	mux.HandleFunc("GET "+PortalPrefix+"/assets/{file}", p.asset)
	p.mountActions(mux)
}

type portalPage struct {
	Title    string
	Operator string
	Login    bool
	Refused  bool
	Home     bool
	Scopes   []portalScope
	Project  *portalProject
	Ledger   *portalLedger
	// Guard is this session's synchroniser token, rendered into every form that acts.
	Guard string
	// Health is the instance's operational snapshot. Absent when the deployment did not wire a
	// reader, which loses this panel and no other.
	Health *portalHealth
	// Activity is the ledger counted over the window the overview was asked for, and
	// ActivityUnavailable says it could not be, so an empty panel is never read as an idle instance.
	Activity            *portalActivity
	ActivityUnavailable bool
	// Switch moves between projects, and is absent when the projects could not be listed.
	Switch *portalSwitch
	// Outcome is what the last action did, carried to the page that follows it. Held in the session
	// rather than in the URL: an outcome in a query string is one a caller can put there, and a
	// page that reports a revocation nobody performed is worse than one that reports nothing.
	Outcome *portalOutcome
}

// portalOutcome is an action's result as the operator reads it.
//
// Text this file wrote, never text from a store or an error. A message assembled from a database
// failure is how a table name, a constraint or a row's content reaches a browser, and the page an
// operator leaves open is the last place any of those should appear.
// portalHealth is what an operator watches when they are not looking for anything in particular.
//
// Every number here is an aggregate the ledger or a catalogue view already holds. None of it is
// about a person: a backlog depth, whether a worker answered, and how close the deployment is to the
// server's connection ceiling — the last of which a deployment learned the hard way, by running out of sessions and losing writes.
type portalHealth struct {
	Pending          int64
	PendingLimit     int64
	Parked           int64
	WorkerResponsive bool
	FailedAttempts   int64
	Connections      int64
	MaxConnections   int64
	Headroom         int64
	// Tight is the judgement, made here rather than left to the reader: an operator scanning a page
	// should not have to do arithmetic to notice they are nearly out of connections.
	Tight bool
	// PendingShare and ConnectionShare are the two meters, as whole percentages: a template cannot
	// divide, and a bar is read faster than two numbers.
	PendingShare, ConnectionShare int
}

type portalOutcome struct {
	Done   bool
	Action string
	Detail string
}

type portalScope struct {
	Project   string
	Suspended bool
	Stored    *int64
	Formed    *int64
	Behind    int64
	Parked    int
}

type portalProject struct {
	Name        string
	Refusals    pg.RefusalSummary
	Erasures    []pg.ErasureReceipt
	Parked      pg.ParkedPage
	Credentials []credential.Listed
}

type portalLedger struct {
	Verified bool
	Detail   string
	Document string
	// Entries are the ledger's own rows: who did what, to which project, how much of it, and
	// whether it was allowed. No content by construction — the table holds none — so showing them
	// discloses nothing a verdict does not, and answers the question a verdict cannot.
	Entries []domain.AuditEntry
	// Unavailable says the rows could not be read, so an empty table is never mistaken for an
	// empty ledger.
	Unavailable bool
	// Project is the one project the rows are narrowed to, or empty for all of them.
	Project string
}

// portalPolicy is the pages' content security policy. Named so the test holds the exact string
// rather than a copy of it.
const portalPolicy = "default-src 'none'; style-src 'unsafe-inline'; font-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// asset serves the portal's own fonts and their licences.
//
// From the binary, not from a font service. An operator's console that fetched its typeface from
// somebody else's server would tell that server when, and from where, the console was opened, and
// would lose its type the day the deployment has no route out. Public, because the sign-in page
// needs them before anybody has signed in, and nothing here belongs to anybody: they are the files
// compiled into every copy of the binary.
//
// Cached for a day and not forever: the names carry no version, so an upgrade that changed a font
// would otherwise be invisible to a browser that had seen the old one.
func (p *Portal) asset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	var kind string
	switch path.Ext(name) {
	case ".woff2":
		kind = "font/woff2"
	case ".txt":
		kind = "text/plain; charset=utf-8"
	default:
		http.NotFound(w, r)
		return
	}
	body, err := portalAssets.ReadFile("portal/assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	_, _ = w.Write(body)
}

// share is n as a whole percentage of of, for a meter.
//
// Clamped at both ends: a backlog read a moment past its limit, or a connection count taken after
// the ceiling was lowered, would otherwise draw a bar out of its box, and a ceiling of zero would
// divide by it.
func share(n, of int64) int {
	if of <= 0 || n <= 0 {
		return 0
	}
	if n >= of {
		return 100
	}
	return int(n * 100 / of)
}

// portalLedgerRows bounds the panel. The ledger grows forever and a page does not; an operator
// reading further uses the management API, which pages properly.
const portalLedgerRows = 100

// renderFor is render for a signed-in page: it attaches the session's guard so forms can act, and
// takes any outcome the last action left. Separate from render, which the sign-in page uses and
// which must never carry either.
func (p *Portal) renderFor(w http.ResponseWriter, r *http.Request, status int, page portalPage) {
	if cookie, err := r.Cookie(portalCookie); err == nil {
		page.Outcome, page.Guard = p.take(cookie.Value)
	}
	if page.Health == nil {
		page.Health = p.health(r)
	}
	p.render(w, status, page)
}

func (p *Portal) render(w http.ResponseWriter, status int, page portalPage) {
	var buf bytes.Buffer
	if err := portalTemplates.ExecuteTemplate(&buf, "login", page); err != nil {
		p.m.log.Error("render portal", "error", err)
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Nothing from anywhere but this origin, and only fonts and inline style from here: the page
	// runs no script and loads no image. frame-ancestors because a console that acts is a console
	// somebody could frame and click through; base-uri because a relative link must not be
	// re-pointed by an injected <base>.
	w.Header().Set("Content-Security-Policy", portalPolicy)
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// signed resolves the browser session to an operator grant, or sends the browser to sign in with
// nothing shown. The token is resolved again each time: a revoked operator is signed out by the
// registry, not by a timer.
func (p *Portal) signed(next func(http.ResponseWriter, *http.Request, credential.Grant)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(portalCookie)
		if err != nil {
			http.Redirect(w, r, PortalPrefix+"/login", http.StatusSeeOther)
			return
		}
		p.mu.Lock()
		session, ok := p.sessions[cookie.Value]
		if ok && time.Now().After(session.expires) {
			delete(p.sessions, cookie.Value)
			ok = false
		}
		p.mu.Unlock()
		if !ok {
			http.Redirect(w, r, PortalPrefix+"/login", http.StatusSeeOther)
			return
		}
		grant, err := p.m.credentials.ResolveOperator(r.Context(), session.token)
		if err != nil {
			p.forget(w, cookie.Value)
			http.Redirect(w, r, PortalPrefix+"/login", http.StatusSeeOther)
			return
		}
		next(w, r, grant)
	}
}

// acting is `signed` for a request that changes something.
//
// It adds the two things reading does not need. The session's synchroniser token must come back in
// the form, so a request the operator did not make from this page is refused before anything runs.
// And the handler is given the session id, because an action's outcome is rendered on the next page
// and the page needs the guard again.
//
// Everything else is deliberately the same: the operator credential is re-resolved here as it is for
// a read, so a revoked operator cannot act with a live browser session any more than they can look.
func (p *Portal) acting(next func(http.ResponseWriter, *http.Request, credential.Grant, string)) http.HandlerFunc {
	return p.signed(func(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
		cookie, err := r.Cookie(portalCookie)
		if err != nil {
			http.Redirect(w, r, PortalPrefix+"/login", http.StatusSeeOther)
			return
		}
		if err := r.ParseForm(); err != nil {
			p.refuse(w, r, grant, "the form could not be read")
			return
		}
		p.mu.Lock()
		session, ok := p.sessions[cookie.Value]
		p.mu.Unlock()
		// Constant time, because a guard compared byte by byte is one an attacker can learn by
		// timing rather than by reading.
		if !ok || subtle.ConstantTimeCompare([]byte(session.guard), []byte(r.PostFormValue("guard"))) != 1 {
			// No detail. A request that did not come from this page is told that it did not work,
			// not which half of the check it failed.
			p.refuse(w, r, grant, "this request did not come from the portal; sign in again")
			return
		}
		next(w, r, grant, cookie.Value)
	})
}

// refuse renders the page the operator was on with a refusal, and records it.
//
// A refused action is on the ledger for the same reason a refused operation is: the record answers
// "who tried", and an attempt that is turned away is exactly the kind somebody asks about later.
func (p *Portal) refuse(w http.ResponseWriter, r *http.Request, grant credential.Grant, detail string) {
	p.m.record(r, domain.AuditFormationStatus, grant, "", domain.OutcomeRefused, 0)
	p.render(w, http.StatusBadRequest, portalPage{Title: "Refused", Operator: grant.Name,
		Outcome: &portalOutcome{Action: "the request", Detail: detail}})
}

// remember holds an action's outcome until the next page renders it, then drops it.
func (p *Portal) remember(id string, outcome portalOutcome) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if session, ok := p.sessions[id]; ok {
		session.outcome = &outcome
		p.sessions[id] = session
	}
}

func (p *Portal) take(id string) (*portalOutcome, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	session, ok := p.sessions[id]
	if !ok {
		return nil, ""
	}
	outcome := session.outcome
	session.outcome = nil
	p.sessions[id] = session
	return outcome, session.guard
}

func (p *Portal) forget(w http.ResponseWriter, id string) {
	p.mu.Lock()
	delete(p.sessions, id)
	p.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: portalCookie, Value: "", Path: PortalPrefix, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

func (p *Portal) loginForm(w http.ResponseWriter, r *http.Request) {
	p.render(w, http.StatusOK, portalPage{Title: "Sign in", Login: true})
}

// login exchanges an operator token for a session. A refused token is answered like a stranger's:
// one page, no detail, and the refusal on the ledger through the surface's sampling budget.
func (p *Portal) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		p.render(w, http.StatusBadRequest, portalPage{Title: "Sign in", Login: true, Refused: true})
		return
	}
	releaseAuth, ok := p.m.admission.authenticate(time.Now())
	if !ok {
		p.render(w, http.StatusTooManyRequests, portalPage{Title: "Sign in", Login: true, Refused: true})
		return
	}
	token := strings.TrimSpace(r.PostFormValue("token"))
	_, err := p.m.credentials.ResolveOperator(r.Context(), token)
	releaseAuth()
	if err != nil {
		p.m.recordRefusedAuth(r)
		p.render(w, http.StatusUnauthorized, portalPage{Title: "Sign in", Login: true, Refused: true})
		return
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		http.Error(w, "no randomness for a session", http.StatusInternalServerError)
		return
	}
	id := hex.EncodeToString(raw[:])
	var guard [32]byte
	if _, err := rand.Read(guard[:]); err != nil {
		http.Error(w, "no randomness for a session", http.StatusInternalServerError)
		return
	}
	p.mu.Lock()
	p.sessions[id] = portalSession{token: token, expires: time.Now().Add(portalSessionLife),
		guard: hex.EncodeToString(guard[:])}
	p.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: portalCookie, Value: id, Path: PortalPrefix, HttpOnly: true, Secure: r.TLS != nil,
		SameSite: http.SameSiteStrictMode, MaxAge: int(portalSessionLife / time.Second)})
	http.Redirect(w, r, PortalPrefix+"/", http.StatusSeeOther)
}

func (p *Portal) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(portalCookie); err == nil {
		p.forget(w, cookie.Value)
	}
	http.Redirect(w, r, PortalPrefix+"/login", http.StatusSeeOther)
}

func (p *Portal) home(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	projects, err := p.m.stores.Projects.List(r.Context())
	if err != nil {
		p.m.log.Error("portal: list projects", "error", err)
		http.Error(w, "projects could not be read", http.StatusInternalServerError)
		return
	}
	page := portalPage{Title: "Instance", Operator: grant.Name, Home: true, Scopes: []portalScope{}}
	// The window and the project come from the query, and each is one of a closed set: a range
	// the page offers, and a project the instance holds. Anything else is the default, not an error
	// and never a value passed on to a query.
	rng := pickRange(r.URL.Query().Get("range"))
	narrowed, names := "", make([]string, 0, len(projects))
	for _, project := range projects {
		names = append(names, project.Scope)
		if project.Scope == r.URL.Query().Get("project") {
			narrowed = project.Scope
		}
	}
	for _, project := range projects {
		f, err := p.m.stores.Observations.Freshness(r.Context(), p.m.schema, project.Scope)
		if err != nil {
			p.m.log.Error("portal: read freshness", "error", err, "project", project.Scope)
			http.Error(w, "formation status could not be read", http.StatusInternalServerError)
			return
		}
		scope := portalScope{Project: project.Scope, Suspended: project.Suspended, Parked: f.Parked}
		if f.HasStored {
			stored := f.Stored
			scope.Stored = &stored
			scope.Behind = stored + 1
		}
		if f.HasFormed {
			formed := f.Formed
			scope.Formed = &formed
			scope.Behind = f.Stored - formed
		}
		page.Scopes = append(page.Scopes, scope)
	}
	p.m.record(r, domain.AuditProjectList, grant, "", domain.OutcomeAllowed, len(projects))
	p.m.record(r, domain.AuditFormationStatus, grant, "", domain.OutcomeAllowed, len(page.Scopes))
	page.Health = p.health(r)
	now := time.Now().UTC()
	var since time.Time
	if rng.Length > 0 {
		since = now.Add(-rng.Length)
	}
	if activity, err := p.m.stores.Audit.Activity(r.Context(), narrowed, since, now, rng.Buckets); err == nil {
		page.Activity = newPortalActivity(rng, narrowed, activity, now)
		p.m.record(r, domain.AuditAuditActivity, grant, narrowed, domain.OutcomeAllowed, int(activity.Current.Operations))
	} else {
		// The activity is a reading of the ledger, and the rest of the page is not: a ledger that
		// cannot be counted loses this panel, says so, and leaves the projects and the health where
		// they are.
		p.m.log.Error("portal: count the ledger", "error", err)
		page.ActivityUnavailable = true
	}
	page.Switch = switcher(names, narrowed, func(name string) string { return overviewHref(rng.Key, name) })
	p.renderFor(w, r, http.StatusOK, page)
}

// health is the instance's operational snapshot for the page and the rail, or nil when the
// deployment wired no reader or the read failed — a monitoring read loses its panel, never the page.
func (p *Portal) health(r *http.Request) *portalHealth {
	if p.m.stores.Health == nil {
		return nil
	}
	health, err := p.m.stores.Health(r.Context())
	if err != nil {
		p.m.log.Error("portal: read operational health", "error", err)
		return nil
	}
	headroom := health.ConnectionHeadroom()
	return &portalHealth{
		Pending: health.Pending, PendingLimit: health.PendingLimit, Parked: health.Parked,
		WorkerResponsive: health.WorkerResponsive, FailedAttempts: health.FailedAttempts,
		Connections: health.ClusterConnections, MaxConnections: health.MaxConnections,
		Headroom:        headroom,
		Tight:           headroom <= max64(10, health.MaxConnections/10),
		PendingShare:    share(health.Pending, health.PendingLimit),
		ConnectionShare: share(health.ClusterConnections, health.MaxConnections),
	}
}

// projectNames lists the instance's projects for the switcher, on the ledger like any listing an
// operator's page makes. A failure loses the switcher and nothing else: it is a way to move between
// pages, not a page.
func (p *Portal) projectNames(r *http.Request, grant credential.Grant) []string {
	projects, err := p.m.stores.Projects.List(r.Context())
	if err != nil {
		p.m.log.Error("portal: list projects for the switcher", "error", err)
		return nil
	}
	p.m.record(r, domain.AuditProjectList, grant, "", domain.OutcomeAllowed, len(projects))
	names := make([]string, 0, len(projects))
	for _, project := range projects {
		names = append(names, project.Scope)
	}
	return names
}

func (p *Portal) project(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	name := r.PathValue("project")
	if _, err := pg.NewSchema(name); err != nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	refusals, err := p.m.stores.Refusals.Summary(ctx, name, 100)
	if err != nil {
		p.m.log.Error("portal: refusals", "error", err, "project", name)
		http.Error(w, "refusals could not be read", http.StatusInternalServerError)
		return
	}
	erasures, err := p.m.stores.Eraser.Receipts(ctx, p.m.schema, name, 50)
	if err != nil {
		p.m.log.Error("portal: erasures", "error", err, "project", name)
		http.Error(w, "erasures could not be read", http.StatusInternalServerError)
		return
	}
	parked, err := p.m.stores.Observations.ParkedPage(ctx, p.m.schema, name, -1, 100)
	if err != nil {
		p.m.log.Error("portal: parked", "error", err, "project", name)
		http.Error(w, "parked turns could not be read", http.StatusInternalServerError)
		return
	}
	listed, err := p.m.credentials.List(ctx, name, 100)
	if err != nil {
		p.m.log.Error("portal: credentials", "error", err, "project", name)
		http.Error(w, "credentials could not be read", http.StatusInternalServerError)
		return
	}
	p.m.record(r, domain.AuditRefusalSummary, grant, name, domain.OutcomeAllowed, len(refusals.Counts))
	p.m.record(r, domain.AuditErasureList, grant, name, domain.OutcomeAllowed, len(erasures))
	p.m.record(r, domain.AuditFormationParked, grant, name, domain.OutcomeAllowed, len(parked.Items))
	p.m.record(r, domain.AuditCredentialList, grant, name, domain.OutcomeAllowed, len(listed))
	page := portalPage{Title: "Project " + name, Operator: grant.Name,
		Project: &portalProject{Name: name, Refusals: refusals, Erasures: erasures, Parked: parked, Credentials: listed}}
	if names := p.projectNames(r, grant); names != nil {
		page.Switch = switcher(names, name, func(other string) string {
			if other == "" {
				return PortalPrefix + "/"
			}
			return PortalPrefix + "/projects/" + other
		})
	}
	p.renderFor(w, r, http.StatusOK, page)
}

func (p *Portal) ledger(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	verification, err := p.m.stores.Audit.Verify(r.Context())
	if err != nil {
		p.m.log.Error("portal: verify the ledger", "error", err)
		http.Error(w, "the ledger could not be verified", http.StatusInternalServerError)
		return
	}
	document, _ := json.MarshalIndent(verification, "", "  ")
	p.m.record(r, domain.AuditAuditVerify, grant, "", domain.OutcomeAllowed, 1)
	page := portalPage{Title: "Ledger", Operator: grant.Name, Ledger: &portalLedger{Document: string(document)}}
	page.Ledger.Verified, page.Ledger.Detail = ledgerVerdict(verification)
	// The rows, not only the verdict. They exist, they hold no words by construction, and they are
	// what an operator is looking for when "the ledger verifies" is not the answer to their
	// question. Bounded: a page is a page, and the ledger grows forever.
	names := p.projectNames(r, grant)
	for _, name := range names {
		if name == r.URL.Query().Get("project") {
			page.Ledger.Project = name
		}
	}
	if names != nil {
		page.Switch = switcher(names, page.Ledger.Project, func(name string) string {
			if name == "" {
				return PortalPrefix + "/ledger"
			}
			return PortalPrefix + "/ledger?project=" + url.QueryEscape(name)
		})
	}
	if entries, err := p.m.stores.Audit.RecentIn(r.Context(), page.Ledger.Project, portalLedgerRows); err == nil {
		page.Ledger.Entries = entries
	} else {
		// A verdict without its rows is still worth showing. Saying which half is missing beats a
		// page that silently renders an empty table over a ledger that has entries.
		p.m.log.Error("portal: read ledger rows", "error", err)
		page.Ledger.Unavailable = true
	}
	p.renderFor(w, r, http.StatusOK, page)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ledgerVerdict says in a sentence what the verification found, because a page that shows only a
// boolean tells nobody what happened.
func ledgerVerdict(v domain.AuditVerification) (bool, string) {
	if !v.Valid {
		return false, "The chain does not verify: " + v.Failure
	}
	return true, "The chain holds across " + itoa(v.Seals) + " seal(s) over " + itoa(v.Entries) + " entries, with " + itoa(v.Unsealed) + " entries written since the last seal and covered by nothing yet."
}

func itoa(n int) string { return strconv.Itoa(n) }
