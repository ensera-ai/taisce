// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/http"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// feedbackListRequest narrows the queue. Both filters are optional and both are server-side: a
// client that could only filter after receiving the page would be reading every open report on the
// project to find the one about its record.
type feedbackListRequest struct {
	RecordID string             `json:"record_id,omitempty"`
	OpenOnly bool               `json:"open_only,omitempty"`
	After    *pg.FeedbackCursor `json:"after,omitempty"`
	Limit    *int               `json:"limit,omitempty"`
}

// Attribution comes from the credential, never from the body — the same rule a correction follows,
// for the same reason: a report whose author is a claim is not evidence of anything.
func (s *Server) recordFeedback(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request pg.FeedbackRequest
	if !decode(w, r, &request) {
		return
	}
	if s.feedback == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "feedback could not be recorded")
		return
	}
	out, err := s.feedback.Record(r.Context(), grant.Project, grant.CredentialID, request)
	if err != nil {
		s.feedbackError(w, r, grant, domain.AuditFeedbackRecord, err, "feedback could not be recorded")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listFeedback(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request feedbackListRequest
	if !decode(w, r, &request) {
		return
	}
	limit := pg.DefaultFeedbackPage
	if request.Limit != nil {
		limit = *request.Limit
	}
	if s.feedback == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "feedback could not be inspected")
		return
	}
	out, err := s.feedback.List(r.Context(), grant.Project, request.RecordID, request.OpenOnly, request.After, limit)
	if err != nil {
		s.feedbackError(w, r, grant, domain.AuditFeedbackList, err, "feedback could not be inspected")
		return
	}
	s.record(r, domain.AuditFeedbackList, grant, domain.OutcomeAllowed, len(out.Feedback))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) promoteFeedback(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request pg.PromotionRequest
	if !decode(w, r, &request) {
		return
	}
	if s.feedback == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "feedback could not be promoted")
		return
	}
	out, err := s.feedback.Promote(r.Context(), grant.Project, grant.CredentialID, request)
	if err != nil {
		s.feedbackError(w, r, grant, domain.AuditFeedbackPromote, err, "feedback could not be promoted")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// feedbackError keeps client-supplied identifiers, note text and database error text out of both the
// ledger and the log. A refused operation is recorded as refused and nothing about its content is.
func (s *Server) feedbackError(w http.ResponseWriter, r *http.Request, grant credential.Grant, operation string, err error, message string) {
	s.record(r, operation, grant, domain.OutcomeRefused, 0)
	switch {
	case errors.Is(err, pg.ErrInvalidFeedback):
		writeError(w, http.StatusBadRequest, codeInvalidFeedback, "a record id, a bounded note and a valid page are required")
	case errors.Is(err, pg.ErrInvalidRecordMutation):
		writeError(w, http.StatusBadRequest, codeInvalidRecordMutation, "the target record and its expected version are required")
	case errors.Is(err, pg.ErrEntityNameLimit):
		writeError(w, http.StatusBadRequest, codeInvalidRecordMutation, "the proposed object exceeds the byte or variant limit; use an existing spelling")
	case errors.Is(err, pg.ErrFeedbackPromoted):
		writeError(w, http.StatusConflict, codeFeedbackPromoted, "this feedback was already promoted")
	case errors.Is(err, pg.ErrRecordConflict):
		writeError(w, http.StatusConflict, codeRecordConflict, "the record changed or is no longer current; inspect its current version")
	case errors.Is(err, pg.ErrFeedbackNotFound), errors.Is(err, pg.ErrRecordNotFound):
		// One answer for "no such feedback", "no such record" and "belongs to another project".
		// Telling them apart would let a caller enumerate what exists elsewhere by watching which
		// refusal came back.
		writeError(w, http.StatusNotFound, codeNotFound, "not found")
	default:
		s.failed(w, err, operation, grant.Project, message)
	}
}
