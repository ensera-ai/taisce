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
	case domain.AuditEntityList, domain.AuditEntityGet, domain.AuditEmbeddingSearch, domain.AuditEntityEmbeddingSearch, domain.AuditReportEmbeddingSearch, domain.AuditMessageGet:
		return true
	case domain.AuditNotificationList, domain.AuditNotificationDeliveries:
		// Reading where a project is told, and whether it was, is inspection. Nominating a
		// destination is not: it is choosing where this deployment makes an outbound request.
		return true
	case domain.AuditFeedbackList:
		// Reading what has been reported about a record is inspection, and the operator who most
		// needs to see the queue is often holding the key that cannot change anything. Recording
		// feedback and promoting it stay out: the first writes a row this deployment retains and
		// erases, and the second is a correction under another name.
		return true
	case domain.AuditFreshness, domain.AuditRecall, domain.AuditContextAssemble, domain.AuditExport, domain.AuditCitationResolve, domain.AuditRecordList, domain.AuditRecordHistory, domain.AuditArtifactGet, domain.AuditArtifactList, domain.AuditSubjectGet, domain.AuditSubjectList:
		return true
	default:
		return false
	}
}
