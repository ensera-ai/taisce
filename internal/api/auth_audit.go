// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
)

const refusedAuthSamplesPerMinute = 16

// refusedAuthBudget bounds anonymous ledger writes per server, without keeping an attacker-keyed
// map. Suppressed attempts contribute to the next sample's magnitude. Counts saturate at PostgreSQL's
// integer limit; pending counts are best effort and disappear on restart, unlike committed samples.
// This is an audit sampling bound, not an authentication request or fleet-wide admission limit.
type refusedAuthBudget struct {
	mu         sync.Mutex
	resetAt    time.Time
	samples    int
	suppressed int
}

func (b *refusedAuthBudget) take(now time.Time) (magnitude int, firstSuppressed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !now.Before(b.resetAt) {
		b.resetAt = now.Add(time.Minute)
		b.samples = 0
	}
	if b.samples == refusedAuthSamplesPerMinute {
		first := b.suppressed == 0
		if b.suppressed < (1<<31)-2 {
			b.suppressed++
		}
		return 0, first
	}
	b.samples++
	magnitude = b.suppressed + 1
	b.suppressed = 0
	return magnitude, false
}

// recordRefusedAuth never receives the presented credential. Even its prefix is arbitrary input,
// and copying it would let an unauthenticated caller put personal content into permanent storage.
// A fixed principal records the failed boundary without claiming to identify an unknown person.
func (s *Server) recordRefusedAuth(r *http.Request) {
	release, ok := s.admission.acquireAudit()
	if !ok {
		return
	}
	defer release()
	magnitude, firstSuppressed := s.authAudits.take(time.Now())
	if magnitude == 0 {
		if firstSuppressed {
			s.log.Warn("anonymous authentication audit sampling is active",
				"samples_per_minute", refusedAuthSamplesPerMinute)
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.audit.Append(ctx, domain.AuditEntry{
		Operation:     domain.AuditAuthenticate,
		Principal:     "unresolved",
		PrincipalKind: domain.PrincipalSystem,
		Outcome:       domain.OutcomeRefused,
		Magnitude:     magnitude,
	}); err != nil {
		s.log.Error("the audit ledger did not record a refused authentication", "error", err)
	}
}
