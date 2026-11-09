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
	switch access {
	case credential.ReadWrite:
		return true
	case credential.Agent:
		return agentPermitted(operation)
	case credential.ReadOnly:
		return readOnlyPermitted(operation)
	default:
		return false
	}
}

// agentPermitted is an allowlist, not "everything except erasure", for the reason the read-only list
// is: a new operation is refused to an agent key until somebody decides it belongs there. A
// destructive operation added later and forgotten here is refused, which is the failure that can
// be noticed; admitted by default, it would be the failure nobody notices until it is used.
//
// What is left out is the point of the level (D26): erasing a person and purging an entity delete
// data with no way back, and an agent process is where a key is most exposed to text written by
// somebody else. Retraction, correction and artifact deletion stay in: each is an agent's ordinary
// work, and a retraction keeps the history it withdraws from. Registering or disabling a
// notification endpoint is out too, though it destroys nothing: it chooses where this deployment
// makes outbound requests, which is the operator's decision and no agent's work.
func agentPermitted(operation string) bool {
	if readOnlyPermitted(operation) {
		return true
	}
	switch operation {
	case domain.AuditObserve, domain.AuditRecordAssert, domain.AuditRecordCorrect, domain.AuditRecordRetract,
		domain.AuditFeedbackRecord, domain.AuditFeedbackPromote,
		domain.AuditArtifactPut, domain.AuditArtifactDelete,
		domain.AuditSubjectRegister, domain.AuditSubjectUpdate:
		return true
	default:
		return false
	}
}

func readOnlyPermitted(operation string) bool {
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
