// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// Every connection string the chart builds says who is allowed to answer it. Read back from the
// rendering; that a certificate from the wrong authority is in fact refused is held where the
// connection is made, in internal/migrate, and on a cluster in docs/37.
package helm_test

import (
	"regexp"
	"strings"
	"testing"
)

// TestEveryConnectionTheChartBuildsVerifiesWhoAnswered holds the four connection strings, and the
// four workloads that use them, to the database's own certificate authority.
func TestEveryConnectionTheChartBuildsVerifiesWhoAnswered(t *testing.T) {
	out := render(t)
	dsns := regexp.MustCompile(`postgres://[^"\s]+`).FindAllString(out, -1)
	if len(dsns) < 5 {
		t.Fatalf("expected the API's two connection strings, the worker's, the management surface's and bootstrap's; found %d", len(dsns))
	}
	for _, dsn := range dsns {
		// verify-full and not verify-ca: the authority signs a certificate for every database the
		// operator runs, so a check of the authority alone would accept any of them at this
		// address. The name is what says it is this one.
		if !strings.Contains(dsn, "sslmode=verify-full") || !strings.Contains(dsn, "sslrootcert=/etc/taisce/database-ca/ca.crt") {
			t.Errorf("a connection string does not verify who answers it: %s", dsn)
		}
		if strings.Contains(dsn, "sslmode=require") || strings.Contains(dsn, "sslmode=disable") || strings.Contains(dsn, "sslmode=prefer") {
			t.Errorf("a connection string accepts whatever certificate it is shown, or none: %s", dsn)
		}
	}

	// Each workload that connects is given the authority's certificate, and only that.
	for _, c := range []struct{ kind, component string }{
		{"Deployment", "api"}, {"Deployment", "worker"}, {"Deployment", "manage"}, {"Job", "bootstrap"},
	} {
		doc := document(t, out, c.kind, c.component)
		if !strings.Contains(doc, "secretName: t-taisce-db-ca") {
			t.Errorf("the %s is told to verify against a file it is not given:\n%s", c.component, doc)
			continue
		}
		if !strings.Contains(doc, "mountPath: /etc/taisce/database-ca") || !strings.Contains(doc, "readOnly: true") {
			t.Errorf("the %s does not mount the authority where its connection string looks for it, read-only", c.component)
		}
		// The secret that holds the authority's certificate also holds the key it signs with.
		// Mounted whole, every pod of the release would carry the means to mint a certificate
		// for the database — which is the thing verification is there to stop. One key is
		// projected, by name.
		_, volume, _ := strings.Cut(doc, "secretName: t-taisce-db-ca")
		volume, _, _ = strings.Cut(volume, "securityContext:")
		if !strings.Contains(volume, "items:") || strings.Count(volume, "key: ") != 1 || !strings.Contains(volume, "- {key: ca.crt, path: ca.crt}") {
			t.Errorf("the %s mounts the authority's secret without naming the one key it may read:\n%s", c.component, doc)
		}
	}
	if strings.Contains(out, "ca.key") {
		t.Error("the authority's signing key is named somewhere in the rendering")
	}

	// An external database's connection strings are the operator's, from their secret: the chart
	// builds none, so it has nothing to verify and no authority to mount.
	external := render(t, "--set", "postgresql.mode=external", "--set", "postgresql.external.existingSecret=dbsecret")
	if strings.Contains(external, "database-ca") || strings.Contains(external, "sslrootcert") || strings.Contains(external, "postgres://") {
		t.Error("with an external database the chart built a connection string or mounted an authority it does not have")
	}
}
