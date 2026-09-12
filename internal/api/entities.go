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

// entityPurgeRequest withdraws an entity the extractor should not have made.
//
// Confirm is the whole safety of the operation and is therefore a field rather than a route: the
// same request without it returns exactly what the confirmed one would do, computed by doing it and
// rolling back. An operator reads that, then sends it again with confirm.
type entityPurgeRequest struct {
	ID      string `json:"id"`
	Reason  string `json:"reason"`
	Confirm bool   `json:"confirm"`
}

func (s *Server) purgeEntity(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var req entityPurgeRequest
	if !decode(w, r, &req) {
		return
	}
	if s.records == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "entities could not be inspected")
		return
	}
	// A confirmed purge's row is written by the purge, inside its transaction, so the deletion and
	// its record commit together. Built here because the principal is the transport's to know.
	row := domain.AuditEntry{Operation: domain.AuditEntityPurge, Principal: grant.CredentialID,
		PrincipalKind: domain.PrincipalCredential, Project: grant.Project, Outcome: domain.OutcomeAllowed}
	out, err := s.records.PurgeEntity(r.Context(), grant.Project, req.ID, req.Reason, req.Confirm, row)
	if err != nil {
		s.entityReadError(w, r, grant, domain.AuditEntityPurge, err)
		return
	}
	// A preview is recorded too, and with a magnitude of zero, because reading what a purge would
	// cost is an operator action worth having in the ledger and is not itself a deletion.
	if out.Previewed {
		s.record(r, domain.AuditEntityPurge, grant, domain.OutcomeAllowed, 0)
	}
	writeJSON(w, http.StatusOK, out)
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
		s.failed(w, err, operation, grant.Project, "entities could not be inspected")
	}
}
