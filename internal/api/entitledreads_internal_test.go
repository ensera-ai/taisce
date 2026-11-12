// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api

import (
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
)

// A window that ended before anything closed it keeps its count.
//
// Windows are closed on a timer, and a read can arrive in the gap between a window ending and the
// timer reaching it. That read opens a new window. The one it replaces had a count nobody has
// written yet, and replacing it in place would drop exactly the reads this exists to count.
func TestAWindowThatEndedBeforeItWasClosedKeepsItsCount(t *testing.T) {
	reads := NewEntitledReads(nil, nil)
	grant := credential.Grant{CredentialID: "c1", Project: "p1"}
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

	if !reads.noted(grant, start) {
		t.Fatal("the first read of a window must be written at once")
	}
	for i := 1; i <= 4; i++ {
		if reads.noted(grant, start.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("read %d inside the window was treated as a first read", i+1)
		}
	}
	// The boundary belongs to the next window: a read at the instant a window ends is not in it.
	if !reads.noted(grant, start.Add(entitledReadWindow)) {
		t.Fatal("a read at the moment the window ended must open the next one")
	}
	if len(reads.ended) != 1 || reads.ended[0].further != 4 || reads.ended[0].key != (readKey{"c1", "p1"}) {
		t.Fatalf("the replaced window's count of 4 was not kept for writing: %+v", reads.ended)
	}
	if w := reads.open[readKey{"c1", "p1"}]; w == nil || w.further != 0 {
		t.Fatalf("the new window did not start empty: %+v", w)
	}
	// A replaced window with nothing to count leaves nothing behind.
	if !reads.noted(grant, start.Add(3*entitledReadWindow)) || len(reads.ended) != 1 {
		t.Fatalf("a window holding one read left a count to write: %+v", reads.ended)
	}
	// The same credential in another project is another window.
	if !reads.noted(credential.Grant{CredentialID: "c1", Project: "p2"}, start.Add(time.Second)) {
		t.Fatal("a read in another project was folded into this one's window")
	}
}
