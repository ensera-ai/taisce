// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package api is the wire boundary: the journey over HTTP rather than through Go packages.
//
// Everything the spine does already exists and is reachable only by importing a package. The
// adapters are the product and they are not written in Go, so a contract they can bind to is the
// difference between a demonstrated design and a usable one.
//
// # What this package is allowed to decide
//
// Transport, and nothing else. It parses, authorises, calls, and renders. Every correctness argument
// — the span rule, the closed vocabulary, the role policy, the residual count — lives below it and
// stays there. A handler that made one of those decisions would be a second place the rule exists,
// and the one that drifts is whichever is not the one with the test named after it.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/entitycandidate"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/notify"
	"github.com/ensera-ai/taisce/internal/passage"
	"github.com/ensera-ai/taisce/internal/recall"
	"github.com/ensera-ai/taisce/internal/reportcandidate"
)

// Version is the contract version, and it is in the path.
//
// In the path rather than negotiated in a header. A version a client must remember to send is a
// version a client will omit, and what it gets then is a default that silently changes meaning under
// it. In the path it is visible in a log line, in a curl, and in the one place a developer looks when
// something behaves unexpectedly. It is permanent from the day the first adapter ships.
const Version = "v1"

// Server is the HTTP surface over one instance.
type Server struct {
	credentials      *credential.Store
	audit            *pg.AuditStore
	exporter         *pg.Exporter
	projects         *pg.ProjectStore
	observations     *pg.ObservationStore
	eraser           *pg.Eraser
	notifications    *pg.NotificationStore
	notifier         *notify.Sender
	recaller         *recall.Recaller
	citations        *pg.CitationStore
	records          *pg.RecordStore
	feedback         *pg.FeedbackStore
	artifacts        *pg.ArtifactStore
	subjects         *pg.SubjectStore
	contexts         *pg.SegmentStore
	passages         *passage.Retriever
	entityCandidates *entitycandidate.Retriever
	reportCandidates *reportcandidate.Retriever
	schema           pg.Schema
	log              *slog.Logger
	authAudits       refusedAuthBudget
	admission        *Admission
}

// Stores names the memory dependencies explicitly so adding a capability cannot shift positional wiring.
type Stores struct {
	Audit        *pg.AuditStore
	Exporter     *pg.Exporter
	Projects     *pg.ProjectStore
	Observations *pg.ObservationStore
	Eraser       *pg.Eraser
	Recaller     *recall.Recaller
	Citations    *pg.CitationStore
	Records      *pg.RecordStore
	// Feedback is reports about records that assert nothing, and the promotion that turns one into
	// a correction or a retraction.
	Feedback  *pg.FeedbackStore
	Artifacts *pg.ArtifactStore
	Subjects  *pg.SubjectStore
	// Contexts serves a subject's history under a budget from the segments the compaction pass wrote.
	Contexts         *pg.SegmentStore
	Passages         *passage.Retriever
	EntityCandidates *entitycandidate.Retriever
	ReportCandidates *reportcandidate.Retriever
	// Notifications and Notifier are the destinations a project nominated and the operator's policy
	// about where this deployment may send. Both absent means the routes refuse, which is what a
	// deployment that has not been configured for notifications should do.
	Notifications *pg.NotificationStore
	Notifier      *notify.Sender
}

// NewServer keeps the credential store separate because it uses the protected registry connection.
func NewServer(credentials *credential.Store, stores Stores, schema pg.Schema, log *slog.Logger, admission ...*Admission) *Server {
	if log == nil {
		log = slog.Default()
	}
	gate, _ := NewAdmission(4, 4)
	if len(admission) > 1 {
		panic("api: only one admission gate may be supplied")
	}
	if len(admission) == 1 {
		if admission[0] == nil {
			panic("api: admission gate is nil")
		}
		gate = admission[0]
	}
	return &Server{
		credentials: credentials, audit: stores.Audit, exporter: stores.Exporter, projects: stores.Projects,
		observations: stores.Observations, eraser: stores.Eraser, recaller: stores.Recaller,
		citations: stores.Citations, records: stores.Records, feedback: stores.Feedback, artifacts: stores.Artifacts, subjects: stores.Subjects, contexts: stores.Contexts,
		passages: stores.Passages, entityCandidates: stores.EntityCandidates, reportCandidates: stores.ReportCandidates,
		notifications: stores.Notifications, notifier: stores.Notifier,
		schema: schema, log: log, admission: gate,
	}
}

// record appends to the ledger, and never fails the request that produced it.
//
// A ledger write that could fail an operation would make it a second thing that has to be working
// for memory to work, and the failure would reach a customer as their write being refused for a
// reason unrelated to their data. A ledger that takes the system down is one an operator switches
// off, and a switched-off ledger records nothing at all.
//
// So a failure here is logged at ERROR and swallowed. That is a real weakness — a gap in the record
// is exactly what the record exists to make impossible — and it is the better of the two, on a table
// whose value is completeness over time rather than atomicity per row.
func (s *Server) record(r *http.Request, operation string, grant credential.Grant,
	outcome string, magnitude int) {

	if err := s.audit.Append(r.Context(), domain.AuditEntry{
		Operation:     operation,
		Principal:     grant.CredentialID,
		PrincipalKind: domain.PrincipalCredential,
		Project:       grant.Project,
		Magnitude:     magnitude,
		Outcome:       outcome,
	}); err != nil {
		s.log.Error("the audit ledger did not record an operation",
			"operation", operation, "principal", grant.CredentialID, "error", err)
	}
}

// ── The surface ───────────────────────────────────────────────────────────────────────────────

// Operation is one thing the surface can be asked to do: a name, and where it is reached.
//
// The name is the ledger's name for the same operation. One identifier runs from the row in
// the audit table, through the wire, to the method an adapter exposes — so "which operation was
// that" has one answer everywhere it is asked, rather than a mapping somebody maintains between an
// operation's public name and its recorded one.
type Operation struct {
	Name   string
	Method string
	Path   string
	// Success is what the operation answers when it worked. Part of the contract because a client
	// that treats any 2xx as success cannot tell an append that was stored from one that was
	// accepted for later, and whether what it sent is readable yet is the whole of that difference.
	Success int
}

// The surface, declared once. A path per operation, because HTTP's own vocabulary is what
// every proxy, log and metric between a caller and us already reads — and dispatching on a body
// field would take that away from the operator to save an adapter four lines.
//
// Declared as data rather than as five registration calls because the set has to be enumerable:
// a test asserts every one of these is refused without a credential and leaves a ledger row, and
// the contract is published from it. A route registered anywhere else is invisible to both, which
// is how a surface acquires an operation nobody checks — and had, before this list existed: an
// export was reachable, and neither test named "every operation" covered it.
type route struct {
	Operation
	handler func(*Server) grantedHandler
	// request and response are zero values of the types this operation reads and writes. They are
	// here so the contract document is rendered from the types the handlers actually use rather
	// than written alongside them: a document describing a field the server does not serve is worse
	// than no document, because it is believed.
	//
	// request is nil for an operation that reads no body.
	request  any
	response any
}

