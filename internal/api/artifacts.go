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

type artifactIDRequest struct {
	ID string `json:"id"`
	// DataSubjectID, when given, requires the object to belong to that person. An application
	// serving many people through one credential has no other way to say "this one is theirs": the
	// credential opens the project, so without this the application's own care is the only thing
	// between one user's session and another's, and care is not a boundary.
	DataSubjectID string `json:"data_subject_id,omitempty"`
}
type artifactDeleteRequest struct {
	ID              string `json:"id"`
	ExpectedVersion string `json:"expected_version"`
	// DataSubjectID, when given, requires the object to belong to that person, for the same reason
	// it does on a read: deleting somebody else's session is the same mistake, spent.
	DataSubjectID string `json:"data_subject_id,omitempty"`
}
type artifactListRequest struct {
	Kind          string `json:"kind,omitempty"`
	NamePrefix    string `json:"name_prefix,omitempty"`
	DataSubjectID string `json:"data_subject_id,omitempty"`
	After         string `json:"after,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}
type artifactDeleted struct {
	Deleted bool `json:"deleted"`
}

func (s *Server) artifactReady(w http.ResponseWriter) bool {
	if s.artifacts == nil {
		writeError(w, 500, codeInternal, "artifact storage is unavailable")
		return false
	}
	return true
}
func (s *Server) artifactError(w http.ResponseWriter, r *http.Request, grant credential.Grant, operation string, err error) {
	s.record(r, operation, grant, domain.OutcomeRefused, 0)
	switch {
	case errors.Is(err, pg.ErrInvalidArtifact):
		writeError(w, 400, codeInvalidArtifact, "a UUID identity, declared owner, state/file kind and bounded fields are required")
	case errors.Is(err, pg.ErrArtifactNotFound):
		writeError(w, 404, codeNotFound, "artifact not found")
	case errors.Is(err, pg.ErrArtifactConflict):
		writeError(w, 409, codeArtifactConflict, "artifact identity or version conflicts; inspect the current object")
	case errors.Is(err, pg.ErrArtifactCapacity):
		writeError(w, 429, codeStorageCapacity, "artifact storage allowance is full; delete retained objects or ask the operator to adjust it")
	default:
		s.log.Error("artifact operation failed", "operation", operation, "project", grant.Project)
		writeError(w, 500, codeInternal, "artifact operation failed")
	}
}
func (s *Server) putArtifact(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request pg.ArtifactPut
	if !decode(w, r, &request) || !s.artifactReady(w) {
		return
	}
	out, err := s.artifacts.Put(r.Context(), grant.Project, grant.CredentialID, request)
	if err != nil {
		s.artifactError(w, r, grant, domain.AuditArtifactPut, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) getArtifact(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request artifactIDRequest
	if !decode(w, r, &request) || !s.artifactReady(w) {
		return
	}
	out, err := s.artifacts.Get(r.Context(), grant.Project, request.ID, request.DataSubjectID)
	if err != nil {
		s.artifactError(w, r, grant, domain.AuditArtifactGet, err)
		return
	}
	s.record(r, domain.AuditArtifactGet, grant, domain.OutcomeAllowed, 1)
	writeJSON(w, 200, out)
}
func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request artifactListRequest
	if !decode(w, r, &request) || !s.artifactReady(w) {
		return
	}
	if request.Limit == 0 {
		request.Limit = 20
	}
	out, err := s.artifacts.List(r.Context(), grant.Project, request.DataSubjectID, request.After, request.Limit, pg.ArtifactFilter{Kind: request.Kind, NamePrefix: request.NamePrefix})
	if err != nil {
		s.artifactError(w, r, grant, domain.AuditArtifactList, err)
		return
	}
	s.record(r, domain.AuditArtifactList, grant, domain.OutcomeAllowed, len(out.Artifacts))
	writeJSON(w, 200, out)
}
func (s *Server) deleteArtifact(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
	var request artifactDeleteRequest
	if !decode(w, r, &request) || !s.artifactReady(w) {
		return
	}
	err := s.artifacts.Delete(r.Context(), grant.Project, grant.CredentialID, request.ID, request.ExpectedVersion, request.DataSubjectID)
	if err != nil {
		s.artifactError(w, r, grant, domain.AuditArtifactDelete, err)
		return
	}
	writeJSON(w, 200, artifactDeleted{Deleted: true})
}
