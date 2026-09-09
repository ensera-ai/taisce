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

type entityListRequest struct {
	After *pg.EntityCursor `json:"after,omitempty"`
	Limit *int             `json:"limit,omitempty"`
}
type entityGetRequest struct {
	ID string `json:"id"`
}

func (s *Server) listEntities(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req entityListRequest
	if !decode(w, r, &req) {
		return
	}
	limit := pg.DefaultRecordPage
	if req.Limit != nil {
		limit = *req.Limit
	}
	if s.records == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "entities could not be inspected")
		return
	}
	out, err := s.records.ListEntities(r.Context(), grant.Project, req.After, limit)
	if err != nil {
		s.entityReadError(w, r, grant, domain.AuditEntityList, err)
		return
	}
	s.record(r, domain.AuditEntityList, grant, domain.OutcomeAllowed, len(out.Entities))
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) getEntity(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req entityGetRequest
	if !decode(w, r, &req) {
		return
	}
	if s.records == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "entities could not be inspected")
		return
	}
	out, err := s.records.InspectEntity(r.Context(), grant.Project, req.ID)
	if err != nil {
		s.entityReadError(w, r, grant, domain.AuditEntityGet, err)
		return
	}
	s.record(r, domain.AuditEntityGet, grant, domain.OutcomeAllowed, 1)
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) entityReadError(w http.ResponseWriter, r *http.Request, grant credential.Grant, operation string, err error) {
	s.record(r, operation, grant, domain.OutcomeRefused, 0)
	switch {
	case errors.Is(err, pg.ErrInvalidEntityPage):
		writeError(w, http.StatusBadRequest, codeInvalidEntityPage, "a valid entity ID or page is required")
	case errors.Is(err, pg.ErrEntityNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "entity not found")
	default:
		s.log.Error("entity inspection failed", "operation", operation, "project", grant.Project)
		writeError(w, http.StatusInternalServerError, codeInternal, "entities could not be inspected")
	}
}