var operations = []route{
	{Operation{domain.AuditMessageGet, http.MethodPost, "/messages/get", http.StatusOK}, func(s *Server) grantedHandler { return s.getMessage }, messageRequest{}, pg.SourceMessageWindow{}},
	{Operation{domain.AuditEmbeddingSearch, http.MethodPost, "/passages/search", http.StatusOK}, func(s *Server) grantedHandler { return s.searchPassages }, passageRequest{}, passage.Page{}},
	{Operation{domain.AuditEntityEmbeddingSearch, http.MethodPost, "/entities/candidates", http.StatusOK}, func(s *Server) grantedHandler { return s.searchEntityCandidates }, entityCandidateRequest{}, entitycandidate.Page{}},
	{Operation{domain.AuditReportEmbeddingSearch, http.MethodPost, "/reports/candidates", http.StatusOK}, func(s *Server) grantedHandler { return s.searchReportCandidates }, reportCandidateRequest{}, reportcandidate.Page{}},
	{Operation{domain.AuditEntityList, http.MethodPost, "/entities/list", http.StatusOK}, func(s *Server) grantedHandler { return s.listEntities }, entityListRequest{}, pg.EntityPage{}},
	{Operation{domain.AuditEntityGet, http.MethodPost, "/entities/get", http.StatusOK}, func(s *Server) grantedHandler { return s.getEntity }, entityGetRequest{}, pg.EntityIdentity{}},
	{Operation{domain.AuditSubjectRegister, http.MethodPost, "/subjects/register", http.StatusOK}, func(s *Server) grantedHandler { return s.registerSubject }, pg.SubjectRegistration{}, pg.SubjectWrite{}},
	{Operation{domain.AuditSubjectGet, http.MethodPost, "/subjects/get", http.StatusOK}, func(s *Server) grantedHandler { return s.getSubject }, subjectGetRequest{}, pg.ManagedSubject{}},
	{Operation{domain.AuditSubjectList, http.MethodPost, "/subjects/list", http.StatusOK}, func(s *Server) grantedHandler { return s.listSubjects }, subjectListRequest{}, pg.SubjectPage{}},
	{Operation{domain.AuditSubjectUpdate, http.MethodPost, "/subjects/update", http.StatusOK}, func(s *Server) grantedHandler { return s.updateSubject }, pg.SubjectUpdate{}, pg.SubjectWrite{}},
	{Operation{domain.AuditArtifactPut, http.MethodPost, "/artifacts/put", http.StatusOK},
		func(s *Server) grantedHandler { return s.putArtifact }, pg.ArtifactPut{}, pg.ArtifactWrite{}},
	{Operation{domain.AuditArtifactGet, http.MethodPost, "/artifacts/get", http.StatusOK},
		func(s *Server) grantedHandler { return s.getArtifact }, artifactIDRequest{}, pg.Artifact{}},
	{Operation{domain.AuditArtifactList, http.MethodPost, "/artifacts/list", http.StatusOK},
		func(s *Server) grantedHandler { return s.listArtifacts }, artifactListRequest{}, pg.ArtifactPage{}},
	{Operation{domain.AuditArtifactDelete, http.MethodPost, "/artifacts/delete", http.StatusOK},
		func(s *Server) grantedHandler { return s.deleteArtifact }, artifactDeleteRequest{}, artifactDeleted{}},
	{Operation{domain.AuditObserve, http.MethodPost, "/observations", http.StatusCreated},
		func(s *Server) grantedHandler { return s.observe }, observeRequest{}, observeResponse{}},
	{Operation{domain.AuditFreshness, http.MethodGet, "/freshness", http.StatusOK},
		func(s *Server) grantedHandler { return s.freshness }, nil, freshnessResponse{}},
	{Operation{domain.AuditRecall, http.MethodPost, "/recalls", http.StatusOK},
		func(s *Server) grantedHandler { return s.recallHandler }, recallRequest{}, recallResponse{}},
	{Operation{domain.AuditContextAssemble, http.MethodPost, "/contexts", http.StatusOK},
		func(s *Server) grantedHandler { return s.assembleContext }, contextRequest{}, contextResponse{}},
	{Operation{domain.AuditErase, http.MethodPost, "/erasures", http.StatusOK},
		func(s *Server) grantedHandler { return s.erase }, eraseRequest{}, eraseResponse{}},
	{Operation{domain.AuditExport, http.MethodPost, "/exports", http.StatusOK},
		func(s *Server) grantedHandler { return s.export }, exportRequest{}, exportResponse{}},
	{Operation{domain.AuditCitationResolve, http.MethodPost, "/citations/resolve", http.StatusOK},
		func(s *Server) grantedHandler { return s.resolveCitation }, citationRequest{}, pg.Citation{}},
	{Operation{domain.AuditNotificationRegister, http.MethodPost, "/notifications/endpoints/register", http.StatusOK},
		func(s *Server) grantedHandler { return s.registerNotificationEndpoint }, notificationRegisterRequest{}, pg.Registered{}},
	{Operation{domain.AuditNotificationList, http.MethodPost, "/notifications/endpoints/list", http.StatusOK},
		func(s *Server) grantedHandler { return s.listNotificationEndpoints }, notificationEndpointListRequest{}, notificationEndpointList{}},
	{Operation{domain.AuditNotificationDisable, http.MethodPost, "/notifications/endpoints/disable", http.StatusOK},
		func(s *Server) grantedHandler { return s.disableNotificationEndpoint }, notificationEndpointRequest{}, notificationDisabled{}},
	{Operation{domain.AuditNotificationDeliveries, http.MethodPost, "/notifications/deliveries/list", http.StatusOK},
		func(s *Server) grantedHandler { return s.listNotificationDeliveries }, notificationDeliveryListRequest{}, notificationDeliveryList{}},
	{Operation{domain.AuditEntityPurge, http.MethodPost, "/entities/purge", http.StatusOK},
		func(s *Server) grantedHandler { return s.purgeEntity }, entityPurgeRequest{}, pg.EntityPurge{}},
	{Operation{domain.AuditRecordList, http.MethodPost, "/records/list", http.StatusOK},
		func(s *Server) grantedHandler { return s.listRecords }, recordListRequest{}, pg.RecordPage{}},
	{Operation{domain.AuditRecordHistory, http.MethodPost, "/records/history", http.StatusOK},
		func(s *Server) grantedHandler { return s.recordHistory }, recordHistoryRequest{}, pg.RecordHistoryPage{}},
	{Operation{domain.AuditRecordAssert, http.MethodPost, "/records/assert", http.StatusOK},
		func(s *Server) grantedHandler { return s.assertRecords }, recordAssertionRequest{}, pg.AssertionBatch{}},
	{Operation{domain.AuditRecordCorrect, http.MethodPost, "/records/correct", http.StatusOK},
		func(s *Server) grantedHandler { return s.correctRecords }, recordCorrectionRequest{}, pg.CorrectionBatch{}},
	{Operation{domain.AuditRecordRetract, http.MethodPost, "/records/retract", http.StatusOK},
		func(s *Server) grantedHandler { return s.retractRecords }, recordRetractionRequest{}, pg.RetractionBatch{}},
	{Operation{domain.AuditFeedbackRecord, http.MethodPost, "/feedback/record", http.StatusOK},
		func(s *Server) grantedHandler { return s.recordFeedback }, pg.FeedbackRequest{}, pg.Feedback{}},
	{Operation{domain.AuditFeedbackList, http.MethodPost, "/feedback/list", http.StatusOK},
		func(s *Server) grantedHandler { return s.listFeedback }, feedbackListRequest{}, pg.FeedbackPage{}},
	{Operation{domain.AuditFeedbackPromote, http.MethodPost, "/feedback/promote", http.StatusOK},
		func(s *Server) grantedHandler { return s.promoteFeedback }, pg.PromotionRequest{}, pg.Promotion{}},
}

