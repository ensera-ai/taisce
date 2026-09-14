// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// The management surface: what an operator does to the instance, over HTTP, with an operator
// credential. It exists so that nothing an operator or a portal does needs a database
// connection: the CLI speaks this where it is configured, and the portal reads through it.
//
// # What this protects, and from whom
//
// The asset is the control registry and the instance's shape: projects, credentials, the ledger's
// seal. The actor is an operator, holding a credential of the operator kind. A project credential
// must not reach this door and an operator credential must not reach a project's memory; the
// registry column `kind` is the boundary, and each door resolves only its own kind, refusing the
// other with the one answer a stranger gets. Refusal content is somebody's words, so this surface
// counts refusals and never quotes them: the words are read under a project credential through the
// memory routes, where the read is on the ledger against that project.
//
// # Why a role of its own
//
// Creating a project is DDL (a partition), which needs the administrative connection the serving
// identities deliberately do not have. Rather than hand the memory-serving process that
// connection, this surface is served by the `manage` role of the same binary on its own listener,
// and is not served at all unless that role is chosen: a management route that does not exist
// cannot be left open.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// ManagementPrefix is where the management operations live: a prefix of their own, so a proxy or
// a network policy can expose the memory surface and not this one.
const ManagementPrefix = "/manage/" + Version

// ManagementStores is what the management surface reads and writes. Provision creates a project's
// storage and needs the administrative connection; it is a function so the surface holds no
// connection of that kind itself.
type ManagementStores struct {
	Projects     *pg.ProjectStore
	Provision    func(ctx context.Context, scope string) error
	Observations *pg.ObservationStore
	Audit        *pg.AuditStore
	Refusals     *pg.RefusalStore
	Eraser       *pg.Eraser
	// Health is the instance's operational snapshot: the formation backlog, whether a worker is
	// answering, and how many connections the deployment holds against the server's ceiling. A
	// function for the same reason Provision is — it reads catalogue views the operator connection
	// can see and this surface holds no connection of that kind.
	//
	// Optional: a deployment wired without it loses the panel and keeps every other one, which is
	// the right failure for a monitoring read.
	Health func(ctx context.Context) (pg.OperationalHealth, error)
}

// ManagementServer serves the operator surface.
type ManagementServer struct {
	credentials *credential.Store
	stores      ManagementStores
	schema      pg.Schema
	log         *slog.Logger
	admission   *Admission
	authAudits  refusedAuthBudget
}

func NewManagementServer(credentials *credential.Store, stores ManagementStores, schema pg.Schema, log *slog.Logger) *ManagementServer {
	if log == nil {
		log = slog.Default()
	}
	gate, _ := NewAdmission(4, 4)
	return &ManagementServer{credentials: credentials, stores: stores, schema: schema, log: log, admission: gate}
}

type managementRoute struct {
	Operation
	handler  func(*ManagementServer) grantedHandler
	request  any
	response any
}

// The management surface, declared once, for the same reasons the memory surface is: it has to be
// enumerable for the contract, for the ledger and for the test that refuses every route to the
// wrong credential.
var managementOperations = []managementRoute{
	{Operation{domain.AuditProjectCreate, http.MethodPost, "/projects/create", http.StatusCreated}, func(m *ManagementServer) grantedHandler { return m.createProject }, projectRequest{}, projectResponse{}},
	{Operation{domain.AuditProjectList, http.MethodPost, "/projects/list", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.listProjects }, nil, projectListResponse{}},
	{Operation{domain.AuditProjectSuspend, http.MethodPost, "/projects/suspend", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.suspendProject }, projectRequest{}, projectResponse{}},
	{Operation{domain.AuditProjectResume, http.MethodPost, "/projects/resume", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.resumeProject }, projectRequest{}, projectResponse{}},
	{Operation{domain.AuditProjectRetention, http.MethodPost, "/projects/retention", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.setProjectRetention }, projectRetentionRequest{}, projectRetentionResponse{}},
	{Operation{domain.AuditCredentialIssue, http.MethodPost, "/credentials/issue", http.StatusCreated}, func(m *ManagementServer) grantedHandler { return m.issueCredential }, credentialIssueRequest{}, credentialIssued{}},
	{Operation{domain.AuditCredentialList, http.MethodPost, "/credentials/list", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.listCredentials }, credentialListRequest{}, credentialListResponse{}},
	{Operation{domain.AuditCredentialRevoke, http.MethodPost, "/credentials/revoke", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.revokeCredential }, credentialRevokeRequest{}, credentialRevoked{}},
	{Operation{domain.AuditRefusalSummary, http.MethodPost, "/refusals/summary", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.refusalSummary }, projectPageRequest{}, pg.RefusalSummary{}},
	{Operation{domain.AuditErasureList, http.MethodPost, "/erasures/list", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.listErasures }, projectPageRequest{}, erasureListResponse{}},
	{Operation{domain.AuditFormationStatus, http.MethodPost, "/formation/status", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.formationStatus }, nil, formationStatusResponse{}},
	{Operation{domain.AuditFormationParked, http.MethodPost, "/formation/parked", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.listParked }, parkedRequest{}, pg.ParkedPage{}},
	{Operation{domain.AuditFormationUnpark, http.MethodPost, "/formation/unpark", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.unpark }, unparkRequest{}, unparkResponse{}},
	{Operation{domain.AuditAuditSeal, http.MethodPost, "/audit/seal", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.sealAudit }, nil, domain.AuditSeal{}},
	{Operation{domain.AuditAuditVerify, http.MethodPost, "/audit/verify", http.StatusOK}, func(m *ManagementServer) grantedHandler { return m.verifyAudit }, nil, domain.AuditVerification{}},
}

