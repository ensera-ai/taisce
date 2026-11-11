// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Admission bounds this process's HTTP work. It never queues requests, stores raw credentials or
// retains inactive project keys. Multiple API processes have independent gates; deployment-wide
// backlog capacity and upstream denial-of-service protection remain separate controls.
type Admission struct {
	mu                                                  sync.Mutex
	workLimit, projectLimit, credentialLimit, authLimit int
	// callerLimit is how many of the authentication slots any one caller may hold at once, where
	// every stranger together counts as one caller.
	callerLimit           int
	work, auth, audit     int
	strangers             int
	projects, credentials map[string]int
	// lookups counts registry lookups in flight per issued credential, keyed by its digest and
	// dropped at zero, so no key outlives the request that brought it.
	lookups                            map[string]int
	updated                            time.Time
	tokens                             float64
	refused, accepted, auditSuppressed uint64
}

// authRequestsPerSecond and authRequestBurst bound the registry lookups made for tokens this
// instance did not issue. They are not a bound on requests: a token the instance issued never
// spends from them.
const authRequestsPerSecond = 128
const authRequestBurst = 256

// NewAdmission reserves one memory connection outside HTTP work and limits each project to at
// most half of the remaining slots. Credential rotation cannot evade the project bound. Pool
// capacities below three cannot reserve room for another project and background work.
func NewAdmission(memoryConnections, registryConnections int32) (*Admission, error) {
	if memoryConnections < 3 || registryConnections < 1 {
		return nil, fmt.Errorf("admission requires at least 3 memory and 1 registry connections")
	}
	work := min(64, int(memoryConnections)-1)
	project := max(1, work/2)
	auth := min(2, int(registryConnections))
	return &Admission{workLimit: work, projectLimit: project, credentialLimit: max(1, project/2),
		authLimit: auth, callerLimit: max(1, auth-1),
		projects: map[string]int{}, credentials: map[string]int{}, lookups: map[string]int{},
		tokens: authRequestBurst, updated: time.Now()}, nil
}

// authenticate takes a registry lookup slot for one request.
//
// The key names a credential this instance issued (credential.Store.Known); empty means the token
// is not one it knows. The two are bounded differently, and the difference is the whole of what
// this function is for.
//
// # A stranger pays from a shared allowance
//
// A token the instance did not issue costs a lookup that will find nothing. Those are bounded by
// rate, so that guessing is slow and the registry's connections are not a resource a stranger can
// occupy.
//
// # A credential holder does not
//
// A token the instance issued spends nothing from that allowance. This is what stops a stranger's
// volume becoming a customer's refusal: the bucket was once charged on every request before
// anything was known about it, so about 130 requests a second with no credential at all left
// nothing for callers who held one, and they were turned away at the door. It is also what stops
// the bucket being a ceiling on the service — 128 requests a second per process, whatever it ran
// on. A holder is bounded by the work slots in acquire, which are sized from the memory pool and
// shared fairly between projects.
//
// # No one caller holds every slot
//
// Lookups are not queued, so a slot held is a request refused. One caller is therefore allowed at
// most all the slots but one — every stranger together counting as a single caller. A flood of
// unknown tokens cannot occupy the last slot, and neither can one credential sent as fast as its
// holder can send it.
//
// What this does not cover. With one registry connection there is one slot and nothing to hold
// back, so whoever is in it is in the way; the chart gives the registry two. Several issued
// credentials acting together can still hold every slot between them. And a slot is refused rather
// than waited for, so two holders arriving in the same millisecond can refuse a third without
// anybody misbehaving — a limit of not queueing, unchanged here.
func (a *Admission) authenticate(now time.Time, key string) (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if now.After(a.updated) {
		a.tokens = min(float64(authRequestBurst), a.tokens+now.Sub(a.updated).Seconds()*authRequestsPerSecond)
		a.updated = now
	}
	if a.auth >= a.authLimit {
		a.refused++
		return nil, false
	}
	if key == "" {
		if a.tokens < 1 || a.strangers >= a.callerLimit {
			a.refused++
			return nil, false
		}
		a.tokens--
		a.strangers++
	} else {
		if a.lookups[key] >= a.callerLimit {
			a.refused++
			return nil, false
		}
		a.lookups[key]++
	}
	a.auth++
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.auth--
			if key == "" {
				a.strangers--
				return
			}
			a.lookups[key]--
			if a.lookups[key] == 0 {
				delete(a.lookups, key)
			}
		})
	}, true
}

func (a *Admission) acquire(project, credential string) (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.work >= a.workLimit || a.projects[project] >= a.projectLimit || a.credentials[credential] >= a.credentialLimit {
		a.refused++
		return nil, false
	}
	a.work++
	a.projects[project]++
	a.credentials[credential]++
	a.accepted++
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.work--
			a.projects[project]--
			a.credentials[credential]--
			if a.projects[project] == 0 {
				delete(a.projects, project)
			}
			if a.credentials[credential] == 0 {
				delete(a.credentials, credential)
			}
		})
	}, true
}

// Anonymous audit shares the memory-work ceiling, with at most one audit write at once. Failing
// to acquire is best-effort audit suppression, not a reason to queue or block authentication.
func (a *Admission) acquireAudit() (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.work >= a.workLimit || a.audit > 0 {
		a.auditSuppressed++
		return nil, false
	}
	a.work++
	a.audit++
	var once sync.Once
	return func() { once.Do(func() { a.mu.Lock(); a.work--; a.audit--; a.mu.Unlock() }) }, true
}

// AdmissionStats contains fixed aggregate fields only, suitable for operator logs without
// introducing a metric series for each project, credential or attacker-supplied header.
type AdmissionStats struct {
	Accepted, Refused                    uint64
	ActiveRequests, ActiveAuthentication int
	AuditSuppressed                      uint64
}

func (a *Admission) Stats() AdmissionStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AdmissionStats{Accepted: a.accepted, Refused: a.refused, ActiveRequests: a.work, ActiveAuthentication: a.auth, AuditSuppressed: a.auditSuppressed}
}

func writeAdmissionRefusal(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	writeError(w, http.StatusTooManyRequests, codeRateLimited, "request capacity is busy; retry with backoff and the same observation idempotency key")
}
