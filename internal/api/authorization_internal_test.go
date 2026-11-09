// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"testing"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
)

func TestUnknownAuthorityAndFutureOperationsFailClosedForReaders(t *testing.T) {
	for _, access := range []credential.Access{"", "admin", credential.ReadOnly} {
		if permitted(access, "future.mutation") {
			t.Fatalf("%q admitted unknown mutation", access)
		}
	}
}

// Every memory operation the service declares is classified for an agent credential, and the ones it
// is refused are exactly the two that destroy data irreversibly and the two that choose where this
// deployment makes outbound requests (D26). A new operation is refused to an agent until somebody
// adds it, and this test is where that decision is seen to be made: it fails for an operation nobody
// classified, whichever way it falls.
func TestAnAgentCredentialReachesEveryOperationButErasurePurgeAndOutboundDestinations(t *testing.T) {
	refused := map[string]bool{
		domain.AuditErase: true, domain.AuditEntityPurge: true,
		domain.AuditNotificationRegister: true, domain.AuditNotificationDisable: true,
	}
	for _, r := range operations {
		name := r.Operation.Name
		if permitted(credential.Agent, name) == refused[name] {
			t.Errorf("%s: agent permitted=%v, and the agent level refuses exactly erasure, purge and notification endpoints", name, !refused[name])
		}
		if !permitted(credential.ReadWrite, name) {
			t.Errorf("%s: read_write is every memory operation, and refused this one", name)
		}
		if permitted(credential.ReadOnly, name) && !permitted(credential.Agent, name) {
			t.Errorf("%s: read_only reaches it and agent does not; each level holds everything the one before it does", name)
		}
	}
	for _, access := range []credential.Access{credential.Agent, "AGENT", "read-write"} {
		if permitted(access, "future.mutation") {
			t.Fatalf("%q admitted an operation nobody classified", access)
		}
	}
}
