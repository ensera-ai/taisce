// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/http"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/passage"
)

type passageRequest struct {
	Question      string `json:"question"`
	Limit         *int   `json:"limit,omitempty"`
	Candidates    int    `json:"candidates,omitempty"`
	DataSubjectID string `json:"data_subject_id,omitempty"`
	SourceRole    string `json:"source_role,omitempty"`
}

func (s *Server) searchPassages(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request passageRequest
	if !decode(w, r, &request) {
		return
	}
	limit := passage.DefaultLimit
	if request.Limit != nil {
		limit = *request.Limit
	}
	query := passage.Query{Question: request.Question, Limit: limit, Candidates: request.Candidates, Subject: request.DataSubjectID, Role: request.SourceRole}
	if err := query.Validate(); err != nil {
		s.passageError(w, r, grant, err)
		return
	}
	if s.passages == nil {
		s.passageError(w, r, grant, passage.ErrConfiguration)
		return
	}
	result, err := s.passages.Search(r.Context(), grant.Project, query)
	if err != nil {
		s.passageError(w, r, grant, err)
		return
	}
	// Evidence reads follow the same best-effort, content-free credential audit policy as recall.
	s.record(r, domain.AuditEmbeddingSearch, grant, domain.OutcomeAllowed, len(result.Passages))
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) passageError(w http.ResponseWriter, r *http.Request, grant credential.Grant, err error) {
	s.record(r, domain.AuditEmbeddingSearch, grant, domain.OutcomeRefused, 0)
	switch {
	case errors.Is(err, passage.ErrInvalidQuery):
		writeError(w, http.StatusBadRequest, codeInvalidPassageQuery, "a bounded question and valid passage search controls are required")
	case errors.Is(err, passage.ErrConfiguration), errors.Is(err, passage.ErrProvider), errors.Is(err, pg.ErrEmbeddingNotFound), errors.Is(err, pg.ErrEmbeddingConflict), errors.Is(err, pg.ErrInvalidEmbedding):
		writeError(w, http.StatusServiceUnavailable, codePassageUnavailable, "passage search is unavailable for the configured model generation")
	default:
		s.failed(w, err, "passage search failed", grant.Project, "passages could not be retrieved")
	}
}
