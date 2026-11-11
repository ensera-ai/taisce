// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/recall"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOversizedObservationBatchesAreRefusedBeforeStorage(t *testing.T) {
	h := newHarness(t, "api_observation_limits")
	counts := []struct{ count, size int }{{domain.MaxObservationMessages + 1, 1}, {1, domain.MaxMessageBytes + 1}, {5, domain.MaxMessageBytes}}
	for _, tc := range counts {
		messages := make([]map[string]any, tc.count)
		for i := range messages {
			messages[i] = map[string]any{"role": "user", "content": strings.Repeat("x", tc.size)}
		}
		status, body := h.raw(t, http.MethodPost, "/v1/observations", map[string]any{"messages": messages}, h.token)
		if status != http.StatusBadRequest || !strings.Contains(string(body), "invalid_turn") {
			t.Fatalf("refusal %d %s", status, body)
		}
	}
	for _, table := range []string{"observation", "turn_message", "chunk", "observation_retry", "projection_dependency", "watermark"} {
		var n int
		if err := h.pool.QueryRow(context.Background(), h.schema.SQL("SELECT count(*) FROM {schema}."+table)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("refused batch changed %s: %d", table, n)
		}
	}
}

func TestABlockedProjectCannotTakeAnotherProjectsMemoryConnections(t *testing.T) {
	h := newHarness(t, "api_admission_fairness")
	ctx := context.Background()
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	credentials := credential.NewStore(h.pool, string(migrate.ControlSchema))
	secondToken, _, err := credentials.Issue(ctx, "admission-second-project", "p2")
	if err != nil {
		t.Fatal(err)
	}
	rotatedToken, _, err := credentials.Issue(ctx, "admission-rotated-credential", "p1")
	if err != nil {
		t.Fatal(err)
	}
	config := h.pool.Config()
	config.MaxConns = 4
	memory, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer memory.Close()
	gate, _ := api.NewAdmission(4, 2)
	server := httptest.NewServer(api.NewServer(credentials, api.Stores{
		Audit:        pg.NewAuditStore(memory, h.schema),
		Projects:     pg.NewProjectStore(memory, h.schema),
		Observations: pg.NewObservationStore(memory),
		Recaller:     recall.NewWithBudget(pg.NewRecallStore(memory, h.schema), recall.DefaultBudget())}, h.schema, nil, gate).Handler())
	defer server.Close()
	request := func(ctx context.Context, token, path string, payload any) (int, string, error) {
		wire, err := json.Marshal(payload)
		if err != nil {
			return 0, "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+path, bytes.NewReader(wire))
		if err != nil {
			return 0, "", err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := server.Client().Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusTooManyRequests && resp.Header.Get("Retry-After") != "1" {
			return 0, "", fmt.Errorf("missing Retry-After")
		}
		return resp.StatusCode, string(body), err
	}
	body := map[string]any{"messages": []map[string]any{{"role": "user", "content": "bounded observation"}}}
	if status, response, err := request(ctx, h.token, "/v1/observations", body); err != nil || status != http.StatusCreated {
		t.Fatalf("seed %d %s %v", status, response, err)
	}
	held, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	if _, err := held.Exec(ctx, h.schema.SQL(`SELECT 1 FROM {schema}.watermark WHERE scope='p1' FOR UPDATE`)); err != nil {
		t.Fatal(err)
	}
	blockedCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, _, err := request(blockedCtx, h.token, "/v1/observations", body); finished <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for gate.Stats().ActiveRequests != 1 {
		if time.Now().After(deadline) {
			t.Fatal("first request never acquired capacity")
		}
		time.Sleep(time.Millisecond)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, response, err := request(ctx, rotatedToken, "/v1/observations", body)
			if err != nil || status != http.StatusTooManyRequests {
				failures <- fmt.Errorf("noisy project %d %s %v", status, response, err)
			}
		}()
	}
	// Authentication has its own bound. A short retry may be needed during the simultaneous burst;
	// once admitted, the second project's database work must finish while p1 remains blocked.
	for _, path := range []string{"/v1/observations", "/v1/recalls"} {
		payload := body
		if path == "/v1/recalls" {
			payload = map[string]any{"question": "bounded"}
		}
		completed := false
		for attempt := 0; attempt < 5; attempt++ {
			bounded, stop := context.WithTimeout(ctx, 3*time.Second)
			status, response, err := request(bounded, secondToken, path, payload)
			stop()
			if err != nil {
				t.Fatalf("second project stalled: %v", err)
			}
			if status == http.StatusTooManyRequests {
				time.Sleep(time.Second)
				continue
			}
			if status != http.StatusOK && status != http.StatusCreated {
				t.Fatalf("second project %d %s", status, response)
			}
			completed = true
			break
		}
		if !completed {
			t.Fatal("second project did not make progress")
		}
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	var stored int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT count(*) FROM {schema}.observation WHERE scope='p1'`)).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("refused requests wrote observations: %d %v", stored, err)
	}
	cancel()
	<-finished
	deadline = time.Now().Add(5 * time.Second)
	for gate.Stats().ActiveRequests != 0 {
		if time.Now().After(deadline) {
			t.Fatal("cancelled request retained its slot")
		}
		time.Sleep(time.Millisecond)
	}
	if err := held.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if status, response, err := request(ctx, h.token, "/v1/observations", body); err != nil || status != http.StatusCreated {
		t.Fatalf("slot not reusable %d %s %v", status, response, err)
	}
	if gate.Stats().Refused < 12 {
		t.Fatal("refusals are not observable")
	}
}

func TestAnonymousBurstsCannotQueueUnboundedRegistryQueries(t *testing.T) {
	h := newHarness(t, "api_auth_capacity")
	ctx := context.Background()
	if _, err := h.pool.Exec(ctx, `CREATE TABLE api_auth_capacity.credential (LIKE control.credential INCLUDING ALL)`); err != nil {
		t.Fatal(err)
	}
	registryConfig := h.pool.Config()
	registryConfig.MaxConns = 2
	registryConfig.ConnConfig.RuntimeParams["application_name"] = "taisce_test_auth_capacity"
	registry, err := pgxpool.NewWithConfig(ctx, registryConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	gate, _ := api.NewAdmission(4, 2)
	server := httptest.NewServer(api.NewServer(credential.NewStore(registry, h.schema.String()), api.Stores{
		Audit: pg.NewAuditStore(h.pool, h.schema)}, h.schema, nil, gate).Handler())
	defer server.Close()
	held, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	request := func(ctx context.Context) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/freshness", nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+credential.Prefix+"unknown")
		resp, err := server.Client().Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests && resp.Header.Get("Retry-After") != "1" {
			return 0, fmt.Errorf("missing retry interval")
		}
		return resp.StatusCode, nil
	}
	// The set of issued credentials is read once before the registry is made to block, so what
	// follows measures lookups and not that read.
	if status, err := request(ctx); err != nil || status != http.StatusUnauthorized {
		t.Fatalf("an unknown token before the registry blocked: %d %v", status, err)
	}
	if _, err := held.Exec(ctx, `LOCK TABLE api_auth_capacity.credential IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan struct{}, 1)
	go func() { request(blocked); finished <- struct{}{} }()
	// One, not two. Every stranger together is one caller, and one caller may hold all the slots
	// but one, so a stuck lookup for an unknown token leaves a slot for somebody who holds a
	// credential. Two here would mean a flood could occupy the registry entirely.
	deadline := time.Now().Add(time.Second)
	for gate.Stats().ActiveAuthentication != 1 {
		if time.Now().After(deadline) {
			t.Fatal("authentication never reached its bound")
		}
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 20; i++ {
		status, err := request(ctx)
		if err != nil || status != http.StatusTooManyRequests {
			t.Fatalf("burst admitted %d %v", status, err)
		}
	}
	var waiting int
	deadline = time.Now().Add(time.Second)
	for {
		if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE application_name='taisce_test_auth_capacity' AND wait_event_type='Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("registry waiters=%d", waiting)
		}
		time.Sleep(time.Millisecond)
	}
	resp, err := server.Client().Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("admission hid liveness")
	}
	cancel()
	<-finished
	deadline = time.Now().Add(5 * time.Second)
	for gate.Stats().ActiveAuthentication != 0 {
		if time.Now().After(deadline) {
			t.Fatal("cancelled authentication retained capacity")
		}
		time.Sleep(time.Millisecond)
	}
	// Without caller cancellation, the authentication deadline still cancels a blocked query.
	if status, err := request(ctx); err != nil || status != http.StatusInternalServerError {
		t.Fatalf("blocked registry deadline: %d %v", status, err)
	}
	if gate.Stats().ActiveAuthentication != 0 {
		t.Fatal("timed-out authentication retained capacity")
	}
	if err := held.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if status, err := request(ctx); err != nil || status != http.StatusUnauthorized {
		t.Fatalf("registry capacity did not recover: %d %v", status, err)
	}
}

