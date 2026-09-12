// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package domain

import "testing"

func TestProjectRebuildControlOperationsAreAuditable(t *testing.T) {
	for _, operation := range []string{AuditRebuildStart, AuditRebuildCancel} {
		if !RecordableOperation(operation) {
			t.Fatalf("operator action is not auditable: %s", operation)
		}
	}
}
