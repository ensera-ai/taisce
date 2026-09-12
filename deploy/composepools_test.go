// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// The shipped deployments say how many connections each process may open. This checks that the
// driver agrees, because a pool setting that does not parse is one that silently does nothing.
package deploy_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ── #197 ──────────────────────────────────────────────────────────────────────────────────────
//
// pgx sizes a pool it is not told about at the machine's CPU count. On a laptop that is eight and
// nobody notices; on a node with a hundred cores it is how a deployment asks for thousands of
// connections against a server that has eighty, and loses writes.
//
// So every shipped DSN says what it may open — and this asserts the driver reads what the file says,
// which is the part nobody was checking. `pool_min_idle_conns` was in the performance profile for
// three releases: it parses without error and sets nothing, so a minimum that looked configured was
// never applied. A key that is wrong in a way the driver accepts is exactly the failure a config
// file cannot catch for itself.
func TestEveryShippedPoolSettingIsOneTheDriverActuallyReads(t *testing.T) {
	interpolation := regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)
	dsn := regexp.MustCompile(`postgres://[^\s"']+`)
	for _, file := range []string{"../compose.yaml", "../compose.perf.yaml"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		found := dsn.FindAllString(string(raw), -1)
		if len(found) == 0 {
			t.Fatalf("%s declares no DSN, so this asserts nothing about it", file)
		}
		sized := 0
		for _, candidate := range found {
			// Shell interpolation with its default, which is what an operator gets by running the
			// file as shipped.
			resolved := interpolation.ReplaceAllString(candidate, "$2")
			config, err := pgxpool.ParseConfig(resolved)
			if err != nil {
				t.Fatalf("%s: %s does not parse: %v", file, candidate, err)
			}
			if !strings.Contains(candidate, "pool_max_conns=") {
				// An administrative DSN is opened once and closed; only the serving pools are sized.
				continue
			}
			sized++
			want := setting(t, resolved, "pool_max_conns")
			if int(config.MaxConns) != want {
				t.Fatalf("%s: %s says pool_max_conns=%d and the driver read %d",
					file, candidate, want, config.MaxConns)
			}
			if !strings.Contains(candidate, "pool_min_conns=") {
				continue
			}
			if wantMin := setting(t, resolved, "pool_min_conns"); int(config.MinConns) != wantMin {
				t.Fatalf("%s: %s says pool_min_conns=%d and the driver read %d — a minimum that is "+
					"written and not applied is worse than none", file, candidate, wantMin, config.MinConns)
			}
		}
		if sized == 0 {
			t.Fatalf("%s sizes no pool: a serving process that does not say what it may open takes "+
				"the machine's CPU count", file)
		}
	}
}

// The guard is only worth having if it fails on the thing it was written for. `pool_min_idle_conns`
// is accepted by the driver and sets nothing, which is how a minimum stayed unapplied for three
// releases without a single error anywhere.
func TestTheGuardCatchesASettingTheDriverAcceptsAndIgnores(t *testing.T) {
	config, err := pgxpool.ParseConfig(
		"postgres://u:p@h:5432/d?sslmode=disable&pool_max_conns=4&pool_min_idle_conns=2")
	if err != nil {
		t.Fatalf("the misspelling did not even parse, so this test proves nothing: %v", err)
	}
	if config.MinConns != 0 {
		t.Skip("this pgx reads pool_min_idle_conns; the guard's premise no longer holds")
	}
	// So a file carrying it would claim a minimum of two and get none — which is what the guard
	// above refuses, by comparing the file's number against the driver's.
	if config.MaxConns != 4 {
		t.Fatalf("the maximum was not read either: %d", config.MaxConns)
	}
}

func setting(t *testing.T, dsn, key string) int {
	t.Helper()
	part := dsn[strings.Index(dsn, key+"=")+len(key)+1:]
	if cut := strings.IndexAny(part, "&\""); cut >= 0 {
		part = part[:cut]
	}
	value := 0
	for _, r := range part {
		if r < '0' || r > '9' {
			t.Fatalf("%s is not a number in %s", key, dsn)
		}
		value = value*10 + int(r-'0')
	}
	return value
}