// The declaration is checked before anything can serve it, and a bad one stops the binary.
//
// Refusing at startup rather than at the request that hits the bad route: a surface is small, fixed
// and known at build time, so the failure is a programming error and the only question is whether an
// operator finds out from a crash or from a customer.
func init() {
	if err := checkOperations(operations); err != nil {
		panic("api: the declared surface is unservable: " + err.Error())
	}
}

// checkOperations refuses a surface the ledger could not account for.
//
// Two ways it can be wrong, and both are silent otherwise. A name the ledger does not know means
// every call to that operation writes an ERROR to the log and no row — the operation works and is
// unaccounted for, which is the one thing this system claims it is not. And two routes under one
// name means the ledger cannot tell them apart afterwards, so the record says an operation happened
// without saying which.
func checkOperations(rs []route) error {
	seen := map[string]string{}
	for _, o := range rs {
		if !domain.RecordableOperation(o.Name) {
			return fmt.Errorf("operation %q (%s %s) is not one the ledger can name",
				o.Name, o.Method, o.Path)
		}
		if where, dup := seen[o.Name]; dup {
			return fmt.Errorf("operation %q is declared twice: %s and %s %s",
				o.Name, where, o.Method, o.Path)
		}
		seen[o.Name] = o.Method + " " + o.Path
	}
	return nil
}

// Operations is the surface, for anything that has to enumerate it rather than remember it.
//
// Paths are absolute and carry the version, because that is what a caller sends and what a contract
// document has to state. The slice is built fresh so a caller cannot rewrite the surface by holding
// onto it.
func Operations() []Operation {
	out := make([]Operation, 0, len(operations))
	for _, o := range operations {
		out = append(out, Operation{Name: o.Name, Method: o.Method,
			Path: "/" + Version + o.Path, Success: o.Success})
	}
	return out
}

// Handler builds the routes.
//
// Recall and erase are POSTs despite reading rather than writing, and that is deliberate. A question
// is somebody's words and a data subject reference identifies a person; both would end up in an
// access log, a proxy log and a browser history if they travelled in a URL. This product's argument
// is that it can say where personal data went, so it does not scatter it into places it cannot
// account for.
func (s *Server) Handler(readiness ...http.Handler) http.Handler {
	mux := http.NewServeMux()

	// Unauthenticated, and it says nothing. A liveness probe that reports version, database state or
	// configuration is a reconnaissance endpoint that happens to answer probes.
	//
	// Not in the operation table: it performs nothing, is reachable without a credential, and leaves
	// no ledger row. Putting it there would make every property the table asserts have an exception.
	if len(readiness) > 1 {
		panic("one readiness handler is permitted")
	}
	var ready http.Handler
	if len(readiness) == 1 {
		ready = readiness[0]
	}
	registerHealth(mux, ready)

	for _, o := range operations {
		mux.Handle(o.Method+" /"+Version+o.Path, s.authenticated(s.authorized(o.Name, o.handler(s))))
	}

	// The MCP door loops back into the operations above, so it is mounted after them and is not
	// one of them: a tool call is one of those operations, reached by another protocol.
	mux.Handle(http.MethodPost+" "+MCPPath, s.mcpHandler(mux))

	return mux
}

type grantedHandler func(http.ResponseWriter, *http.Request, credential.Grant)

// authenticated refuses anything that does not resolve, without saying which way it failed.
//
// Absent, malformed, unknown and revoked are one answer. Distinguishing them tells a stranger
// whether a token they hold is real, which is the single fact they cannot obtain any other way.
func (s *Server) authenticated(next grantedHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCtx, cancelRequest := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancelRequest()
		r = r.WithContext(requestCtx)
		grant, ok := s.resolveGrant(w, r)
		if !ok {
			return
		}
		releaseWork, ok := s.admission.acquire(grant.Project, grant.CredentialID)
		if !ok {
			writeAdmissionRefusal(w)
			return
		}
		defer releaseWork()
		next(w, r, grant)
	})
}

// resolveGrant is the credential half of authentication: the authentication slot, the bearer, the
// registry lookup and the check that the credential's project still exists. It writes the refusal
// itself and reports whether the caller may go on. It is separate from the work slot so that a
// door which loops back into the operations (the MCP route) can refuse a stranger at the door
// without holding a work slot the looped-back request then needs, which with a per-credential
// limit of one would refuse every tool call as busy.
func (s *Server) resolveGrant(w http.ResponseWriter, r *http.Request) (credential.Grant, bool) {
	releaseAuth, ok := s.admission.authenticate(time.Now())
	if !ok {
		writeAdmissionRefusal(w)
		return credential.Grant{}, false
	}
	defer releaseAuth()
	token, ok := bearer(r.Header.Get("Authorization"))
	if !ok {
		s.recordRefusedAuth(r)
		writeError(w, http.StatusUnauthorized, codeUnauthenticated, "a credential is required")
		return credential.Grant{}, false
	}
	authCtx, cancelAuth := context.WithTimeout(r.Context(), 2*time.Second)
	grant, err := s.credentials.Resolve(authCtx, token)
	cancelAuth()
	if errors.Is(err, credential.ErrUnknown) {
		s.recordRefusedAuth(r)
		writeError(w, http.StatusUnauthorized, codeUnauthenticated, "a credential is required")
		return credential.Grant{}, false
	}
	if err != nil {
		s.failed(w, err, "resolve credential", "", "the request could not be served")
		return credential.Grant{}, false
	}
	// The credential resolved. Whether the project it names still exists is a different
	// question, and it is asked here because no constraint can ask it: the credential is in the
	// registry and the project is in the tenant schema, on opposite sides of the boundary that
	// stops a memory query reading a key.
	//
	// The command that mints a credential checks the project exists, which catches a typo. It
	// cannot catch what happens next — a project suspended, or removed, with credentials for it
	// still in circulation. Without this, such a credential authenticates and writes into a
	// namespace nobody is watching.
	//
	// Refused as UNAUTHENTICATED rather than as a missing project. From the holder's side the
	// fact they can act on is that their credential does not work; which project exists, and
	// whether an operator suspended it, is not theirs to learn from an error message.
	if _, err := s.projects.Active(r.Context(), grant.Project); err != nil {
		if errors.Is(err, pg.ErrNoSuchProject) {
			s.log.Warn("a credential names a project that is gone or suspended",
				"credential", grant.CredentialID, "project", grant.Project)
			s.record(r, domain.AuditAuthenticate, grant, domain.OutcomeRefused, 0)
			writeError(w, http.StatusUnauthorized, codeUnauthenticated, "a credential is required")
			return credential.Grant{}, false
		}
		s.failed(w, err, "read project", grant.Project, "the request could not be served")
		return credential.Grant{}, false
	}
	return grant, true
}

