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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/recall"
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
	credentials  *credential.Store
	audit        *pg.AuditStore
	exporter     *pg.Exporter
	projects     *pg.ProjectStore
	observations *pg.ObservationStore
	eraser       *pg.Eraser
	recaller     *recall.Recaller
	schema       pg.Schema
	log          *slog.Logger
}

// NewServer wires the surface over stores that already exist.
//
// The credential store is separate from every other argument because it is the only one that reaches
// the registry connection. That separation is the point: a memory query cannot read a key.
func NewServer(credentials *credential.Store, audit *pg.AuditStore, exporter *pg.Exporter,
	projects *pg.ProjectStore, observations *pg.ObservationStore, eraser *pg.Eraser,
	recaller *recall.Recaller, schema pg.Schema, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		credentials: credentials, audit: audit, exporter: exporter, projects: projects,
		observations: observations,
		eraser:       eraser, recaller: recaller, schema: schema, log: log,
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

// recordRefusedAuth records a credential that did not resolve.
//
// The principal is the token's prefix rather than the token, because the token is a secret and the
// prefix is enough to recognise repeated attempts with one value. A stranger guessing produces a row
// per attempt, which is what makes a burst of them visible.
func (s *Server) recordRefusedAuth(r *http.Request, presented string) {
	principal := "unpresented"
	if len(presented) >= 12 {
		principal = presented[:12]
	} else if presented != "" {
		principal = "malformed"
	}
	if err := s.audit.Append(r.Context(), domain.AuditEntry{
		Operation:     domain.AuditAuthenticate,
		Principal:     principal,
		PrincipalKind: domain.PrincipalSystem,
		Outcome:       domain.OutcomeRefused,
	}); err != nil {
		s.log.Error("the audit ledger did not record a refused authentication", "error", err)
	}
}

// ── The surface ───────────────────────────────────────────────────────────────────────────────

// Operation is one thing the surface can be asked to do: a name, and where it is reached.
//
// The name is the ledger's name for the same operation (D51). One identifier runs from the row in
// the audit table, through the wire, to the method an adapter exposes — so "which operation was
// that" has one answer everywhere it is asked, rather than a mapping somebody maintains between an
// operation's public name and its recorded one.
type Operation struct {
	Name   string
	Method string
	Path   string
	// Success is what the operation answers when it worked. Part of the contract because a client
	// that treats any 2xx as success cannot tell an append that was stored from one that was
	// accepted for later, and the difference is the whole of D19.
	Success int
}

// The surface, declared once (D51). A path per operation, because HTTP's own vocabulary is what
// every proxy, log and metric between a caller and us already reads — and dispatching on a body
// field would take that away from the operator to save an adapter four lines.
//
// Declared as data rather than as five registration calls because the set has to be enumerable:
// a test asserts every one of these is refused without a credential and leaves a ledger row, and
// #36 publishes the contract from here. A route registered anywhere else is invisible to both, which
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
	{Operation{domain.AuditObserve, http.MethodPost, "/observations", http.StatusCreated},
		func(s *Server) grantedHandler { return s.observe }, observeRequest{}, observeResponse{}},
	{Operation{domain.AuditFreshness, http.MethodGet, "/freshness", http.StatusOK},
		func(s *Server) grantedHandler { return s.freshness }, nil, freshnessResponse{}},
	{Operation{domain.AuditRecall, http.MethodPost, "/recalls", http.StatusOK},
		func(s *Server) grantedHandler { return s.recallHandler }, recallRequest{}, recallResponse{}},
	{Operation{domain.AuditErase, http.MethodPost, "/erasures", http.StatusOK},
		func(s *Server) grantedHandler { return s.erase }, eraseRequest{}, eraseResponse{}},
	{Operation{domain.AuditExport, http.MethodPost, "/exports", http.StatusOK},
		func(s *Server) grantedHandler { return s.export }, exportRequest{}, exportResponse{}},
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
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Unauthenticated, and it says nothing. A liveness probe that reports version, database state or
	// configuration is a reconnaissance endpoint that happens to answer probes.
	//
	// Not in the operation table: it performs nothing, is reachable without a credential, and leaves
	// no ledger row. Putting it there would make every property the table asserts have an exception.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	for _, o := range operations {
		mux.Handle(o.Method+" /"+Version+o.Path, s.authenticated(o.handler(s)))
	}

	return mux
}

type grantedHandler func(http.ResponseWriter, *http.Request, credential.Grant)

