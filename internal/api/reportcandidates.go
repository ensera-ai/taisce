// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/http"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/reportcandidate"
)

type reportCandidateRequest struct {
	Question   string `json:"question"`
	Limit      *int   `json:"limit,omitempty"`
	Candidates int    `json:"candidates,omitempty"`
}

func (s *Server) searchReportCandidates(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request reportCandidateRequest
	if !decode(w, r, &request) {
		return
	}
	limit := reportcandidate.DefaultLimit
	if request.Limit != nil {
		limit = *request.Limit
	}
	query := reportcandidate.Query{Question: request.Question, Limit: limit, Candidates: request.Candidates}
	if err := query.Validate(); err != nil {
		s.reportCandidateError(w, r, grant, err)
		return
	}
	if s.reportCandidates == nil {
		s.reportCandidateError(w, r, grant, reportcandidate.ErrConfiguration)
		return
	}
	result, err := s.reportCandidates.Search(r.Context(), grant.Project, query)
	if err != nil {
		s.reportCandidateError(w, r, grant, err)
		return
	}
	s.record(r, domain.AuditReportEmbeddingSearch, grant, domain.OutcomeAllowed, len(result.Candidates))
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) reportCandidateError(w http.ResponseWriter, r *http.Request, grant credential.Grant, err error) {
	s.record(r, domain.AuditReportEmbeddingSearch, grant, domain.OutcomeRefused, 0)
	switch {
	case errors.Is(err, reportcandidate.ErrInvalidQuery):
		writeError(w, http.StatusBadRequest, codeInvalidReportCandidateQuery, "a bounded question and valid candidate controls are required")
	case errors.Is(err, reportcandidate.ErrConfiguration), errors.Is(err, reportcandidate.ErrProvider),
		errors.Is(err, pg.ErrEmbeddingNotFound), errors.Is(err, pg.ErrEmbeddingConflict), errors.Is(err, pg.ErrInvalidEmbedding):
		writeError(w, http.StatusServiceUnavailable, codeReportCandidatesUnavailable, "report candidates are unavailable for the configured model generation")
	default:
		s.failed(w, err, "report candidate search failed", grant.Project, "report candidates could not be retrieved")
	}
}
