// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation

import (
	"context"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// The driver installs reporting before it starts. Direct worker users may omit it.
// This runs on the actual processing path; no independent timer can mask a blocked drain.
func (w *Worker) heartbeat(ctx context.Context, db pg.HealthExecutor, formed, failed int64) {
	if w.health != nil {
		w.health(ctx, db, formed, failed)
	}
}