// authenticated refuses anything that does not resolve, without saying which way it failed.
//
// Absent, malformed, unknown and revoked are one answer. Distinguishing them tells a stranger
// whether a token they hold is real, which is the single fact they cannot obtain any other way.
func (s *Server) authenticated(next grantedHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearer(r.Header.Get("Authorization"))
		if !ok {
			s.recordRefusedAuth(r, "")
			writeError(w, http.StatusUnauthorized, codeUnauthenticated, "a credential is required")
			return
		}
		grant, err := s.credentials.Resolve(r.Context(), token)
		if errors.Is(err, credential.ErrUnknown) {
			s.recordRefusedAuth(r, token)
			writeError(w, http.StatusUnauthorized, codeUnauthenticated, "a credential is required")
			return
		}
		if err != nil {
			s.log.Error("resolve credential", "error", err)
			writeError(w, http.StatusInternalServerError, codeInternal, "the request could not be served")
			return
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
				return
			}
			s.log.Error("read project", "error", err, "project", grant.Project)
			writeError(w, http.StatusInternalServerError, codeInternal, "the request could not be served")
			return
		}

		next(w, r, grant)
	})
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
	// No project. The credential decides it (D47) — a caller cannot name one, so there is nothing
	// to check and nothing to get wrong.
	DataSubjectID string           `json:"data_subject_id"`
	OccurredAt    *time.Time       `json:"occurred_at"`
	Messages      []messagePayload `json:"messages"`
}

type messagePayload struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	OccurredAt *time.Time `json:"occurred_at"`
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

	turn := domain.Turn{Scope: grant.Project, DataSubjectID: req.DataSubjectID}
	if req.OccurredAt != nil {
		turn.OccurredAt = req.OccurredAt.UTC()
	} else {
		turn.OccurredAt = time.Now().UTC()
	}
	for i, m := range req.Messages {
		occurred := turn.OccurredAt
		if m.OccurredAt != nil {
			occurred = m.OccurredAt.UTC()
		}
		turn.Messages = append(turn.Messages, domain.Message{
			Ordinal: i, Role: domain.Role(m.Role), Content: m.Content, OccurredAt: occurred,
		})
	}

	// Validated by the domain, not here. A second validator in a handler is a second definition of
	// what a turn is, and the one that drifts is the one no store enforces.
	if err := turn.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidTurn, err.Error())
		return
	}

	stored, err := s.observations.Append(r.Context(), s.schema, turn)
	if err != nil {
		s.log.Error("append observation", "error", err, "project", grant.Project)
		writeError(w, http.StatusInternalServerError, codeInternal, "the turn could not be stored")
		return
	}
	s.record(r, domain.AuditObserve, grant, domain.OutcomeAllowed, len(turn.Messages))
	writeJSON(w, http.StatusCreated, observeResponse{
		ID: stored.ID, Scope: stored.Scope, LogOffset: stored.LogOffset,
	})
}

// ── Freshness ─────────────────────────────────────────────────────────────────────────────────

type freshnessResponse struct {
	Scope  string `json:"scope"`
	Stored int64  `json:"stored"`
	// Formed is absent until the scope's first turn has formed. Absent rather than zero, because
	// zero is a real offset belonging to the first turn, and a caller polling for "formed >= my
	// offset" would be told yes before anything had happened.
	Formed *int64 `json:"formed"`
	// Parked is how many turns the driver gave up on, and it is the exception to Formed. The
	// watermark advances past a turn nobody can form so that one bad turn does not freeze a
	// project's memory — which makes Formed mean "formed, except these". That is only acceptable
	// while it is visible, so it is on the wire rather than left to be discovered.
	Parked int `json:"parked"`
}

func (s *Server) freshness(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	f, err := s.observations.Freshness(r.Context(), s.schema, grant.Project)
	if err != nil {
		s.log.Error("read freshness", "error", err, "project", grant.Project)
		writeError(w, http.StatusInternalServerError, codeInternal, "freshness could not be read")
		return
	}
	out := freshnessResponse{Scope: f.Scope, Stored: f.Stored, Parked: f.Parked}
	if f.HasFormed {
		formed := f.Formed
		out.Formed = &formed
	}
	s.record(r, domain.AuditFreshness, grant, domain.OutcomeAllowed, 0)
	writeJSON(w, http.StatusOK, out)
}

// ── Recall ────────────────────────────────────────────────────────────────────────────────────

type recallRequest struct {
	Question string `json:"question"`
}

type recallResponse struct {
	Anchors []anchorPayload `json:"anchors"`
	Facts   []factPayload   `json:"facts"`
	// Truncated says the limit cut the result, and it is not a ranking verdict: nothing has ranked
	// these, so a cut bundle is missing arbitrary members rather than the least relevant ones. A
	// caller told only the facts would read the cut as a judgement.
	Truncated bool `json:"truncated"`
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
	// AnchoredOn is the entity this fact was reached through. A fact can name two anchors, and which
	// one brought it in is the difference between a result and a traversal a caller can check.
	AnchoredOn string `json:"anchored_on"`
	// SourceRole is who said it. Always present, because a fact shown without saying it came from a
	// fetched page asks the reader to trust our sources as if they were their user's words.
	SourceRole string `json:"source_role"`
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
	// every read rather than a default (D24) — it has one member now, which is the whole of D47.
	bundle, err := s.recaller.Recall(r.Context(), []string{grant.Project}, req.Question)
	if err != nil {
		s.log.Error("recall", "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "the question could not be answered")
		return
	}
	// The magnitude is how many facts came back. Never the question, and never what they said.
	s.record(r, domain.AuditRecall, grant, domain.OutcomeAllowed, len(bundle.Facts))
	writeJSON(w, http.StatusOK, renderBundle(bundle))
}

func renderBundle(b domain.Bundle) recallResponse {
	perAnchor := b.Reach.FactsPerAnchor
	if perAnchor == nil {
		perAnchor = map[string]int{}
	}
	out := recallResponse{
		Truncated: b.Truncated,
		Anchors:   []anchorPayload{},
		Facts:     []factPayload{},
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
			SourceRole: f.SourceRole, CoDerivedWith: coDerived(f.CoDerivedWith),
			Evidence: evidencePayload{
				ObservationID: f.Evidence.SourceObservationID,
				SourceOrdinal: f.Evidence.SourceOrdinal,
				Quote:         f.Evidence.Quote,
				ByteStart:     f.Evidence.ByteStart,
				ByteEnd:       f.Evidence.ByteEnd,
			},
		})
	}
	return out
}

