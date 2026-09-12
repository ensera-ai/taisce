// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/http"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/entitycandidate"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

type entityCandidateRequest struct {
	Question   string `json:"question"`
	Limit      *int   `json:"limit,omitempty"`
	Candidates int    `json:"candidates,omitempty"`
}

func (s *Server) searchEntityCandidates(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request entityCandidateRequest
	if !decode(w, r, &request) {
		return
	}
	limit := entitycandidate.DefaultLimit
	if request.Limit != nil {
		limit = *request.Limit
	}
	query := entitycandidate.Query{Question: request.Question, Limit: limit, Candidates: request.Candidates}
	if err := query.Validate(); err != nil {
		s.entityCandidateError(w, r, grant, err)
		return
	}
	if s.entityCandidates == nil {
		s.entityCandidateError(w, r, grant, entitycandidate.ErrConfiguration)
		return
	}
	result, err := s.entityCandidates.Search(r.Context(), grant.Project, query)
	if err != nil {
		s.entityCandidateError(w, r, grant, err)
		return
	}
	s.record(r, domain.AuditEntityEmbeddingSearch, grant, domain.OutcomeAllowed, len(result.Candidates))
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) entityCandidateError(w http.ResponseWriter, r *http.Request, grant credential.Grant, err error) {
	s.record(r, domain.AuditEntityEmbeddingSearch, grant, domain.OutcomeRefused, 0)
	switch {
	case errors.Is(err, entitycandidate.ErrInvalidQuery):
		writeError(w, http.StatusBadRequest, codeInvalidEntityCandidateQuery, "a bounded question and valid candidate controls are required")
	case errors.Is(err, entitycandidate.ErrConfiguration), errors.Is(err, entitycandidate.ErrProvider),
		errors.Is(err, pg.ErrEmbeddingNotFound), errors.Is(err, pg.ErrEmbeddingConflict), errors.Is(err, pg.ErrInvalidEmbedding):
		writeError(w, http.StatusServiceUnavailable, codeEntityCandidatesUnavailable, "entity candidates are unavailable for the configured model generation")
	default:
		s.failed(w, err, "entity candidate search failed", grant.Project, "entity candidates could not be retrieved")
	}
}