func bearer(header string) (string, bool) {
	const scheme = "Bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", false
	}
	return strings.TrimSpace(header[len(scheme):]), true
}

// ── Observe ───────────────────────────────────────────────────────────────────────────────────

type observeRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	// No project. The credential decides it — a caller cannot name one, so there is nothing
	// to check and nothing to get wrong.
	DataSubjectID string           `json:"data_subject_id"`
	OccurredAt    *time.Time       `json:"occurred_at"`
	Messages      []messagePayload `json:"messages"`
}

type messagePayload struct {
	// If supplied, every message supplies one. Omitted groups mean standalone ordinary messages;
	// tool exchanges require explicit boundaries because roles cannot reconstruct call identity.
	GroupOrdinal *int       `json:"group_ordinal"`
	Role         string     `json:"role"`
	Content      string     `json:"content"`
	OccurredAt   *time.Time `json:"occurred_at"`
}

type observeResponse struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	LogOffset int64  `json:"log_offset"`
}

// observe appends a turn and returns before it has been formed.
//
// Returning after formation would make an append wait on a model call, which turns a write a caller
// is holding a request open for into a variable-latency operation whose worst case is a provider's
// worst case. So the append is durable when this returns, the offset is the caller's receipt, and
// freshness is the separate question of whether a recall would see it yet.
func (s *Server) observe(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req observeRequest
	if !decode(w, r, &req) {
		return
	}
	if err := domain.ValidateMessageCount(len(req.Messages)); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidTurn, err.Error())
		return
	}

	turn := domain.Turn{Scope: grant.Project, DataSubjectID: req.DataSubjectID}
	if req.OccurredAt != nil {
		turn.OccurredAt = req.OccurredAt.UTC()
	}
	explicitGroups := len(req.Messages) > 0 && req.Messages[0].GroupOrdinal != nil
	for i, m := range req.Messages {
		if (m.GroupOrdinal != nil) != explicitGroups {
			writeError(w, http.StatusBadRequest, codeInvalidTurn, "group_ordinal must be supplied for every message or omitted for all")
			return
		}
		group := i
		if explicitGroups {
			group = *m.GroupOrdinal
		} else if m.Role == string(domain.RoleTool) {
			writeError(w, http.StatusBadRequest, codeInvalidTurn, "turns containing tool messages require explicit group ordinals")
			return
		}
		occurred := turn.OccurredAt
		if m.OccurredAt != nil {
			occurred = m.OccurredAt.UTC()
		}
		turn.Messages = append(turn.Messages, domain.Message{
			Ordinal: i, Role: domain.Role(m.Role), Content: m.Content, OccurredAt: occurred, GroupOrdinal: group,
		})
	}

	// Validated by the domain, not here. A second validator in a handler is a second definition of
	// what a turn is, and the one that drifts is the one no store enforces.
	if err := turn.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidTurn, err.Error())
		return
	}

	stored, replayed, err := s.observations.AppendIdempotent(r.Context(), s.schema, turn, req.IdempotencyKey)
	if errors.Is(err, pg.ErrIngestionCapacity) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, codeRateLimited, "unfinished observation capacity is full; retry after formation or erasure with the same idempotency key")
		return
	}
	if errors.Is(err, pg.ErrInvalidIdempotencyKey) {
		writeError(w, http.StatusBadRequest, codeInvalidTurn, "idempotency_key must be a UUID")
		return
	}
	if errors.Is(err, pg.ErrIdempotencyConflict) {
		writeError(w, http.StatusConflict, codeIdempotencyConflict, "idempotency key was already used for a different or erased observation")
		return
	}
	if err != nil {
		s.failed(w, err, "append observation", grant.Project, "the turn could not be stored")
		return
	}
	magnitude := len(turn.Messages)
	if replayed {
		magnitude = 0
	}
	s.record(r, domain.AuditObserve, grant, domain.OutcomeAllowed, magnitude)
	writeJSON(w, http.StatusCreated, observeResponse{
		ID: stored.ID, Scope: stored.Scope, LogOffset: stored.LogOffset,
	})
}

// ── Freshness ─────────────────────────────────────────────────────────────────────────────────

type freshnessResponse struct {
	Scope  string `json:"scope"`
	Stored *int64 `json:"stored"`
	// Formed is absent until the scope's first turn has formed. Absent rather than zero, because
	// zero is a real offset belonging to the first turn, and a caller polling for "formed >= my
	// offset" would be told yes before anything had happened.
	Formed *int64 `json:"formed"`
	// Parked is how many turns the driver gave up on, and it is the exception to Formed. The
	// watermark advances past a turn nobody can form so that one bad turn does not freeze a
	// project's memory — which makes Formed mean "formed, except these". That is only acceptable
	// while it is visible, so it is on the wire rather than left to be discovered.
	Parked int `json:"parked"`
	// Rebuilding is present only while an operator is reinterpreting this project's facts. Its
	// presence is the message: a rebuild publishes source by source, so a project with one in
	// flight answers from the old extractor's reading of what it has not reached and the new one's
	// of what it has, and a bundle taken then is reproducible from neither alone.
	//
	// Here rather than on recall, deliberately. A lookup on the read path is paid by every caller on
	// every question to report a state that changes a few times a year; this is the route a client
	// already polls, and the MCP surface exposes it as a tool.
	Rebuilding *rebuildingState `json:"rebuilding,omitempty"`
}

type rebuildingState struct {
	ReinterpretedThrough  int64 `json:"reinterpreted_through"`
	ReinterpretingThrough int64 `json:"reinterpreting_through"`
	// Acknowledged and Skipped are sources the job has finished with, never a frozen total: a source
	// erased before the job reached it enters neither, so no remainder can be computed from them.
	Acknowledged int64 `json:"sources_acknowledged"`
	Skipped      int64 `json:"sources_skipped"`
}

func (s *Server) freshness(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	f, err := s.observations.Freshness(r.Context(), s.schema, grant.Project)
	if err != nil {
		s.failed(w, err, "read freshness", grant.Project, "freshness could not be read")
		return
	}
	out := freshnessResponse{Scope: f.Scope, Parked: f.Parked}
	if f.HasStored {
		stored := f.Stored
		out.Stored = &stored
	}
	if f.HasFormed {
		formed := f.Formed
		out.Formed = &formed
	}
	if f.Rebuilding != nil {
		out.Rebuilding = &rebuildingState{
			ReinterpretedThrough:  f.Rebuilding.ReinterpretedThrough,
			ReinterpretingThrough: f.Rebuilding.ReinterpretingThrough,
			Acknowledged:          f.Rebuilding.Acknowledged,
			Skipped:               f.Rebuilding.Skipped,
		}
	}
	s.record(r, domain.AuditFreshness, grant, domain.OutcomeAllowed, 0)
	writeJSON(w, http.StatusOK, out)
}

