// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdmissionCapacityIsBoundedAndReleasedWithoutKeepingInactiveKeys(t *testing.T) {
	a, err := NewAdmission(10, 4)
	if err != nil {
		t.Fatal(err)
	}
	var release []func()
	take := func(project, credential string) {
		t.Helper()
		r, ok := a.acquire(project, credential)
		if !ok {
			t.Fatal("capacity refused early")
		}
		release = append(release, r)
	}
	refuse := func(project, credential string) {
		t.Helper()
		if _, ok := a.acquire(project, credential); ok {
			t.Fatal("capacity exceeded")
		}
	}
	take("a", "a1")
	take("a", "a1")
	refuse("a", "a1")
	take("a", "a2")
	take("a", "a2")
	refuse("a", "rotated")
	take("b", "b1")
	take("b", "b1")
	take("b", "b2")
	take("b", "b2")
	take("c", "c1")
	refuse("d", "d1")
	for _, r := range release {
		r()
		r()
	}
	for i := 0; i < 10000; i++ {
		r, ok := a.acquire(fmt.Sprint(i), fmt.Sprint(i))
		if !ok {
			t.Fatal("release lost capacity")
		}
		r()
	}
	stats := a.Stats()
	if stats.ActiveRequests != 0 || stats.Refused != 3 || len(a.projects) != 0 || len(a.credentials) != 0 {
		t.Fatalf("retained state: %+v", stats)
	}
}

// A stranger's volume never spends what a credential holder needs.
//
// The allowance that bounds lookups for unknown tokens was once charged on every request, before
// the request's credential had been looked at. So a flood from somebody holding nothing emptied
// it, and callers holding credentials were refused at the door: about 130 requests a second per
// process was enough, and no token was needed. The same charge made the bucket a ceiling on the
// service itself.
func TestAStrangersVolumeNeverSpendsWhatACredentialHolderNeeds(t *testing.T) {
	a, _ := NewAdmission(4, 2)
	now := a.updated
	const holder = "0123456789abcdef"

	admitted := 0
	for {
		release, ok := a.authenticate(now, "")
		if !ok {
			break
		}
		release()
		admitted++
	}
	if admitted != authRequestBurst {
		t.Fatalf("strangers were admitted %d times in one instant; the allowance is %d", admitted, authRequestBurst)
	}
	// The allowance is spent, and it is still that instant.
	release, ok := a.authenticate(now, holder)
	if !ok {
		t.Fatal("a caller holding an issued credential was refused because strangers had spent the allowance")
	}
	release()

	// And a holder is not bounded by the allowance at all: ten times the burst, in one instant.
	for i := 0; i < 10*authRequestBurst; i++ {
		release, ok := a.authenticate(now, holder)
		if !ok {
			t.Fatalf("an issued credential was refused at request %d by a limit meant for strangers", i)
		}
		release()
	}

	// Strangers are still bounded, by rate, and time going backwards does not pay them.
	if _, ok := a.authenticate(now.Add(-time.Hour), ""); ok {
		t.Fatal("a clock reversal created allowance")
	}
	for i := 0; i < authRequestsPerSecond; i++ {
		release, ok := a.authenticate(now.Add(time.Second), "")
		if !ok {
			t.Fatalf("the allowance did not refill: refused at %d of %d", i, authRequestsPerSecond)
		}
		release()
	}
	if _, ok := a.authenticate(now.Add(time.Second), ""); ok {
		t.Fatal("the refill exceeded the stated rate")
	}
	if a.Stats().ActiveAuthentication != 0 || a.strangers != 0 || len(a.lookups) != 0 {
		t.Fatalf("a slot or a key was retained: active=%d strangers=%d keys=%d", a.Stats().ActiveAuthentication, a.strangers, len(a.lookups))
	}
}