// ManagementOperations is the surface by operation, for the contract and the tests that walk it.
func ManagementOperations() []Operation {
	out := make([]Operation, 0, len(managementOperations))
	for _, o := range managementOperations {
		out = append(out, o.Operation)
	}
	return out
}

// Handler mounts every management operation under the prefix, each behind the operator door, and
// the health endpoints, which say nothing; readiness, when given, is the database answering.
func (m *ManagementServer) Handler(readiness ...http.Handler) http.Handler {
	mux := http.NewServeMux()
	var ready http.Handler
	if len(readiness) > 1 {
		panic("one readiness handler is permitted")
	}
	if len(readiness) == 1 {
		ready = readiness[0]
	}
	registerHealth(mux, ready)
	for _, o := range managementOperations {
		mux.Handle(o.Method+" "+ManagementPrefix+o.Path, m.operated(o.handler(m)))
	}
	return mux
}

// operated is the operator door: an operator credential and nothing else, then the work slot.
func (m *ManagementServer) operated(next grantedHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		r = r.WithContext(requestCtx)
		releaseAuth, ok := m.admission.authenticate(time.Now())
		if !ok {
			writeAdmissionRefusal(w)
			return
		}
		authCtx, cancelAuth := context.WithTimeout(r.Context(), 2*time.Second)
		token, _ := bearer(r.Header.Get("Authorization"))
		grant, err := m.credentials.ResolveOperator(authCtx, token)
		cancelAuth()
		releaseAuth()
		if err != nil {
			if !errors.Is(err, credential.ErrUnknown) {
				m.log.Error("resolve operator credential", "error", err)
				writeError(w, http.StatusInternalServerError, codeInternal, "the request could not be served")
				return
			}
			m.recordRefusedAuth(r)
			writeError(w, http.StatusUnauthorized, codeUnauthenticated, "a credential is required")
			return
		}
		releaseWork, ok := m.admission.acquire("", grant.CredentialID)
		if !ok {
			writeAdmissionRefusal(w)
			return
		}
		defer releaseWork()
		next(w, r, grant)
	})
}

