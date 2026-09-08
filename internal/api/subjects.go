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

type subjectGetRequest struct {
	ID                string `json:"id,omitempty"`
	ExternalReference string `json:"external_reference,omitempty"`
}
type subjectListRequest struct {
	After string `json:"after,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

func (s *Server) subjectReady(w http.ResponseWriter) bool {
	if s.subjects == nil {
		writeError(w, 500, codeInternal, "subject storage is unavailable")
		return false
	}
	return true
}
func (s *Server) subjectError(w http.ResponseWriter, r *http.Request, grant credential.Grant, operation string, err error) {
	s.record(r, operation, grant, domain.OutcomeRefused, 0)
	switch {
	case errors.Is(err, pg.ErrInvalidSubject):
		writeError(w, 400, codeInvalidSubject, "UUID identities and bounded subject fields are required")
	case errors.Is(err, pg.ErrSubjectNotFound):
		writeError(w, 404, codeNotFound, "subject not found")
	case errors.Is(err, pg.ErrSubjectConflict):
		writeError(w, 409, codeSubjectConflict, "subject reference, retry or version conflicts; inspect the current entry")
	default:
		s.log.Error("subject operation failed", "operation", operation, "project", grant.Project)
		writeError(w, 500, codeInternal, "subject operation failed")
	}
}
func (s *Server) registerSubject(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request pg.SubjectRegistration
	if !decode(w, r, &request) || !s.subjectReady(w) {
		return
	}
	out, err := s.subjects.Register(r.Context(), grant.Project, grant.CredentialID, request)
	if err != nil {
		s.subjectError(w, r, grant, domain.AuditSubjectRegister, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) getSubject(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request subjectGetRequest
	if !decode(w, r, &request) || !s.subjectReady(w) {
		return
	}
	out, err := s.subjects.Get(r.Context(), grant.Project, request.ID, request.ExternalReference)
	if err != nil {
		s.subjectError(w, r, grant, domain.AuditSubjectGet, err)
		return
	}
	s.record(r, domain.AuditSubjectGet, grant, domain.OutcomeAllowed, 1)
	writeJSON(w, 200, out)
}
func (s *Server) listSubjects(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request subjectListRequest
	if !decode(w, r, &request) || !s.subjectReady(w) {
		return
	}
	if request.Limit == 0 {
		request.Limit = 20
	}
	out, err := s.subjects.List(r.Context(), grant.Project, request.After, request.Limit)
	if err != nil {
		s.subjectError(w, r, grant, domain.AuditSubjectList, err)
		return
	}
	s.record(r, domain.AuditSubjectList, grant, domain.OutcomeAllowed, len(out.Subjects))
	writeJSON(w, 200, out)
}
func (s *Server) updateSubject(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request pg.SubjectUpdate
	if !decode(w, r, &request) || !s.subjectReady(w) {
		return
	}
	out, err := s.subjects.Update(r.Context(), grant.Project, grant.CredentialID, request)
	if err != nil {
		s.subjectError(w, r, grant, domain.AuditSubjectUpdate, err)
		return
	}
	writeJSON(w, 200, out)
}
