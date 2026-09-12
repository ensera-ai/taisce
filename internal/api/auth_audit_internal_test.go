// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefusedAuditSamplingCountsConcurrentAttemptsWithinAFixedBudget(t *testing.T) {
	var budget refusedAuthBudget
	now := time.Now()
	var samples, warnings atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, warn := budget.take(now)
			if n > 0 {
				samples.Add(int32(n))
			}
			if warn {
				warnings.Add(1)
			}
		}()
	}
	wg.Wait()
	if samples.Load() != 16 || warnings.Load() != 1 {
		t.Fatalf("samples=%d warnings=%d", samples.Load(), warnings.Load())
	}
	if n, _ := budget.take(now.Add(time.Minute)); n != 985 {
		t.Fatalf("next sample lost suppressed attempts: %d", n)
	}
	budget.samples = refusedAuthSamplesPerMinute
	budget.suppressed = (1 << 31) - 2
	if n, warn := budget.take(now.Add(time.Minute)); n != 0 || warn {
		t.Fatalf("saturated counter sampled: %d %v", n, warn)
	}
	if n, _ := budget.take(now.Add(2 * time.Minute)); n != (1<<31)-1 {
		t.Fatalf("overflowed durable magnitude: %d", n)
	}
}
