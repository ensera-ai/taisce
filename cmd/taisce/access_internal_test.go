// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/ensera-ai/taisce/internal/credential"
)

// A credential issued from the command line without naming an authority is agent (D26); --read-only
// still means read_only for the scripts that use it; and a flag pair that disagrees, or an authority
// outside the three, is refused rather than resolved one way or the other.
func TestTheCommandLineIssuesAgentUnlessToldOtherwise(t *testing.T) {
	for _, c := range []struct {
		access   string
		readOnly bool
		want     credential.Access
	}{
		{"", false, credential.Agent},
		{"", true, credential.ReadOnly},
		{"read_only", true, credential.ReadOnly},
		{"agent", false, credential.Agent},
		{"read_write", false, credential.ReadWrite},
	} {
		got, err := issuedAccess(c.access, c.readOnly)
		if err != nil || got != c.want {
			t.Errorf("--access %q --read-only=%v: got %q %v, want %q", c.access, c.readOnly, got, err, c.want)
		}
	}
	for _, c := range []struct {
		access   string
		readOnly bool
	}{{"read_write", true}, {"agent", true}, {"admin", false}, {"READ_WRITE", false}} {
		if got, err := issuedAccess(c.access, c.readOnly); err == nil {
			t.Errorf("--access %q --read-only=%v was accepted as %q", c.access, c.readOnly, got)
		}
	}
}
