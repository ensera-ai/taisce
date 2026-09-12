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

type recordListRequest struct {
	DataSubjectID string           `json:"data_subject_id,omitempty"`
	After         *pg.RecordCursor `json:"after,omitempty"`
	Limit         *int             `json:"limit,omitempty"`
}

type recordHistoryRequest struct {
	ID     string                  `json:"id"`
	Before *pg.RecordHistoryCursor `json:"before,omitempty"`
	Limit  *int                    `json:"limit,omitempty"`
}

func (s *Server) listRecords(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request recordListRequest
	if !decode(w, r, &request) {
		return
	}
	limit := pg.DefaultRecordPage
	if request.Limit != nil {
		limit = *request.Limit
	}
	if s.records == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "records could not be inspected")
		return
	}
	out, err := s.records.List(r.Context(), grant.Project, request.DataSubjectID, request.After, limit)
	if err != nil {
		s.recordReadError(w, r, grant, domain.AuditRecordList, err)
		return
	}
	s.record(r, domain.AuditRecordList, grant, domain.OutcomeAllowed, len(out.Records))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) recordHistory(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request recordHistoryRequest
	if !decode(w, r, &request) {
		return
	}
	limit := pg.DefaultRecordPage
	if request.Limit != nil {
		limit = *request.Limit
	}
	if s.records == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "records could not be inspected")
		return
	}
	out, err := s.records.History(r.Context(), grant.Project, request.ID, request.Before, limit)
	if err != nil {
		s.recordReadError(w, r, grant, domain.AuditRecordHistory, err)
		return
	}
	s.record(r, domain.AuditRecordHistory, grant, domain.OutcomeAllowed, 1+len(out.Previous))
	writeJSON(w, http.StatusOK, out)
}

// Keep client-supplied IDs, subjects and database error text out of both audit and diagnostics.
func (s *Server) recordReadError(w http.ResponseWriter, r *http.Request, grant credential.Grant, operation string, err error) {
	s.record(r, operation, grant, domain.OutcomeRefused, 0)
	switch {
	case errors.Is(err, pg.ErrInvalidRecordPage):
		writeError(w, http.StatusBadRequest, codeInvalidRecordPage, "a valid record page is required")
	case errors.Is(err, pg.ErrRecordNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "record not found")
	default:
		s.failed(w, err, operation, grant.Project, "records could not be inspected")
	}
}
