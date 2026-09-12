// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A runtime connection reaches the server with keepalives that end a vanished client's session,
// and its locks, in about a minute, and with a check that cancels a query whose client has gone
//. A setting the operator puts in the DSN wins over the default.
func TestARuntimeConnectionCarriesKeepalivesAndAClientCheck(t *testing.T) {
	ctx := context.Background()
	owner := testPool(t)
	schema, _ := NewSchema("runtimeconn_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	establish(t, owner, schema.String())
	t.Cleanup(func() { owner.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE") })
	role := "runtime_conn_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := ensureRole(ctx, owner, role, "isolated-runtime-test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		owner.Exec(ctx, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
		owner.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize())
	})
	if _, err := owner.Exec(ctx, "GRANT "+pgx.Identifier{DataRole}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	dsn.User = url.UserPassword(role, "isolated-runtime-test")
	settings := func(raw string) map[string]string {
		t.Helper()
		pool, err := NewRuntimePool(ctx, raw, schema, MemoryPlane)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		got := map[string]string{}
		for name := range runtimeConnectionDefaults {
			var value string
			if err := pool.QueryRow(ctx, "SELECT current_setting($1)", name).Scan(&value); err != nil {
				t.Fatal(err)
			}
			got[name] = value
		}
		return got
	}
	got := settings(dsn.String())
	for name, want := range map[string]string{"tcp_keepalives_idle": "30", "tcp_keepalives_interval": "10", "tcp_keepalives_count": "3", "client_connection_check_interval": "10s"} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
	query := dsn.Query()
	query.Set("tcp_keepalives_idle", "45")
	dsn.RawQuery = query.Encode()
	if got := settings(dsn.String()); got["tcp_keepalives_idle"] != "45" || got["tcp_keepalives_count"] != "3" {
		t.Fatalf("the DSN's own setting must win and the rest still apply, got %v", got)
	}
}
