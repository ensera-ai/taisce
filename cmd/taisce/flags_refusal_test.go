// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"
)

// A flag a command does not define is refused before anything connects, so a mistyped option never
// becomes a command run with its default in place of what the operator meant.
func TestACommandRefusesAFlagItDoesNotDefineBeforeConnecting(t *testing.T) {
	t.Setenv("TAISCE_ADMIN_DSN", "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	for _, args := range [][]string{
		{"bootstrap", "--no-such-flag"},
		{"credential", "issue", "app", "--no-such-flag"},
		{"entity-embeddings", "status", "--no-such-flag"},
		{"rebuild", "reports", "--no-such-flag"},
	} {
		err := dispatch(context.Background(), quiet(), args)
		if err == nil || !strings.Contains(err.Error(), "no-such-flag") {
			t.Errorf("%v: %v", args, err)
		}
	}
}
