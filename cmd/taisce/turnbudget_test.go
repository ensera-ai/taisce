// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"testing"
	"time"
)

// The budget is the operator's only bound on a document-sized turn, so it must be honoured when
// set, kept at the default when absent, and refused rather than ignored when it cannot be a bound.
func TestTheTurnBudgetIsAnOperatorSettingAndRefusesWhatCannotBound(t *testing.T) {
	fallback := 5 * time.Minute
	cases := []struct {
		raw     string
		want    time.Duration
		refused bool
	}{
		{"", fallback, false},
		{"  ", fallback, false},
		{"15m", 15 * time.Minute, false},
		{"900s", 15 * time.Minute, false},
		{"0s", 0, true},
		{"-1m", 0, true},
		{"soon", 0, true},
	}
	for _, c := range cases {
		t.Setenv(envTurnBudget, c.raw)
		got, err := configuredTurnBudget(fallback)
		if c.refused {
			if err == nil {
				t.Fatalf("%q: expected a refusal, got %s", c.raw, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("%q: got %s, %v; want %s", c.raw, got, err, c.want)
		}
	}
}
