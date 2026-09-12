// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Readiness permits one dependency check at a time and caches either result for one second.
// Probe clients cannot amplify pool acquisition or cancel a shared check to poison its result.
type Readiness struct {
	mu        sync.Mutex
	check     func(context.Context) error
	checking  bool
	checkedAt time.Time
	ready     bool
}

func NewReadiness(check func(context.Context) error) *Readiness { return &Readiness{check: check} }

func (h *Readiness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	ready := h.ready
	if time.Since(h.checkedAt) < time.Second {
		h.mu.Unlock()
	} else if h.checking {
		ready = false
		h.mu.Unlock()
	} else {
		h.checking = true
		h.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Second)
		ready = h.check != nil && h.check(ctx) == nil
		cancel()
		h.mu.Lock()
		h.ready = ready
		h.checkedAt = time.Now()
		h.checking = false
		h.mu.Unlock()
	}
	w.Header().Set("Cache-Control", "no-store")
	if !ready {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// HealthHandler exposes only liveness/readiness for a worker's loopback listener.
func HealthHandler(ready http.Handler) http.Handler {
	mux := http.NewServeMux()
	registerHealth(mux, ready)
	return mux
}

func registerHealth(mux *http.ServeMux, ready http.Handler) {
	if ready == nil {
		ready = NewReadiness(nil)
	}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("GET /ready", ready)
}
