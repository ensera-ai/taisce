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

type citationRequest struct {
	ID    string             `json:"id"`
	After *pg.CitationCursor `json:"after,omitempty"`
	Limit *int               `json:"limit,omitempty"`
}

func (s *Server) resolveCitation(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request citationRequest
	if !decode(w, r, &request) {
		return
	}
	limit := pg.DefaultCitationEvidence
	if request.Limit != nil {
		limit = *request.Limit
	}
	if s.citations == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "the citation could not be resolved")
		return
	}
	out, err := s.citations.Resolve(r.Context(), grant.Project, request.ID, request.After, limit)
	if err != nil {
		s.record(r, domain.AuditCitationResolve, grant, domain.OutcomeRefused, 0)
		switch {
		case errors.Is(err, pg.ErrInvalidCitation):
			writeError(w, http.StatusBadRequest, codeInvalidCitation, "a fact UUID and a valid evidence page are required")
		case errors.Is(err, pg.ErrCitationNotFound):
			writeError(w, http.StatusNotFound, codeNotFound, "citation not found")
		case errors.Is(err, pg.ErrCitationLimit):
			writeError(w, http.StatusUnprocessableEntity, codeCitationLimit, "citation source exceeds supported text bounds; use the subject export")
		default:
			s.failed(w, err, "resolve citation failed", grant.Project, "the citation could not be resolved")
		}
		return
	}
	s.record(r, domain.AuditCitationResolve, grant, domain.OutcomeAllowed, len(out.Evidence))
	writeJSON(w, http.StatusOK, out)
}