// recordRefusedAuth is the memory door's rule applied here: the presented token never reaches the
// ledger, and the sampling budget bounds what an anonymous caller can make the instance write.
func (m *ManagementServer) recordRefusedAuth(r *http.Request) {
	release, ok := m.admission.acquireAudit()
	if !ok {
		return
	}
	defer release()
	magnitude, firstSuppressed := m.authAudits.take(time.Now())
	if magnitude == 0 {
		if firstSuppressed {
			m.log.Warn("anonymous management authentication audit sampling is active", "samples_per_minute", refusedAuthSamplesPerMinute)
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := m.stores.Audit.Append(ctx, domain.AuditEntry{Operation: domain.AuditAuthenticate, Principal: "unresolved",
		PrincipalKind: domain.PrincipalSystem, Outcome: domain.OutcomeRefused, Magnitude: magnitude}); err != nil {
		m.log.Error("the audit ledger did not record a refused management authentication", "error", err)
	}
}

// record puts an operator's operation on the ledger, against the project the request named.
func (m *ManagementServer) record(r *http.Request, operation string, grant credential.Grant, project, outcome string, magnitude int) {
	if err := m.stores.Audit.Append(r.Context(), domain.AuditEntry{Operation: operation, Principal: grant.CredentialID,
		PrincipalKind: domain.PrincipalOperator, Project: project, Magnitude: magnitude, Outcome: outcome}); err != nil {
		m.log.Error("the audit ledger did not record a management operation", "operation", operation, "principal", grant.CredentialID, "error", err)
	}
}

// ── Projects ──────────────────────────────────────────────────────────────────────────────────

type projectRequest struct {
	Name string `json:"name"`
}

type projectResponse struct {
	Project   string `json:"project"`
	Suspended bool   `json:"suspended"`
}

// RetentionDays is how long new turns are kept. Null is indefinite, which is also how a project
// starts. Days rather than an interval: an interval accepted as text puts a parser between an
// operator and a deletion schedule.
type projectRetentionRequest struct {
	Name          string `json:"name"`
	RetentionDays *int   `json:"retention_days"`
}
type projectRetentionResponse struct {
	Project   string `json:"project"`
	Retention string `json:"retention"`
}

type projectListResponse struct {
	Projects []pg.Listed `json:"projects"`
}

func (m *ManagementServer) createProject(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req projectRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, codeInvalidProject, "a project needs a name")
		return
	}
	if err := m.stores.Provision(r.Context(), req.Name); err != nil {
		// The provisioner refuses a name that is not an identifier before it touches anything;
		// everything after that is the database.
		if strings.Contains(err.Error(), "is not a valid identifier") {
			m.record(r, domain.AuditProjectCreate, grant, "", domain.OutcomeRefused, 0)
			writeError(w, http.StatusBadRequest, codeInvalidProject, err.Error())
			return
		}
		m.log.Error("create project", "error", err, "project", req.Name)
		writeError(w, http.StatusInternalServerError, codeInternal, "the project could not be created")
		return
	}
	m.record(r, domain.AuditProjectCreate, grant, req.Name, domain.OutcomeAllowed, 1)
	writeJSON(w, http.StatusCreated, projectResponse{Project: req.Name})
}

func (m *ManagementServer) listProjects(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	projects, err := m.stores.Projects.List(r.Context())
	if err != nil {
		m.log.Error("list projects", "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "projects could not be listed")
		return
	}
	m.record(r, domain.AuditProjectList, grant, "", domain.OutcomeAllowed, len(projects))
	writeJSON(w, http.StatusOK, projectListResponse{Projects: projects})
}

func (m *ManagementServer) suspendProject(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	m.setSuspended(w, r, grant, domain.AuditProjectSuspend, true)
}

func (m *ManagementServer) resumeProject(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	m.setSuspended(w, r, grant, domain.AuditProjectResume, false)
}

// setProjectRetention says how long a project keeps what it is told from now on.
//
// A governance decision, so it is on the ledger beside suspension, and it changes nothing already
// stored: a deadline is stamped on a turn when it arrives, and the policy in force then governs it.
func (m *ManagementServer) setProjectRetention(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req projectRetentionRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, codeInvalidProject, "a project is required")
		return
	}
	err := m.stores.Projects.SetRetention(r.Context(), req.Name, req.RetentionDays)
	switch {
	case errors.Is(err, pg.ErrInvalidRetention):
		m.record(r, domain.AuditProjectRetention, grant, req.Name, domain.OutcomeRefused, 0)
		writeError(w, http.StatusBadRequest, codeInvalidProject, "retention is a whole number of days from 1 to 36500, or null to keep indefinitely")
		return
	case errors.Is(err, pg.ErrNoSuchProject):
		m.record(r, domain.AuditProjectRetention, grant, req.Name, domain.OutcomeRefused, 0)
		writeError(w, http.StatusNotFound, codeNotFound, "no such project")
		return
	case err != nil:
		m.log.Error("set retention", "error", err, "project", req.Name)
		writeError(w, http.StatusInternalServerError, codeInternal, "the retention policy could not be set")
		return
	}
	m.record(r, domain.AuditProjectRetention, grant, req.Name, domain.OutcomeAllowed, 0)
	writeJSON(w, http.StatusOK, projectRetentionResponse{Project: req.Name, Retention: retentionText(req.RetentionDays)})
}

