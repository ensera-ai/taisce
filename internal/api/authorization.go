// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
)

// authorized runs after authentication, project checks and admission, but before parsing a body or
// executing memory work. New operations are denied to read-only keys until explicitly admitted.
func (s *Server) authorized(operation string, next grantedHandler) grantedHandler {
	return func(w http.ResponseWriter, r *http.Request, grant credential.Grant) {
		if !permitted(grant.Access, operation) {
			s.record(r, operation, grant, domain.OutcomeRefused, 0)
			writeError(w, http.StatusForbidden, codeForbidden, "the credential does not permit this operation")
			return
		}
		next(w, r, grant)
	}
}

func permitted(access credential.Access, operation string) bool {
	if access == credential.ReadWrite {
		return true
	}
	if access != credential.ReadOnly {
		return false
	}
	switch operation {
	case domain.AuditFreshness, domain.AuditRecall, domain.AuditExport, domain.AuditCitationResolve:
		return true
	default:
		return false
	}
}
