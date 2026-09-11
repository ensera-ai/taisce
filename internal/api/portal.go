// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// The portal (G14, D135): what an operator sees, read from the tables that exist, through the
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
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
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
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
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
	p.mu.Lock()
	p.sessions[id] = portalSession{token: token, expires: time.Now().Add(portalSessionLife)}
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
	p.render(w, http.StatusOK, page)
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
	p.render(w, http.StatusOK, portalPage{Title: "Project " + name, Operator: grant.Name,
		Project: &portalProject{Name: name, Refusals: refusals, Erasures: erasures, Parked: parked, Credentials: listed}})
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
	p.render(w, http.StatusOK, page)
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
