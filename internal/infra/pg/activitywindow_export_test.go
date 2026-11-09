// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"time"
)

// Test-only access to the explicit window, so the external suite can count a ledger of rows it
// placed at chosen times — and can state, over the same rows, what a window whose end came from a
// clock behind the one that stamped them would have missed. Unexported in production precisely so
// that no caller can supply an end of its own.
func (s *AuditStore) ActivityBetweenForTesting(ctx context.Context, project string,
	since, until time.Time, buckets int) (Activity, error) {
	return s.activityBetween(ctx, project, since, until, buckets)
}
