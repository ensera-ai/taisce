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

func TestAuthenticationAdmissionBoundsConcurrencyAndRate(t *testing.T) {
	a, _ := NewAdmission(4, 2)
	now := a.updated
	first, ok := a.authenticate(now)
	if !ok {
		t.Fatal("first refused")
	}
	second, ok := a.authenticate(now)
	if !ok {
		t.Fatal("second refused")
	}
	if _, ok := a.authenticate(now); ok {
		t.Fatal("third concurrent authentication admitted")
	}
	first()
	first()
	second()
	for i := 2; i < authRequestBurst; i++ {
		r, ok := a.authenticate(now)
		if !ok {
			t.Fatalf("burst refused at %d", i)
		}
		r()
	}
	if _, ok := a.authenticate(now); ok {
		t.Fatal("unbounded authentication burst")
	}
	if _, ok := a.authenticate(now.Add(-time.Hour)); ok {
		t.Fatal("clock reversal created tokens")
	}
	for i := 0; i < authRequestsPerSecond; i++ {
		r, ok := a.authenticate(now.Add(time.Second))
		if !ok {
			t.Fatal("refill missing")
		}
		r()
	}
	if _, ok := a.authenticate(now.Add(time.Second)); ok {
		t.Fatal("refill exceeded rate")
	}
	if a.Stats().ActiveAuthentication != 0 {
		t.Fatal("authentication slot leaked")
	}
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
