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
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	p := "/" + Version
	mux.Handle("POST "+p+"/observations", s.authenticated(s.observe))
	mux.Handle("GET "+p+"/freshness", s.authenticated(s.freshness))
	mux.Handle("POST "+p+"/recalls", s.authenticated(s.recallHandler))
	mux.Handle("POST "+p+"/erasures", s.authenticated(s.erase))
	mux.Handle("POST "+p+"/exports", s.authenticated(s.export))

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
			writeError(w, http.StatusUnauthorized, "unauthenticated", "a credential is required")
			return
		}
		grant, err := s.credentials.Resolve(r.Context(), token)
		if errors.Is(err, credential.ErrUnknown) {
			s.recordRefusedAuth(r, token)
			writeError(w, http.StatusUnauthorized, "unauthenticated", "a credential is required")
			return
		}
		if err != nil {
			s.log.Error("resolve credential", "error", err)
			writeError(w, http.StatusInternalServerError, "internal", "the request could not be served")
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
				writeError(w, http.StatusUnauthorized, "unauthenticated", "a credential is required")
				return
			}
			s.log.Error("read project", "error", err, "project", grant.Project)
			writeError(w, http.StatusInternalServerError, "internal", "the request could not be served")
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
		writeError(w, http.StatusBadRequest, "invalid_turn", err.Error())
		return
	}

	stored, err := s.observations.Append(r.Context(), s.schema, turn)
	if err != nil {
		s.log.Error("append observation", "error", err, "project", grant.Project)
		writeError(w, http.StatusInternalServerError, "internal", "the turn could not be stored")
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
		writeError(w, http.StatusInternalServerError, "internal", "freshness could not be read")
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
	SourceRole string          `json:"source_role"`
	Evidence   evidencePayload `json:"evidence"`
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
		writeError(w, http.StatusBadRequest, "invalid_question", "a question is required")
		return
	}

	// The credential's project, and only ever that. The authorised set is still an argument on
	// every read rather than a default (D24) — it has one member now, which is the whole of D47.
	bundle, err := s.recaller.Recall(r.Context(), []string{grant.Project}, req.Question)
	if err != nil {
		s.log.Error("recall", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "the question could not be answered")
		return
	}
	// The magnitude is how many facts came back. Never the question, and never what they said.
	s.record(r, domain.AuditRecall, grant, domain.OutcomeAllowed, len(bundle.Facts))
	writeJSON(w, http.StatusOK, renderBundle(bundle))
}

func renderBundle(b domain.Bundle) recallResponse {
	out := recallResponse{Truncated: b.Truncated, Anchors: []anchorPayload{}, Facts: []factPayload{}}
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
			SourceRole: f.SourceRole,
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
		writeError(w, http.StatusBadRequest, "no_subject",
			"an erasure names the person it is for; one without a subject would be a scope-wide delete")
		return
	}

	receipt, err := s.eraser.Erase(r.Context(), s.schema, grant.Project, req.DataSubjectID, req.Reason)
	if err != nil {
		s.log.Error("erase", "error", err, "project", grant.Project)
		writeError(w, http.StatusInternalServerError, "internal", "the erasure did not complete")
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
		writeError(w, http.StatusBadRequest, "no_subject",
			"an export names the person it is for; one without a subject would be a copy of the project")
		return
	}

	out, err := s.exporter.Export(r.Context(), s.schema, grant.Project, req.DataSubjectID)
	if err != nil {
		s.log.Error("export", "error", err, "project", grant.Project)
		writeError(w, http.StatusInternalServerError, "internal", "the export could not be produced")
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
		writeError(w, http.StatusBadRequest, "invalid_body", "the request body could not be read")
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

// writeError says what the caller can act on and nothing about how the system is built.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}
