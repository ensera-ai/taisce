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

type recordAssertionRequest struct {
	Records []pg.RecordAssertion `json:"records"`
}

// The credential supplies project and authorship. The transaction owns successful audit, including
// replay counts; client content and low-level storage errors never enter failure diagnostics.
func (s *Server) assertRecords(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request recordAssertionRequest
	if !decode(w, r, &request) {
		return
	}
	if s.records == nil {
		writeError(w, 500, codeInternal, "records could not be asserted")
		return
	}
	out, err := s.records.AssertRecords(r.Context(), grant.Project, grant.CredentialID, request.Records)
	if err != nil {
		s.record(r, domain.AuditRecordAssert, grant, domain.OutcomeRefused, 0)
		switch {
		case errors.Is(err, pg.ErrEntityNameLimit):
			writeError(w, 400, codeInvalidRecordMutation, "entity name exceeds the byte or variant limit; use an existing spelling")
		case errors.Is(err, pg.ErrInvalidRecordMutation), errors.Is(err, pg.ErrUnboundSpeaker):
			writeError(w, 400, codeInvalidRecordMutation, "bounded assertions, an admitted predicate and unique UUID retry keys are required")
		case errors.Is(err, pg.ErrIdempotencyConflict):
			writeError(w, 409, codeIdempotencyConflict, "an assertion retry key already identifies different or erased content")
		case errors.Is(err, pg.ErrRecordConflict):
			writeError(w, 409, codeRecordConflict, "assertion validity conflicts with retained knowledge; inspect the current records")
		default:
			s.failed(w, err, domain.AuditRecordAssert, grant.Project, "records could not be asserted")
		}
		return
	}
	writeJSON(w, 200, out)
}
