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

type recordRetractionRequest struct {
	Records []pg.RecordMutation `json:"records"`
}

// The actor is the authenticated credential, never a body field. Success auditing belongs to the
// mutation transaction; a rejected request records no record IDs, source identifiers or content.
func (s *Server) retractRecords(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request recordRetractionRequest
	if !decode(w, r, &request) {
		return
	}
	if s.records == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "records could not be retracted")
		return
	}
	out, err := s.records.Retract(r.Context(), grant.Project, grant.CredentialID, request.Records)
	if err != nil {
		s.record(r, domain.AuditRecordRetract, grant, domain.OutcomeRefused, 0)
		switch {
		case errors.Is(err, pg.ErrInvalidRecordMutation):
			writeError(w, http.StatusBadRequest, codeInvalidRecordMutation, "record identifiers and current versions are required within the batch limit")
		case errors.Is(err, pg.ErrRecordNotFound):
			writeError(w, http.StatusNotFound, codeNotFound, "record not found")
		case errors.Is(err, pg.ErrRecordConflict):
			writeError(w, http.StatusConflict, codeRecordConflict, "a record changed or was already retracted; inspect its current version")
		case errors.Is(err, pg.ErrRecordMutationLimit):
			writeError(w, http.StatusRequestEntityTooLarge, codeRecordMutationLimit, "the batch exceeds the supported source bound")
		default:
			s.failed(w, err, "record retraction failed", grant.Project, "records could not be retracted")
		}
		return
	}
	writeJSON(w, http.StatusOK, out)
}
