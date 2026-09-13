// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"
)

// TestEveryCommandNameReachesItsOwnCommand holds dispatch to routing each name to the command that
// owns it.
//
// The switch is a table nobody reads twice, and a transposed case sends `health` to the probe or
// `report-embeddings` to the entity command — each of which parses flags close enough to the other's
// to fail confusingly rather than loudly. So every name is given an argument its own command refuses
// before connecting to anything, and the refusal must be that command's own words.
func TestEveryCommandNameReachesItsOwnCommand(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		args []string
		says string
	}{
		{[]string{"rebuild"}, "taisce rebuild facts"},
		{[]string{"recover"}, "taisce recover facts"},
		{[]string{"health", "extra"}, "usage: taisce health"},
		{[]string{"conformance", "--no-such-flag"}, "conformance:"},
		{[]string{"embeddings"}, "taisce embeddings <"},
		{[]string{"entity-embeddings"}, "taisce entity-embeddings <"},
		{[]string{"report-embeddings"}, "taisce report-embeddings <"},
		{[]string{"probe", "extra"}, "probe accepts only"},
	} {
		err := dispatch(ctx, quiet(), c.args)
		if err == nil {
			t.Errorf("%v was accepted; it must be refused by its own command", c.args)
			continue
		}
		if strings.Contains(err.Error(), "unknown command") || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%v reached the wrong place: %v (wanted %q)", c.args, err, c.says)
		}
	}
}
