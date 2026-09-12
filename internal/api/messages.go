// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"net/http"
)

type messageRequest struct {
	ChunkID        string `json:"chunk_id"`
	ByteStart      int    `json:"byte_start,omitempty"`
	ByteLimit      *int   `json:"byte_limit,omitempty"`
	ExpectedDigest string `json:"expected_digest,omitempty"`
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request messageRequest
	if !decode(w, r, &request) {
		return
	}
	limit := pg.MaxMessageWindowBytes
	if request.ByteLimit != nil {
		limit = *request.ByteLimit
	}
	query := pg.MessageWindowQuery{ChunkID: request.ChunkID, ByteStart: request.ByteStart, Limit: limit, ExpectedDigest: request.ExpectedDigest}
	if err := query.Validate(); err != nil {
		s.messageError(w, r, grant, err)
		return
	}
	if s.citations == nil {
		s.messageError(w, r, grant, pg.ErrMessageUnavailable)
		return
	}
	message, err := s.citations.Message(r.Context(), grant.Project, query)
	if err != nil {
		s.messageError(w, r, grant, err)
		return
	}
	s.record(r, domain.AuditMessageGet, grant, domain.OutcomeAllowed, 1)
	writeJSON(w, http.StatusOK, message)
}

func (s *Server) messageError(w http.ResponseWriter, r *http.Request, grant credential.Grant, err error) {
	s.record(r, domain.AuditMessageGet, grant, domain.OutcomeRefused, 0)
	switch {
	case errors.Is(err, pg.ErrInvalidMessageWindow):
		writeError(w, http.StatusBadRequest, codeInvalidMessageWindow, "a valid message reference and bounded UTF-8 byte window are required")
	case errors.Is(err, pg.ErrMessageNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "message not found")
	case errors.Is(err, pg.ErrMessageChanged):
		writeError(w, http.StatusConflict, codeSourceChanged, "the source changed; retrieve a new initial window")
	default:
		s.failed(w, err, "source message read failed", grant.Project, "the source message could not be read")
	}
}
