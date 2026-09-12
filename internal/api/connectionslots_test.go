// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── A write that cannot get a connection ──────────────────────────────────────────────────────
//
// A write that cannot get a connection used to be an internal error, which tells a caller nothing
// they can act on. It is the one database failure that is transient, entirely the deployment's own
// doing, and safe to retry — an observation carries an idempotency key — so it is answered
// retryably.
//
// The exhaustion is real rather than simulated: a login with CONNECTION LIMIT 0 makes the server
// refuse the session exactly as a full server does, with the same SQLSTATE.
func TestAWriteThatCannotGetAConnectionIsAnsweredRetryablyRatherThanAsAnInternalError(t *testing.T) {
	h := newHarness(t, "api_no_slots")
	ctx := context.Background()

	const login = "taisce_no_slots_test"
	if _, err := h.pool.Exec(ctx, `DROP ROLE IF EXISTS `+login); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx,
		`CREATE ROLE `+login+` LOGIN CONNECTION LIMIT 0 PASSWORD 'no-slots'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.pool.Exec(context.Background(), `DROP ROLE IF EXISTS `+login) })

	// Host, port and database come from the same DSN the harness used, so this runs wherever the
	// suite runs — on the host, or inside a container where the substrate is not on localhost.
	target, err := url.Parse(os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	target.User = url.UserPassword(login, "no-slots")
	exhausted, err := pgxpool.New(ctx, target.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(exhausted.Close)

	// Only the observation store is on the exhausted pool. Everything the request passes through
	// before reaching it — authentication, the project check, admission — uses the working one, so
	// what this measures is the write path meeting the ceiling and nothing else.
	server := httptest.NewServer(api.NewServer(
		credential.NewStore(h.pool, string(migrate.ControlSchema)), api.Stores{
			Audit:        pg.NewAuditStore(h.pool, h.schema),
			Projects:     pg.NewProjectStore(h.pool, h.schema),
			Observations: pg.NewObservationStore(exhausted),
		}, h.schema, nil).Handler())
	t.Cleanup(server.Close)

	body := `{"data_subject_id":"subject-1","messages":[{"role":"user","content":"I live in Dublin."}]}`
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/observations", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+h.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	answered, _ := io.ReadAll(response.Body)
	response.Body.Close()

	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("returned %d, want 503: %s", response.StatusCode, answered)
	}
	// The header is what makes it actionable without a caller guessing.
	if response.Header.Get("Retry-After") != "1" {
		t.Fatalf("no retry hint: %q", response.Header.Get("Retry-After"))
	}
	// Its own code, not the rate limit's. A rate limit is this deployment deciding it is busy and is
	// bounded by its own settings; this is the substrate having nothing left, and an operator
	// reading a log has to be able to tell them apart.
	if !strings.Contains(string(answered), "no_database_capacity") {
		t.Fatalf("wrong code: %s", answered)
	}
	// And it says nothing about how the deployment is built: no role, no host, no SQLSTATE.
	for _, leaked := range []string{login, "53300", "localhost", "FATAL", "SUPERUSER"} {
		if strings.Contains(string(answered), leaked) {
			t.Fatalf("the refusal disclosed %q: %s", leaked, answered)
		}
	}
}
