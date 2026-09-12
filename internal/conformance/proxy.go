// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package conformance

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

// Recorded is one request the adapter made through the proxy.
type Recorded struct {
	Method string
	Path   string
	Body   []byte
}

// proxy stands between the driver and the deployment. It forwards everything, records what the
// adapter sent, and refuses the operations a case's faults name with 503, which is what a
// deployment behind a load balancer answers when it is down: the same status an adapter has to
// treat as "memory unavailable" in production.
type proxy struct {
	server  *httptest.Server
	mu      sync.Mutex
	faults  Faults
	context *ContextAnswer
	log     []Recorded
}

func newProxy(target string) (*proxy, error) {
	upstream, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	p := &proxy{}
	reverse := httputil.NewSingleHostReverseProxy(upstream)
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body = io.NopCloser(bytes.NewReader(body))
		p.mu.Lock()
		p.log = append(p.log, Recorded{Method: r.Method, Path: r.URL.Path, Body: body})
		faults := p.faults
		answer := p.context
		p.mu.Unlock()
		if (faults.Recall != "" && strings.HasSuffix(r.URL.Path, "/recalls")) ||
			(faults.Observe != "" && strings.HasSuffix(r.URL.Path, "/observations")) ||
			(faults.Context != "" && strings.HasSuffix(r.URL.Path, "/contexts")) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"code":"unavailable","message":"refused by the conformance proxy"}}`)
			return
		}
		if answer != nil && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/contexts") {
			// The case's stated context, in the deployment's shape; see Case.Context.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(answer)
			return
		}
		reverse.ServeHTTP(w, r)
	}))
	return p, nil
}

func (p *proxy) URL() string { return p.server.URL }

func (p *proxy) Close() { p.server.Close() }

// begin arms the faults and the stated context for one case and clears the log.
func (p *proxy) begin(faults Faults, answer *ContextAnswer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.faults = faults
	p.context = answer
	p.log = nil
}

func (p *proxy) recorded() []Recorded {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Recorded, len(p.log))
	copy(out, p.log)
	return out
}