// No one caller holds every authentication slot, where every stranger together is one caller.
//
// Lookups are not queued, so a slot held is a request refused. If a flood of unknown tokens could
// occupy both slots, a credential holder would be refused for as long as the flood lasted, with the
// allowance untouched; and if one credential could, its holder could do the same to everyone else.
func TestNoOneCallerHoldsEveryAuthenticationSlot(t *testing.T) {
	a, _ := NewAdmission(4, 2)
	now := a.updated

	stranger, ok := a.authenticate(now, "")
	if !ok {
		t.Fatal("the first stranger was refused")
	}
	if _, ok := a.authenticate(now, ""); ok {
		t.Fatal("strangers hold every slot")
	}
	holder, ok := a.authenticate(now, "holder-a")
	if !ok {
		t.Fatal("a credential holder was refused the slot strangers may not take")
	}
	// Both slots are now in use, so the bound that remains is the registry's own.
	if _, ok := a.authenticate(now, "holder-b"); ok {
		t.Fatal("more lookups were admitted than the registry has connections for")
	}
	stranger()
	stranger() // released twice, counted once
	holder()

	first, ok := a.authenticate(now, "holder-a")
	if !ok {
		t.Fatal("the first lookup for a credential was refused")
	}
	if _, ok := a.authenticate(now, "holder-a"); ok {
		t.Fatal("one credential holds every slot")
	}
	other, ok := a.authenticate(now, "holder-b")
	if !ok {
		t.Fatal("a second credential was refused the slot the first may not take")
	}
	first()
	other()
	if a.Stats().ActiveAuthentication != 0 || a.strangers != 0 || len(a.lookups) != 0 {
		t.Fatalf("a slot or a key was retained: active=%d strangers=%d keys=%d", a.Stats().ActiveAuthentication, a.strangers, len(a.lookups))
	}

	// One registry connection is one slot, and there is nothing to hold back. Stated as a limit
	// rather than left to be discovered: whoever is in the slot is in the way.
	single, _ := NewAdmission(4, 1)
	only, ok := single.authenticate(single.updated, "")
	if !ok {
		t.Fatal("with one slot, the first caller was refused")
	}
	if _, ok := single.authenticate(single.updated, "holder-a"); ok {
		t.Fatal("with one slot, two lookups were admitted")
	}
	only()
}

func TestAdmissionRefusesUndersizedPoolsAndCapsOversizedOnes(t *testing.T) {
	for _, size := range [][2]int32{{2, 1}, {4, 0}, {-1, 4}} {
		if _, err := NewAdmission(size[0], size[1]); err == nil {
			t.Fatal("unsafe pool configuration accepted")
		}
	}
	a, err := NewAdmission(10000, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if a.workLimit != 64 || a.authLimit != 2 {
		t.Fatal("oversized pools removed process ceilings")
	}
}

func TestServerRejectsAmbiguousAdmissionConfiguration(t *testing.T) {
	a, _ := NewAdmission(4, 4)
	for _, options := range [][]*Admission{{nil}, {a, a}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("invalid configuration accepted")
				}
			}()
			NewServer(nil, Stores{}, "", nil, options...)
		}()
	}
}

func TestAnonymousAuditUsesTheSameMemoryCapacityAndOnlyOneSlot(t *testing.T) {
	a, _ := NewAdmission(4, 2)
	first, ok := a.acquireAudit()
	if !ok {
		t.Fatal("first audit refused")
	}
	if _, ok := a.acquireAudit(); ok {
		t.Fatal("concurrent audit bypassed its ceiling")
	}
	second, ok := a.acquire("a", "a")
	if !ok {
		t.Fatal("audit excluded first project")
	}
	third, ok := a.acquire("b", "b")
	if !ok {
		t.Fatal("audit excluded second project")
	}
	if _, ok := a.acquire("c", "c"); ok {
		t.Fatal("audit bypassed global memory limit")
	}
	first()
	first()
	fourth, ok := a.acquire("c", "c")
	if !ok {
		t.Fatal("audit failed to release memory capacity")
	}
	if _, ok := a.acquireAudit(); ok {
		t.Fatal("audit exceeded occupied global memory limit")
	}
	second()
	third()
	fourth()
	if stats := a.Stats(); stats.ActiveRequests != 0 || stats.AuditSuppressed != 2 {
		t.Fatalf("audit accounting: %+v", stats)
	}
}

func TestAuditSuppressionNeverAttemptsAWriteWhenMemoryIsBusy(t *testing.T) {
	a, _ := NewAdmission(4, 2)
	release, _ := a.acquireAudit()
	defer release()
	server := &Server{admission: a}
	server.recordRefusedAuth(httptest.NewRequest("GET", "/v1/freshness", nil))
	if a.Stats().AuditSuppressed != 1 {
		t.Fatal("suppression was not counted")
	}
}