// retentionText says a policy the way an operator wrote it.
func retentionText(days *int) string {
	if days == nil {
		return "indefinite"
	}
	return fmt.Sprintf("%d days", *days)
}

func (m *ManagementServer) setSuspended(w http.ResponseWriter, r *http.Request, grant credential.Grant, operation string, suspended bool) {
	var req projectRequest
	if !decode(w, r, &req) {
		return
	}
	var err error
	if suspended {
		err = m.stores.Projects.Suspend(r.Context(), req.Name)
	} else {
		err = m.stores.Projects.Resume(r.Context(), req.Name)
	}
	if errors.Is(err, pg.ErrNoSuchProject) {
		m.record(r, operation, grant, req.Name, domain.OutcomeRefused, 0)
		writeError(w, http.StatusNotFound, codeNotFound, "no such project in that state")
		return
	}
	if err != nil {
		m.log.Error("change project state", "error", err, "project", req.Name)
		writeError(w, http.StatusInternalServerError, codeInternal, "the project could not be changed")
		return
	}
	m.record(r, operation, grant, req.Name, domain.OutcomeAllowed, 1)
	writeJSON(w, http.StatusOK, projectResponse{Project: req.Name, Suspended: suspended})
}

// ── Credentials ───────────────────────────────────────────────────────────────────────────────

type credentialIssueRequest struct {
	Name    string `json:"name"`
	Project string `json:"project"`
	// Access is read_only or read_write; omitted means read_write.
	Access string `json:"access"`
}

// credentialIssued carries the token once. It is not recoverable afterwards, by anyone.
type credentialIssued struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Project string `json:"project"`
	Access  string `json:"access"`
	Token   string `json:"token"`
}

type credentialListRequest struct {
	// Project names whose credentials to list; empty lists the operator credentials.
	Project string `json:"project"`
	Limit   int    `json:"limit"`
}

type credentialListResponse struct {
	Credentials []credential.Listed `json:"credentials"`
}

type credentialRevokeRequest struct {
	ID string `json:"id"`
}

type credentialRevoked struct {
	ID      string `json:"id"`
	Revoked bool   `json:"revoked"`
}

func (m *ManagementServer) issueCredential(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req credentialIssueRequest
	if !decode(w, r, &req) {
		return
	}
	access := credential.Access(req.Access)
	if req.Access == "" {
		access = credential.ReadWrite
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Project) == "" || (access != credential.ReadOnly && access != credential.ReadWrite) {
		writeError(w, http.StatusBadRequest, codeInvalidCredential, "a credential needs a name, a project, and access read_only or read_write")
		return
	}
	token, issued, err := m.issueProjectCredential(r.Context(), req.Name, req.Project, access)
	if errors.Is(err, pg.ErrNoSuchProject) {
		m.record(r, domain.AuditCredentialIssue, grant, req.Project, domain.OutcomeRefused, 0)
		writeError(w, http.StatusNotFound, codeNotFound, "no such active project")
		return
	}
	if err != nil {
		m.log.Error("issue credential", "error", err, "project", req.Project)
		writeError(w, http.StatusInternalServerError, codeInternal, "the credential could not be issued")
		return
	}
	m.record(r, domain.AuditCredentialIssue, grant, req.Project, domain.OutcomeAllowed, 1)
	writeJSON(w, http.StatusCreated, credentialIssued{ID: issued.CredentialID, Name: issued.Name, Project: issued.Project, Access: string(issued.Access), Token: token})
}

// issueProjectCredential is the only way a project credential is minted, whichever surface the
// operator came through. The API and the portal are two ways to reach one set of powers, and a check
// each caller had to remember is how the portal came to mint credentials the API refused (#51).
//
// The project lives in the memory schema and the credential in the registry. The management server is
// the one place that sees both, so the reference is checked here rather than by a constraint.
//   - **Suspended:** a suspended project mints no credential. It would authenticate and reach nothing
//     until somebody resumed the project.
//   - **Missing:** a name no project uses mints none either. That credential would wait for a project
//     of that name and reach it the moment one was created, which is access nobody meant to grant.
//
// What this does not cover: a project suspended between the check and the insert. The credential then
// exists for a suspended project, and authentication refuses it until the project is resumed, like
// every other credential of a suspended project.
func (m *ManagementServer) issueProjectCredential(ctx context.Context, name, project string, access credential.Access) (string, credential.Grant, error) {
	if _, err := m.stores.Projects.Active(ctx, project); err != nil {
		return "", credential.Grant{}, err
	}
	return m.credentials.IssueWithAccess(ctx, name, project, access)
}

