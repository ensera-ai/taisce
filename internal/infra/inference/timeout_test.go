// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"testing"
	"time"
)

// The client's own timeout must follow the configured budget: if it stayed at a lower constant, the
// deadline that fired would not be the one the operator set, and the failure would misreport it.
func TestTheModelClientTimeoutFollowsTheConfiguredBudget(t *testing.T) {
	if got := NewModel(Config{Timeout: 15 * time.Minute}).client.Timeout; got != 15*time.Minute {
		t.Fatalf("configured budget not honoured: %s", got)
	}
	if got := NewModel(Config{}).client.Timeout; got != 2*time.Minute {
		t.Fatalf("zero budget should select the two-minute default, got %s", got)
	}
	if got := NewModel(Config{Timeout: -time.Second}).client.Timeout; got != 2*time.Minute {
		t.Fatalf("negative budget should select the two-minute default, got %s", got)
	}
}