// ── Recall ────────────────────────────────────────────────────────────────────────────────────

type recallRequest struct {
	// Optional attribution filter, not a new authentication or project grant.
	DataSubjectID string `json:"data_subject_id"`
	Question      string `json:"question"`
	// AsOf and AsKnownAt are the two instants a bitemporal read is taken at: what was true of the
	// world then, as this system understood things then. Both are optional and independent — the
	// half a caller does not name is the open interval, which is what we believe now, and is not a
	// timestamp anybody computed.
	//
	// Typed as times rather than strings, so a value that is not RFC 3339 is refused by the decoder
	// with the rest of a malformed body. A string parsed later would be a second refusal path with
	// its own error code, for a mistake the first one already catches.
	AsOf      *time.Time `json:"as_of"`
	AsKnownAt *time.Time `json:"as_known_at"`
	// Hops is how far from the entities the question names the answer may reach. One — the default —
	// is facts about those entities. Two follows one relation further and returns the chain it
	// followed, so "who does Marta's manager work for" is answerable and inspectable.
	//
	// Optional, because a second hop costs every caller who did not need one, and because at two hops
	// a bundle contains facts about things the question never named. Whether that is the answer or
	// noise depends on the question, and only the caller knows which they asked.
	recall.Controls
}

// at is the pair of instants this request asks about.
//
// A request naming one and not the other asks about that time as things are understood now, which is
// the ordinary historical question: what was true in March, as best we know today.
func (r recallRequest) at() domain.AsOf {
	var out domain.AsOf
	if r.AsOf != nil {
		out.Valid = *r.AsOf
	}
	if r.AsKnownAt != nil {
		out.Known = *r.AsKnownAt
	}
	return out
}

type recallResponse struct {
	Controls recall.EffectiveControls `json:"controls"`
	Anchors  []anchorPayload          `json:"anchors"`
	Facts    []factPayload            `json:"facts"`
	// Reports and Passages are the composed surfaces: themes the graph holds, and source
	// words by similarity. Present and empty when a surface returned nothing, so a caller can
	// tell an empty surface from a surface that did not run, which `degraded` names.
	Reports  []reportPayload  `json:"reports"`
	Passages []passagePayload `json:"passages"`
	// Truncated says the limit cut the result, and it is not a ranking verdict: nothing has ranked
	// these, so a cut bundle is missing arbitrary members rather than the least relevant ones. A
	// caller told only the facts would read the cut as a judgement.
	Truncated bool `json:"truncated"`
	// Characters is what these facts cost, in the unit the cut was made in. A caller converts to
	// their own tokeniser's units; we do not, because a token count computed with the wrong
	// tokeniser is wrong in a way the caller cannot correct and would be believed.
	Characters int `json:"characters"`
	// Degraded names parts of the retrieval that did not run. Empty today and present anyway,
	// because a bundle that silently comes back smaller is indistinguishable from a memory with less
	// in it — and a field cannot be added to a published contract without a version.
	Degraded []string `json:"degraded"`
	// Reach is how this bundle was arrived at. Always present, because an empty bundle without it
	// cannot say whether the question named something unknown or something known-and-silent.
	Reach reachPayload `json:"reach"`
}

// reachPayload reports the shape rather than a verdict.
//
// No relevance score, deliberately: scoring candidates is a ranking stage, and ranking waits for the
// measurement that would say whether it helps. What a caller gets instead is exact and needs no
// threshold — how many names the question offered, how many were found, and how the facts distribute
// across them. Everything arriving from one anchor when several matched is a question that named
// something and asked about something else, and it is visible without anybody choosing a number.
type reachPayload struct {
	Terms    int `json:"terms"`
	Anchored int `json:"anchored"`
	// NamedNothingKnown separates "we have never heard of what you asked about" from "we know that
	// and have nothing to say". Both produce an empty bundle and they are different answers.
	NamedNothingKnown bool           `json:"named_nothing_known"`
	FactsPerAnchor    map[string]int `json:"facts_per_anchor"`
}

type anchorPayload struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	// Matched is the term in the question that reached this entity. It is what makes the path
	// inspectable: a caller can see why an entity is in the bundle, not only that it is.
	Matched string `json:"matched"`
}

type factPayload struct {
	FactID     string  `json:"fact_id"`
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	Statement  string  `json:"statement"`
	Confidence float32 `json:"confidence"`
	// ValidFrom and ValidUntil are when this was true of the world, which is not when it was
	// recorded. ValidUntil is null for a fact nothing has superseded and a time for one that has
	// been — so a fact returned by a read of last March says that it stopped holding, rather than
	// arriving indistinguishable from a fact that holds now.
	ValidFrom  time.Time  `json:"valid_from"`
	ValidUntil *time.Time `json:"valid_until"`
	// AnchoredOn is the entity this fact was reached through. A fact can name two anchors, and which
	// one brought it in is the difference between a result and a traversal a caller can check.
	AnchoredOn string `json:"anchored_on"`
	// SourceRole is who said it. Always present, because a fact shown without saying it came from a
	// fetched page asks the reader to trust our sources as if they were their user's words.
	SourceRole string `json:"source_role"`
	// Hops is how many relations away from the question this fact was found, and Path and Via are the
	// chain that reached it: Path holds the entity names in order, Via the relations between them, so
	// Path has one more member than Via.
	//
	// Empty for a one-hop fact, where the chain is a single name and no relations. Present for
	// anything further, because a fact about something the question did not name is only believable
	// if the route to it can be read.
	Hops int      `json:"hops"`
	Path []string `json:"path"`
	Via  []string `json:"via"`
	// CoDerivedWith names the other facts in this bundle from the same message about the same
	// subject. They are not independent of each other, and a reader counting them as corroboration
	// is counting one sentence twice.
	//
	// Always present, empty when there are none: an absent field says "we did not check", and this
	// is checked on every bundle.
	CoDerivedWith []string        `json:"co_derived_with"`
	Evidence      evidencePayload `json:"evidence"`
}

type evidencePayload struct {
	ObservationID string `json:"observation_id"`
	SourceOrdinal int    `json:"source_ordinal"`
	Quote         string `json:"quote"`
	ByteStart     int    `json:"byte_start"`
	ByteEnd       int    `json:"byte_end"`
	// Context is the words around the quote, from the same message. A quote and a span prove a fact
	// was in the message and say nothing about whether the sentence asserted it, hedged it or denied
	// it — which is the difference between a receipt and a legible receipt.
	//
	// Empty when the message is gone, which is what an erasure leaves behind.
	Context string `json:"context"`
	// ContextStart is where the context begins in the message. The quote sits at
	// `byte_start - context_start` inside it, which is what lets a reader highlight the cited words
	// rather than search for them.
	ContextStart int `json:"context_start"`
	// ContextComplete says the context IS the whole message. Without it a window that happened to fit
	// and one that was cut are indistinguishable, and a reader assuming the first has read a sentence
	// that continues.
	ContextComplete bool `json:"context_complete"`
}

