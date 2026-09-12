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
	work, auth, audit                                   int
	projects, credentials                               map[string]int
	updated                                             time.Time
	tokens                                              float64
	refused, accepted, auditSuppressed                  uint64
}

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
	return &Admission{workLimit: work, projectLimit: project, credentialLimit: max(1, project/2),
		authLimit: min(2, int(registryConnections)), projects: map[string]int{}, credentials: map[string]int{},
		tokens: authRequestBurst, updated: time.Now()}, nil
}

func (a *Admission) authenticate(now time.Time) (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if now.After(a.updated) {
		a.tokens = min(float64(authRequestBurst), a.tokens+now.Sub(a.updated).Seconds()*authRequestsPerSecond)
		a.updated = now
	}
	if a.auth >= a.authLimit || a.tokens < 1 {
		a.refused++
		return nil, false
	}
	a.tokens--
	a.auth++
	var once sync.Once
	return func() { once.Do(func() { a.mu.Lock(); a.auth--; a.mu.Unlock() }) }, true
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