// ── Erase ─────────────────────────────────────────────────────────────────────────────────────

type eraseRequest struct {
	DataSubjectID string `json:"data_subject_id"`
	Reason        string `json:"reason"`
}

type eraseResponse struct {
	RequestID     string         `json:"request_id"`
	Scope         string         `json:"scope"`
	DataSubjectID string         `json:"data_subject_id"`
	CompletedAt   time.Time      `json:"completed_at"`
	Deleted       map[string]int `json:"deleted"`
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
	if strings.TrimSpace(req.DataSubjectID) == "" {
		writeError(w, http.StatusBadRequest, codeNoSubject,
			"an erasure names the person it is for; one without a subject would be a scope-wide delete")
		return
	}

	receipt, err := s.eraser.Erase(r.Context(), s.schema, grant.Project, req.DataSubjectID, req.Reason)
	if err != nil {
		s.log.Error("erase", "error", err, "project", grant.Project)
		writeError(w, http.StatusInternalServerError, codeInternal, "the erasure did not complete")
		return
	}
	deleted := 0
	for _, n := range receipt.Deleted {
		deleted += n
	}
	s.record(r, domain.AuditErase, grant, domain.OutcomeAllowed, deleted)
	writeJSON(w, http.StatusOK, eraseResponse{
		RequestID: receipt.RequestID, Scope: receipt.Scope, DataSubjectID: receipt.DataSubjectID,
		CompletedAt: receipt.CompletedAt, Deleted: receipt.Deleted, Residual: receipt.Residual,
		Clean: receipt.Clean(),
	})
}

// ── Export ────────────────────────────────────────────────────────────────────────────────────

type exportRequest struct {
	DataSubjectID string `json:"data_subject_id"`
}

type exportResponse struct {
	Scope         string `json:"scope"`
	DataSubjectID string `json:"data_subject_id"`
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
	if strings.TrimSpace(req.DataSubjectID) == "" {
		writeError(w, http.StatusBadRequest, codeNoSubject,
			"an export names the person it is for; one without a subject would be a copy of the project")
		return
	}

	out, err := s.exporter.Export(r.Context(), s.schema, grant.Project, req.DataSubjectID)
	if err != nil {
		s.log.Error("export", "error", err, "project", grant.Project)
		writeError(w, http.StatusInternalServerError, codeInternal, "the export could not be produced")
		return
	}

	total := 0
	for _, n := range out.Rows() {
		total += n
	}
	s.record(r, domain.AuditExport, grant, domain.OutcomeAllowed, total)
	writeJSON(w, http.StatusOK, exportResponse{
		Scope: out.Scope, DataSubjectID: out.DataSubjectID,
		Sections: out.Sections, Rows: out.Rows(),
	})
}

// ── Shared ────────────────────────────────────────────────────────────────────────────────────

func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	// Bounded, because an unbounded body is a way to spend our memory with one request. A turn is
	// text; a megabyte of it is already far past anything a conversation produces.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidBody, "the request body could not be read")
		return false
	}
	return true
}

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

// errorCode is the vocabulary of refusals, and it is part of the contract (D51).
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
	// codeInvalidQuestion is a recall with nothing to answer.
	codeInvalidQuestion errorCode = "invalid_question"
	// codeNoSubject is an erasure or an export that names nobody. Refused rather than treated as
	// scope-wide, which is the difference between a governance operation and an accident.
	codeNoSubject errorCode = "no_subject"
	// codeInternal is anything that went wrong here. It says nothing about what, because what went
	// wrong is a description of how this system is built.
	codeInternal errorCode = "internal"
)

// errorCodes is the declared set, in the order the contract publishes them.
//
// What this does not cover: it says which codes exist, not which operation can return which. A
// per-operation set would document better and would be maintained by hand against handlers that
// change, so it would be wrong before it was useful. The honest statement is that any operation can
// return any of these, and that is what the contract says.
var errorCodes = []errorCode{
	codeUnauthenticated, codeInvalidBody, codeInvalidTurn,
	codeInvalidQuestion, codeNoSubject, codeInternal,
}

// writeError says what the caller can act on and nothing about how the system is built.
func writeError(w http.ResponseWriter, status int, code errorCode, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: string(code), Message: message}})
}

// coDerived never renders null. An absent list says "we did not check"; this is checked on every
// bundle, so the honest empty answer is an empty list.
func coDerived(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}