// recallHandler answers a question against the scopes the credential authorises AND the caller asked
// for — the intersection, never the union.
//
// A caller naming a scope they do not hold gets a refusal rather than a silent narrowing, because a
// bundle quietly missing a project looks exactly like a project with nothing in it.
func (s *Server) recallHandler(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req recallRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		writeError(w, http.StatusBadRequest, codeInvalidQuestion, "a question is required")
		return
	}

	// The credential's project, and only ever that. The authorised set is still an argument on
	// every read rather than a default — it has one member now, because a credential carries exactly one project.
	bundle, effective, err := s.recaller.RecallWithControls(r.Context(), []string{grant.Project}, req.Question,
		req.at(), req.DataSubjectID, req.Controls)
	if errors.Is(err, recall.ErrInvalidControls) {
		s.record(r, domain.AuditRecall, grant, domain.OutcomeRefused, 0)
		writeError(w, http.StatusBadRequest, codeInvalidRecallControls, "recall controls must fit the server limits and allowed source roles")
		return
	}
	if errors.Is(err, domain.ErrRecallQuestionSize) || errors.Is(err, domain.ErrRecallTerms) || errors.Is(err, domain.ErrRecallMatches) {
		writeError(w, http.StatusBadRequest, codeInvalidQuestion, err.Error())
		return
	}
	if err != nil {
		s.failed(w, err, "recall", "", "the question could not be answered")
		return
	}
	// The magnitude is how many facts came back. Never the question, and never what they said.
	s.record(r, domain.AuditRecall, grant, domain.OutcomeAllowed, len(bundle.Facts))
	out := renderBundle(bundle)
	out.Controls = effective
	writeJSON(w, http.StatusOK, out)
}