// A flood from somebody holding nothing does not refuse a caller who holds a credential.
//
// Through the door itself, against a real registry. Three kinds of stranger: no header at all,
// something that cannot be a token, and a well-formed token this instance never issued. The first
// two have nothing to look up and cost nothing. The third costs a lookup, is bounded by rate, and
// is refused once the allowance is spent — and through all of it the holder is answered.
//
// Before this held, every request spent from one bucket before its credential was read: the
// headerless flood below emptied it in a fraction of a second and the holder's next request was
// answered 429.
func TestAStrangersFloodDoesNotRefuseACallerWhoHoldsACredential(t *testing.T) {
	h := newHarness(t, "api_stranger_flood")

	holder := func(when string) {
		t.Helper()
		if status, body := h.raw(t, http.MethodGet, "/v1/freshness", nil, h.token); status != http.StatusOK {
			t.Fatalf("%s, the caller holding a credential was answered %d: %s", when, status, body)
		}
	}
	holder("before any flood")

	for i := 0; i < 1500; i++ {
		if status, _ := h.raw(t, http.MethodGet, "/v1/freshness", nil, ""); status != http.StatusUnauthorized {
			t.Fatalf("a request with no credential was answered %d at request %d; it has nothing to look up and nothing to be limited for", status, i)
		}
		if i%100 == 0 {
			holder("during a flood of requests with no credential")
		}
	}
	holder("after 1500 requests with no credential")

	for i := 0; i < 1500; i++ {
		if status, _ := h.raw(t, http.MethodGet, "/v1/freshness", nil, "not-a-token"); status != http.StatusUnauthorized {
			t.Fatalf("something that cannot be a token was answered %d at request %d", status, i)
		}
	}
	holder("after 1500 requests carrying something that cannot be a token")

	// Well-formed and never issued: each is a registry lookup that finds nothing. These are what the
	// allowance exists to bound, so the flood must eventually be refused — and the holder must not.
	limited := 0
	for i := 0; i < 1200; i++ {
		stranger := credential.Prefix + fmt.Sprintf("%043d", i)
		switch status, body := h.raw(t, http.MethodGet, "/v1/freshness", nil, stranger); status {
		case http.StatusUnauthorized:
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Fatalf("an unknown token was answered %d: %s", status, body)
		}
		if i%50 == 0 {
			holder("during a flood of well-formed unknown tokens")
		}
	}
	holder("after a flood of well-formed unknown tokens")
	t.Logf("of 1200 unknown tokens, %d were refused by the allowance; the holder was never refused", limited)
}

// A caller holding a credential is not bounded by the allowance that bounds strangers.
//
// The bucket holds 256 and refills at 128 a second. Charged on every request, it made that the
// service's own ceiling: one caller asking for freshness in a loop was refused after a few hundred
// requests, on a process doing nothing else.
func TestACredentialHolderIsNotBoundedByTheStrangersAllowance(t *testing.T) {
	h := newHarness(t, "api_holder_ceiling")
	started := time.Now()
	const requests = 2000
	for i := 0; i < requests; i++ {
		if status, body := h.raw(t, http.MethodGet, "/v1/freshness", nil, h.token); status != http.StatusOK {
			t.Fatalf("request %d of %d from one credential, %.1fs in, was answered %d: %s",
				i, requests, time.Since(started).Seconds(), status, body)
		}
	}
	// Reported, not asserted: how fast a development machine answered is not evidence of anything,
	// and it is what says whether this run could have reached the old ceiling at all.
	elapsed := time.Since(started).Seconds()
	t.Logf("%d sequential requests in %.1fs; the old allowance would have covered %.0f of them",
		requests, elapsed, 256+128*elapsed)
}
