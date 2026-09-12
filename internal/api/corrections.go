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

type recordCorrectionRequest struct {
	Records []pg.RecordCorrection `json:"records"`
}

// Attribution comes from the credential and retained target; the body cannot replace either.
func (s *Server) correctRecords(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request recordCorrectionRequest
	if !decode(w, r, &request) {
		return
	}
	if s.records == nil {
		writeError(w, 500, codeInternal, "records could not be corrected")
		return
	}
	out, err := s.records.Correct(r.Context(), grant.Project, grant.CredentialID, request.Records)
	if err != nil {
		s.record(r, domain.AuditRecordCorrect, grant, domain.OutcomeRefused, 0)
		switch {
		case errors.Is(err, pg.ErrEntityNameLimit):
			writeError(w, 400, codeInvalidRecordMutation, "entity name exceeds the byte or variant limit; use an existing spelling")
		case errors.Is(err, pg.ErrInvalidRecordMutation), errors.Is(err, pg.ErrUnboundSpeaker):
			writeError(w, 400, codeInvalidRecordMutation, "current record identifiers, versions and bounded corrections are required")
		case errors.Is(err, pg.ErrRecordNotFound):
			writeError(w, 404, codeNotFound, "record not found")
		case errors.Is(err, pg.ErrRecordConflict):
			writeError(w, 409, codeRecordConflict, "a record changed or is no longer current; inspect its current version")
		case errors.Is(err, pg.ErrRecordMutationLimit):
			writeError(w, 413, codeRecordMutationLimit, "the batch exceeds the supported source bound")
		default:
			s.failed(w, err, domain.AuditRecordCorrect, grant.Project, "records could not be corrected")
		}
		return
	}
	writeJSON(w, 200, out)
}