type reportPayload struct {
	CommunityID string   `json:"community_id"`
	ReportID    string   `json:"report_id"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	Importance  float32  `json:"importance"`
	Similarity  float64  `json:"similarity"`
	Level       int      `json:"level"`
	Parent      string   `json:"parent,omitempty"`
	Sources     []string `json:"sources"`
}

type passagePayload struct {
	ChunkID    string    `json:"chunk_id"`
	SourceID   string    `json:"source_id"`
	Ordinal    int       `json:"ordinal"`
	Role       string    `json:"role"`
	Quote      string    `json:"quote"`
	Similarity float64   `json:"similarity"`
	OccurredAt time.Time `json:"occurred_at"`
	Complete   bool      `json:"complete"`
}

func renderBundle(b domain.Bundle) recallResponse {
	perAnchor := b.Reach.FactsPerAnchor
	if perAnchor == nil {
		perAnchor = map[string]int{}
	}
	degraded := b.Degraded
	if degraded == nil {
		degraded = []string{}
	}
	out := recallResponse{
		Truncated:  b.Truncated,
		Characters: b.Characters,
		Degraded:   degraded,
		Anchors:    []anchorPayload{},
		Facts:      []factPayload{},
		Reports:    []reportPayload{},
		Passages:   []passagePayload{},
		Reach: reachPayload{
			Terms: b.Reach.Terms, Anchored: b.Reach.Anchored,
			NamedNothingKnown: b.Reach.NamedNothingKnown(), FactsPerAnchor: perAnchor,
		},
	}
	for _, a := range b.Anchors {
		out.Anchors = append(out.Anchors, anchorPayload{
			EntityID: a.Entity.ID, Name: a.Entity.CanonicalName,
			Type: a.Entity.Type, Matched: a.Matched,
		})
	}
	for _, f := range b.Facts {
		out.Facts = append(out.Facts, factPayload{
			FactID: f.ID, Subject: f.Subject, Predicate: f.Predicate, Object: f.Object,
			Statement: f.Statement, Confidence: f.Confidence, AnchoredOn: f.AnchoredOn,
			ValidFrom: f.ValidFrom, ValidUntil: f.ValidUntil,
			SourceRole: f.SourceRole, CoDerivedWith: coDerived(f.CoDerivedWith),
			Hops: f.Hops, Path: names(f.Path), Via: names(f.Via),
			Evidence: evidencePayload{
				ObservationID:   f.Evidence.SourceObservationID,
				SourceOrdinal:   f.Evidence.SourceOrdinal,
				Quote:           f.Evidence.Quote,
				ByteStart:       f.Evidence.ByteStart,
				ByteEnd:         f.Evidence.ByteEnd,
				Context:         f.Evidence.Context.Text,
				ContextStart:    f.Evidence.Context.ByteStart,
				ContextComplete: f.Evidence.Context.Complete,
			},
		})
	}
	for _, h := range b.Reports {
		sources := h.Sources
		if sources == nil {
			sources = []string{}
		}
		out.Reports = append(out.Reports, reportPayload{
			CommunityID: h.CommunityID, ReportID: h.ReportID, Title: h.Title, Summary: h.Summary,
			Importance: h.Importance, Similarity: h.Similarity, Level: h.Level, Parent: h.Parent, Sources: sources,
		})
	}
	for _, p := range b.Passages {
		out.Passages = append(out.Passages, passagePayload{
			ChunkID: p.ChunkID, SourceID: p.SourceID, Ordinal: p.Ordinal, Role: p.Role, Quote: p.Quote,
			Similarity: p.Similarity, OccurredAt: p.OccurredAt, Complete: p.Complete,
		})
	}
	return out
}

// ── Erase ─────────────────────────────────────────────────────────────────────────────────────

// eraseRequest names one thing: the person to erase, or the source observations to erase — a
// document observed project-wide has no person, and its manifest holds its observation ids.
type eraseRequest struct {
	DataSubjectID        string   `json:"data_subject_id"`
	SourceObservationIDs []string `json:"source_observation_ids"`
	Reason               string   `json:"reason"`
}

// maxErasureSources bounds an erasure by source. Each id is one element of one array predicate in
// every statement of the walk, so the cost is linear and small; the bound exists so that a request
// cannot be a project-wide delete spelled out one id at a time. Ten thousand segments is a document
// of two and a half megabytes cut at the smallest ceiling; a larger one is erased in more than one
// request, each with its own receipt.
const maxErasureSources = 10000

type eraseResponse struct {
	RequestID            string         `json:"request_id"`
	Scope                string         `json:"scope"`
	DataSubjectID        string         `json:"data_subject_id"`
	SourceObservationIDs []string       `json:"source_observation_ids,omitempty"`
	CompletedAt          time.Time      `json:"completed_at"`
	Deleted              map[string]int `json:"deleted"`
	// Residual is the product. "Done" is a claim; a count of what still matches, taken after the
	// delete in the same transaction, is a measurement — and it is the one a data protection officer
	// is actually asking for.
	Residual map[string]int `json:"residual"`
	Clean    bool           `json:"clean"`
}

func (s *Server) erase(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req eraseRequest
	if !decode(w, r, &req) {
		return
	}
	subject := strings.TrimSpace(req.DataSubjectID)
	if subject == "" && len(req.SourceObservationIDs) == 0 {
		writeError(w, http.StatusBadRequest, codeNoSubject,
			"an erasure names the person it is for, or the source observations it is for; one naming neither would be a scope-wide delete")
		return
	}
	if subject != "" && len(req.SourceObservationIDs) > 0 {
		writeError(w, http.StatusBadRequest, codeInvalidErasure,
			"an erasure names a person or a set of sources, not both: the receipt says what was asked, and it was one thing")
		return
	}
	if len(req.SourceObservationIDs) > maxErasureSources {
		writeError(w, http.StatusBadRequest, codeInvalidErasure,
			fmt.Sprintf("an erasure names at most %d source observations", maxErasureSources))
		return
	}
	for _, id := range req.SourceObservationIDs {
		if _, err := uuid.Parse(id); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidErasure,
				"an erasure by source names observations by the id each was observed under")
			return
		}
	}

	var receipt domain.Erasure
	var err error
	if subject != "" {
		receipt, err = s.eraser.Erase(r.Context(), s.schema, grant.Project, subject, req.Reason)
	} else {
		receipt, err = s.eraser.EraseSources(r.Context(), s.schema, grant.Project, req.SourceObservationIDs, req.Reason)
	}
	if err != nil {
		s.failed(w, err, "erase", grant.Project, "the erasure did not complete")
		return
	}
	deleted := 0
	for _, n := range receipt.Deleted {
		deleted += n
	}
	s.record(r, domain.AuditErase, grant, domain.OutcomeAllowed, deleted)
	writeJSON(w, http.StatusOK, eraseResponse{
		RequestID: receipt.RequestID, Scope: receipt.Scope, DataSubjectID: receipt.DataSubjectID,
		SourceObservationIDs: receipt.SourceObservationIDs,
		CompletedAt:          receipt.CompletedAt, Deleted: receipt.Deleted, Residual: receipt.Residual,
		Clean: receipt.Clean(),
	})
}

// ── Export ────────────────────────────────────────────────────────────────────────────────────

// exportRequest names one thing, as an erasure does: the person to export, or the turns to export.
// A document observed project-wide has no person, and its manifest holds its observation ids.
type exportRequest struct {
	DataSubjectID        string   `json:"data_subject_id"`
	SourceObservationIDs []string `json:"source_observation_ids"`
}

type exportResponse struct {
	Scope                string   `json:"scope"`
	DataSubjectID        string   `json:"data_subject_id"`
	SourceObservationIDs []string `json:"source_observation_ids,omitempty"`
	// Sections keyed by what produced them, plus the messages everything derives from. Raw rows
	// rather than a shaped view: an export has to include a column a migration added without
	// anybody remembering to add it here.
	Sections map[string][]json.RawMessage `json:"sections"`
	// Rows is the count per section, so a reader can compare an export against an erasure receipt
	// without parsing either. The two walks agreeing is what says neither has forgotten a projection.
	Rows map[string]int `json:"rows"`
}

// export answers what is held about one person.
//
// A POST although it reads, for the same reason recall and erase are: the subject reference
// identifies a person, and in a URL it would land in an access log, a proxy log and a browser
// history — places this product cannot account for.
func (s *Server) export(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req exportRequest
	if !decode(w, r, &req) {
		return
	}
	subject := strings.TrimSpace(req.DataSubjectID)
	if subject == "" && len(req.SourceObservationIDs) == 0 {
		writeError(w, http.StatusBadRequest, codeNoSubject,
			"an export names the person it is for, or the turns it is for; one naming neither would be a copy of the project")
		return
	}
	if subject != "" && len(req.SourceObservationIDs) > 0 {
		writeError(w, http.StatusBadRequest, codeInvalidExport,
			"an export names a person or turns, not both: two selections would make one receipt mean two things")
		return
	}
	if len(req.SourceObservationIDs) > maxErasureSources {
		writeError(w, http.StatusBadRequest, codeInvalidExport,
			fmt.Sprintf("an export names at most %d source observations", maxErasureSources))
		return
	}

	var out domain.Export
	var err error
	if subject != "" {
		out, err = s.exporter.Export(r.Context(), s.schema, grant.Project, subject)
	} else {
		out, err = s.exporter.ExportSources(r.Context(), s.schema, grant.Project, req.SourceObservationIDs)
	}
	switch {
	case errors.Is(err, pg.ErrExportTooLarge):
		writeError(w, http.StatusUnprocessableEntity, codeExportTooLarge,
			fmt.Sprintf("this person holds more than %d rows; export their turns by source instead", pg.MaxExportRows))
		return
	case err != nil:
		s.failed(w, err, "export", grant.Project, "the export could not be produced")
		return
	}

	total := 0
	for _, n := range out.Rows() {
		total += n
	}
	s.record(r, domain.AuditExport, grant, domain.OutcomeAllowed, total)
	writeJSON(w, http.StatusOK, exportResponse{
		Scope: out.Scope, DataSubjectID: out.DataSubjectID, SourceObservationIDs: out.SourceObservationIDs,
		Sections: out.Sections, Rows: out.Rows(),
	})
}

// ── Shared ────────────────────────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status is already written, so this cannot be turned into an error response. It is
		// logged by the caller's own logger rather than swallowed.
		_ = err
	}
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// errorCode is the vocabulary of refusals, and it is part of the contract.
//
// A caller branches on the code and shows the message; that is the whole reason the code exists as a
// separate field. So the set is declared here rather than spelled at each call site, because a code
// invented in a handler is a value external code has to handle and nobody wrote down — and the day it
// appears is the day somebody's error handling falls through to a default it did not choose.
//
// A named type rather than a string so a call site reads as a choice from a list. Go will still
// accept an untyped literal, which is why a test parses this package and refuses one.
type errorCode string

const (
	// codeUnauthenticated is every way a credential can fail to work: absent, malformed, unknown,
	// revoked, or naming a project that is gone. One code, deliberately — see authenticated.
	codeUnauthenticated errorCode = "unauthenticated"
	// codeInvalidBody is a body that could not be read as this operation's request: malformed JSON,
	// an unknown field, or one over the size limit.
	codeInvalidBody errorCode = "invalid_body"
	// codeInvalidTurn is a turn the domain refuses. The message carries the domain's own reason,
	// because a caller can fix it and a generic refusal would send them reading source.
	codeInvalidTurn errorCode = "invalid_turn"
	// A key identifies one logical write, including after its content has been erased.
	codeIdempotencyConflict errorCode = "idempotency_conflict"
	// codeInvalidQuestion is a recall with nothing to answer.
	codeInvalidQuestion errorCode = "invalid_question"
	codeRateLimited     errorCode = "rate_limited"
	// Separate from rate_limited on purpose. A rate limit is this deployment deciding it is busy and
	// is bounded by its own settings; this is the substrate having nothing left, which no per-request
	// budget can prevent and which an operator fixes by changing max_connections or the replica
	// count. A caller retries either the same way, and an operator reading a log needs to tell them
	// apart.
	codeNoCapacity      errorCode = "no_database_capacity"
	codeInvalidCitation errorCode = "invalid_citation"
	codeCitationLimit   errorCode = "citation_limit"
	codeNotFound        errorCode = "not_found"
	// codeInvalidDestination is a notification destination this deployment will not send to, or one
	// already registered. The message names the policy and never what is behind it.
	codeInvalidDestination errorCode = "invalid_destination"
	// codeNotificationsOff is every notification route on a deployment whose operator has named no
	// destination. Refused rather than served empty: an application that registered an endpoint and
	// got a receipt would reasonably believe it would be told.
	codeNotificationsOff            errorCode = "notifications_unavailable"
	codeForbidden                   errorCode = "forbidden"
	codeInvalidRecordPage           errorCode = "invalid_record_page"
	codeInvalidEntityPage           errorCode = "invalid_entity_page"
	codeInvalidPassageQuery         errorCode = "invalid_passage_query"
	codeInvalidEntityCandidateQuery errorCode = "invalid_entity_candidate_query"
	codeInvalidReportCandidateQuery errorCode = "invalid_report_candidate_query"
	codeInvalidMessageWindow        errorCode = "invalid_message_window"
	codeSourceChanged               errorCode = "source_changed"
	codePassageUnavailable          errorCode = "passage_unavailable"
	codeEntityCandidatesUnavailable errorCode = "entity_candidates_unavailable"
	codeReportCandidatesUnavailable errorCode = "report_candidates_unavailable"
	codeInvalidRecallControls       errorCode = "invalid_recall_controls"
	// codeInvalidContext is a context request the server refuses: no subject, or a budget outside the server's.
	codeInvalidContext  errorCode = "invalid_context"
	codeInvalidFeedback errorCode = "invalid_feedback"
	// A second promotion of one feedback is its own code, not a conflict: nothing about the request
	// was wrong, and the caller's correct response is to stop rather than to re-read and retry.
	codeFeedbackPromoted      errorCode = "feedback_already_promoted"
	codeInvalidRecordMutation errorCode = "invalid_record_mutation"
	codeRecordConflict        errorCode = "record_conflict"
	codeRecordMutationLimit   errorCode = "record_mutation_limit"
	codeInvalidArtifact       errorCode = "invalid_artifact"
	codeInvalidSubject        errorCode = "invalid_subject"
	codeSubjectConflict       errorCode = "subject_conflict"
	codeArtifactConflict      errorCode = "artifact_conflict"
	codeStorageCapacity       errorCode = "storage_capacity"
	// codeNoSubject is an erasure or an export that names nobody. Refused rather than treated as
	// scope-wide, which is the difference between a governance operation and an accident.
	codeNoSubject errorCode = "no_subject"
	// codeInvalidErasure is an erasure that names both a person and sources, too many sources, or a
	// source that is not an observation id.
	codeInvalidErasure errorCode = "invalid_erasure"
	// codeInvalidExport is the same refusal for an export, which selects the same two ways.
	codeInvalidExport errorCode = "invalid_export"
	// codeExportTooLarge is a subject holding more rows than one export returns. Refused rather
	// than truncated: an export missing rows nobody counted is the opposite of what it is for, and
	// the caller's next move — export those turns by source — is one they can take.
	codeExportTooLarge errorCode = "export_too_large"
	// codeInternal is anything that went wrong here. It says nothing about what, because what went
	// wrong is a description of how this system is built.
	codeInternal errorCode = "internal"
	// The management surface's refusals.
	codeInvalidProject    errorCode = "invalid_project"
	codeInvalidCredential errorCode = "invalid_credential"
)

// errorCodes is the declared set, in the order the contract publishes them.
//
// What this does not cover: it says which codes exist, not which operation can return which. A
// per-operation set would document better and would be maintained by hand against handlers that
// change, so it would be wrong before it was useful. The honest statement is that any operation can
// return any of these, and that is what the contract says.
var errorCodes = []errorCode{
	codeInvalidMessageWindow, codeSourceChanged,
	codeInvalidPassageQuery, codePassageUnavailable,
	codeInvalidEntityCandidateQuery, codeEntityCandidatesUnavailable,
	codeInvalidReportCandidateQuery, codeReportCandidatesUnavailable,
	codeInvalidEntityPage,
	codeUnauthenticated, codeInvalidBody, codeInvalidTurn,
	codeInvalidQuestion, codeNoSubject, codeInvalidErasure, codeInternal, codeIdempotencyConflict, codeRateLimited, codeNoCapacity,
	codeInvalidCitation, codeCitationLimit, codeInvalidExport, codeExportTooLarge, codeNotFound, codeInvalidDestination, codeNotificationsOff, codeForbidden, codeInvalidRecordPage, codeInvalidRecallControls, codeInvalidContext, codeInvalidRecordMutation, codeRecordConflict, codeRecordMutationLimit, codeInvalidFeedback, codeFeedbackPromoted, codeInvalidArtifact, codeArtifactConflict, codeStorageCapacity, codeInvalidSubject, codeSubjectConflict,
	codeInvalidProject, codeInvalidCredential,
}

// writeError says what the caller can act on and nothing about how the system is built.
func writeError(w http.ResponseWriter, status int, code errorCode, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: string(code), Message: message}})
}

// failed answers a database error that no handler-specific arm recognised.
//
// # Why this is not just a 500
//
// One database failure is the deployment's own doing and clears by itself: the server running out of
// connection slots. A caller did nothing wrong, the condition ends when a sibling process finishes,
// and an observation's idempotency key makes the retry safe. Answering it as an internal error
// tells a caller nothing they can act on — measured as ten documents lost on an eight-A100 node
// — so it is answered retryably, with the same status and header the admission gate uses for
// the same reason.
//
// # Why it is one function rather than an arm in every handler
//
// Rule 8: a rule every future handler has to remember is broken by the handler written under
// pressure. Every default arm calls this, so a route added later is covered by having a default arm
// at all, which it cannot serve without.
//
// # What it does not cover
//
// A handler that recognises the error in an earlier arm never reaches here. That is correct — a
// specific answer beats a general one — but it means a store that folds connection exhaustion into
// its own sentinel hides it from this, and the honest place to fix that is the store.
func (s *Server) failed(w http.ResponseWriter, err error, operation, project, message string) {
	if pg.NoConnectionSlots(err) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, codeNoCapacity,
			"the deployment has no database connection available; retry with backoff and the same idempotency key")
		return
	}
	s.log.Error("operation failed", "operation", operation, "project", project)
	writeError(w, http.StatusInternalServerError, codeInternal, message)
}

// names never renders null, for the reason coDerived does not: an absent list and an empty one read
// the same to a caller and mean different things.
func names(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// coDerived never renders null. An absent list says "we did not check"; this is checked on every
// bundle, so the honest empty answer is an empty list.
func coDerived(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}
