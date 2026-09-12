// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

func TestAnonymousAuditIsContentFreeAndBounded(t *testing.T) {
	h := newHarness(t, "api_refusal_budget")
	ctx := context.Background()
	credentials := credential.NewStore(h.pool, string(migrate.ControlSchema))
	revoked, grant, err := credentials.Issue(ctx, "revoked-audit-test", "p1")
	if err != nil {
		t.Fatal(err)
	}
	if err := credentials.Revoke(ctx, grant.CredentialID); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	audit := pg.NewAuditStore(h.pool, h.schema)
	server := httptest.NewServer(api.NewServer(credentials, api.Stores{
		Audit:        audit,
		Projects:     pg.NewProjectStore(h.pool, h.schema),
		Observations: pg.NewObservationStore(h.pool)}, h.schema, slog.New(slog.NewTextHandler(&logs, nil))).Handler())
	defer server.Close()
	inputs := []string{"", "person@example.invalid", "private-note-more-text", "tsk_unknown-secret-content", revoked}
	var expected string
	for i := 0; i < 100; i++ {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/freshness", nil)
		if err != nil {
			t.Fatal(err)
		}
		if input := inputs[i%len(inputs)]; input != "" {
			req.Header.Set("Authorization", "Bearer "+input)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("refusal=%d", resp.StatusCode)
		}
		if i == 0 {
			expected = string(body)
		} else if string(body) != expected {
			t.Fatalf("refusals distinguish inputs: %s", body)
		}
	}
	entries, err := audit.Recent(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 16 {
		t.Fatalf("100 refusals made %d durable entries, want 16", len(entries))
	}
	for _, entry := range entries {
		if entry.Principal != "unresolved" || entry.Project != "" || entry.Magnitude != 1 {
			t.Fatalf("unsafe audit entry: %+v", entry)
		}
	}
	var dump string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT string_agg(a::text,' ') FROM {schema}.audit_entry a`)).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	for _, input := range inputs[1:] {
		for _, part := range []string{input, input[:12]} {
			if strings.Contains(dump, part) || strings.Contains(logs.String(), part) {
				t.Fatal("caller content entered permanent audit or logs")
			}
		}
	}
	if strings.Count(logs.String(), "sampling is active") != 1 {
		t.Fatalf("suppression was not signalled once: %s", logs.String())
	}
	// Sampling unresolved callers must not consume the budget for attributed operations.
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "seed the freshness watermark"}},
	}, http.StatusCreated, nil)
	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/freshness", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated call after burst: %d", resp.StatusCode)
	}
	// Read a page and look for the entry rather than demanding it be the newest row.
	//
	// The anonymous audit path is admitted one write at a time with its own deadline, so its
	// rows are not ordered against the requests that produced them: a burst row can land after the
	// attributed call that logically followed it. Asserting on `Recent(ctx, 1)` required an ordering
	// the design deliberately does not give, and it failed exactly once — under load, on a machine
	// running a second suite against the same database, which is what a CI runner is.
	//
	// The property this test is for is unchanged: the attributed operation reached the ledger with
	// the credential as its principal, and sampling unresolved callers did not consume its budget.
	entries, err = audit.Recent(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	attributed := 0
	for _, entry := range entries {
		if entry.Operation == "freshness" {
			if entry.PrincipalKind != "credential" {
				t.Fatalf("the attributed operation lost its principal: %+v", entry)
			}
			attributed++
		}
	}
	if attributed != 1 {
		t.Fatalf("want one attributed freshness entry, got %d in %d rows", attributed, len(entries))
	}
	if _, err := audit.Seal(ctx); err != nil {
		t.Fatal(err)
	}
	verification, err := audit.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Valid {
		t.Fatalf("sampling broke the seal: %+v", verification)
	}
}