func (m *ManagementServer) listCredentials(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req credentialListRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Limit < 0 || req.Limit > 500 {
		writeError(w, http.StatusBadRequest, codeInvalidCredential, "limit is 1..500")
		return
	}
	listed, err := m.credentials.List(r.Context(), req.Project, req.Limit)
	if err != nil {
		m.log.Error("list credentials", "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "credentials could not be listed")
		return
	}
	m.record(r, domain.AuditCredentialList, grant, req.Project, domain.OutcomeAllowed, len(listed))
	writeJSON(w, http.StatusOK, credentialListResponse{Credentials: listed})
}

func (m *ManagementServer) revokeCredential(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req credentialRevokeRequest
	if !decode(w, r, &req) {
		return
	}
	err := m.credentials.Revoke(r.Context(), req.ID)
	if errors.Is(err, credential.ErrUnknown) {
		m.record(r, domain.AuditCredentialRevoke, grant, "", domain.OutcomeRefused, 0)
		writeError(w, http.StatusNotFound, codeNotFound, "no such live credential")
		return
	}
	if err != nil {
		m.log.Error("revoke credential", "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "the credential could not be revoked")
		return
	}
	m.record(r, domain.AuditCredentialRevoke, grant, "", domain.OutcomeAllowed, 1)
	writeJSON(w, http.StatusOK, credentialRevoked{ID: req.ID, Revoked: true})
}

// ── Refusals, erasures, formation, the ledger ─────────────────────────────────────────────────

type projectPageRequest struct {
	Project string `json:"project"`
	Limit   int    `json:"limit"`
}

type erasureListResponse struct {
	Erasures []pg.ErasureReceipt `json:"erasures"`
}

func (m *ManagementServer) projectPage(w http.ResponseWriter, r *http.Request) (projectPageRequest, bool) {
	var req projectPageRequest
	if !decode(w, r, &req) {
		return req, false
	}
	if strings.TrimSpace(req.Project) == "" || req.Limit < 0 || req.Limit > 500 {
		writeError(w, http.StatusBadRequest, codeInvalidProject, "a project is required and limit is 1..500")
		return req, false
	}
	return req, true
}

func (m *ManagementServer) refusalSummary(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	req, ok := m.projectPage(w, r)
	if !ok {
		return
	}
	summary, err := m.stores.Refusals.Summary(r.Context(), req.Project, req.Limit)
	if err != nil {
		m.log.Error("summarise refusals", "error", err, "project", req.Project)
		writeError(w, http.StatusInternalServerError, codeInternal, "refusals could not be summarised")
		return
	}
	m.record(r, domain.AuditRefusalSummary, grant, req.Project, domain.OutcomeAllowed, len(summary.Counts))
	writeJSON(w, http.StatusOK, summary)
}

func (m *ManagementServer) listErasures(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	req, ok := m.projectPage(w, r)
	if !ok {
		return
	}
	receipts, err := m.stores.Eraser.Receipts(r.Context(), m.schema, req.Project, req.Limit)
	if err != nil {
		m.log.Error("list erasures", "error", err, "project", req.Project)
		writeError(w, http.StatusInternalServerError, codeInternal, "erasures could not be listed")
		return
	}
	m.record(r, domain.AuditErasureList, grant, req.Project, domain.OutcomeAllowed, len(receipts))
	writeJSON(w, http.StatusOK, erasureListResponse{Erasures: receipts})
}

type formationScope struct {
	Project   string `json:"project"`
	Suspended bool   `json:"suspended"`
	Stored    *int64 `json:"stored"`
	Formed    *int64 `json:"formed"`
	Parked    int    `json:"parked"`
}

type formationStatusResponse struct {
	Scopes []formationScope `json:"scopes"`
}

func (m *ManagementServer) formationStatus(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	projects, err := m.stores.Projects.List(r.Context())
	if err != nil {
		m.log.Error("list projects", "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "formation status could not be read")
		return
	}
	out := formationStatusResponse{Scopes: []formationScope{}}
	for _, p := range projects {
		f, err := m.stores.Observations.Freshness(r.Context(), m.schema, p.Scope)
		if err != nil {
			m.log.Error("read freshness", "error", err, "project", p.Scope)
			writeError(w, http.StatusInternalServerError, codeInternal, "formation status could not be read")
			return
		}
		scope := formationScope{Project: p.Scope, Suspended: p.Suspended, Parked: f.Parked}
		if f.HasStored {
			stored := f.Stored
			scope.Stored = &stored
		}
		if f.HasFormed {
			formed := f.Formed
			scope.Formed = &formed
		}
		out.Scopes = append(out.Scopes, scope)
	}
	m.record(r, domain.AuditFormationStatus, grant, "", domain.OutcomeAllowed, len(out.Scopes))
	writeJSON(w, http.StatusOK, out)
}

type parkedRequest struct {
	Project string `json:"project"`
	// After is an exclusive log offset; omitted starts from the beginning.
	After *int64 `json:"after"`
	Limit int    `json:"limit"`
}

func (m *ManagementServer) listParked(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req parkedRequest
	if !decode(w, r, &req) {
		return
	}
	after := int64(-1)
	if req.After != nil {
		after = *req.After
	}
	limit := req.Limit
	if limit == 0 {
		limit = 100
	}
	page, err := m.stores.Observations.ParkedPage(r.Context(), m.schema, req.Project, after, limit)
	if err != nil {
		if strings.HasPrefix(err.Error(), "invalid parked page") {
			writeError(w, http.StatusBadRequest, codeInvalidProject, err.Error())
			return
		}
		m.log.Error("list parked turns", "error", err, "project", req.Project)
		writeError(w, http.StatusInternalServerError, codeInternal, "parked turns could not be listed")
		return
	}
	m.record(r, domain.AuditFormationParked, grant, req.Project, domain.OutcomeAllowed, len(page.Items))
	writeJSON(w, http.StatusOK, page)
}

type unparkRequest struct {
	Project       string `json:"project"`
	ObservationID string `json:"observation_id"`
}

type unparkResponse struct {
	ObservationID string `json:"observation_id"`
	Unparked      bool   `json:"unparked"`
}

func (m *ManagementServer) unpark(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req unparkRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Project) == "" || strings.TrimSpace(req.ObservationID) == "" {
		writeError(w, http.StatusBadRequest, codeInvalidProject, "a project and an observation id are required")
		return
	}
	// The store records the unpark itself, with the operator as principal, in the same
	// transaction that changes the turn: the ledger row and the change cannot disagree.
	changed, err := m.stores.Observations.UnparkAudited(r.Context(), m.schema, req.Project, req.ObservationID, grant.CredentialID)
	if err != nil {
		// A turn the project never held is the same answer as one no longer parked: there is
		// nothing to unpark, and the operator did nothing the server failed at.
		if errors.Is(err, pg.ErrFormationTurnNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "no parked turn with that id in that project")
			return
		}
		if strings.HasPrefix(err.Error(), "invalid") {
			writeError(w, http.StatusBadRequest, codeInvalidProject, err.Error())
			return
		}
		m.log.Error("unpark", "error", err, "project", req.Project)
		writeError(w, http.StatusInternalServerError, codeInternal, "the turn could not be unparked")
		return
	}
	if !changed {
		writeError(w, http.StatusNotFound, codeNotFound, "no parked turn with that id in that project")
		return
	}
	writeJSON(w, http.StatusOK, unparkResponse{ObservationID: req.ObservationID, Unparked: true})
}

func (m *ManagementServer) sealAudit(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	seal, err := m.stores.Audit.Seal(r.Context())
	if err != nil {
		m.log.Error("seal the ledger", "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "the ledger could not be sealed")
		return
	}
	m.record(r, domain.AuditAuditSeal, grant, "", domain.OutcomeAllowed, 1)
	writeJSON(w, http.StatusOK, seal)
}

func (m *ManagementServer) verifyAudit(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	verification, err := m.stores.Audit.Verify(r.Context())
	if err != nil {
		m.log.Error("verify the ledger", "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "the ledger could not be verified")
		return
	}
	m.record(r, domain.AuditAuditVerify, grant, "", domain.OutcomeAllowed, 1)
	writeJSON(w, http.StatusOK, verification)
}
