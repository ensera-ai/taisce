// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
)

// TestTheConsolesListenerSendsItsBareRootToTheConsoleAndNothingElse holds the one address an
// operator is given to the page they meant to reach.
//
// What it must not become is a redirect from every path nobody serves: that would answer a prober's
// wordlist with where the console is. And it exists only with the portal — a management listener
// whose portal is off has no console to send anybody to.
func TestTheConsolesListenerSendsItsBareRootToTheConsoleAndNothingElse(t *testing.T) {
	m := newManaged(t, "api_portal_root")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ask := func(server *httptest.Server, method, path string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("Location")
	}

	mux := http.NewServeMux()
	mux.Handle("/", m.manageServer.Handler())
	api.NewPortal(m.manageServer).Mount(mux)
	withPortal := httptest.NewServer(mux)
	defer withPortal.Close()

	if status, location := ask(withPortal, http.MethodGet, "/"); status != http.StatusFound || location != "/portal/" {
		t.Fatalf("the bare root answered %d → %q; an operator holding only the hostname must land on the console", status, location)
	}
	// A relative location: the listener does not know the name or the scheme it was reached by,
	// and a guess would send a browser behind a TLS-terminating proxy to plain HTTP.
	for _, path := range []string{"/admin", "/portal.bak", "/index.html", "/manage", "/.env"} {
		if status, location := ask(withPortal, http.MethodGet, path); status != http.StatusNotFound || location != "" {
			t.Errorf("%s answered %d → %q; only the bare root is sent anywhere", path, status, location)
		}
	}
	if status, location := ask(withPortal, http.MethodPost, "/"); status < 400 || location != "" {
		t.Errorf("POST / answered %d → %q; the root is somewhere to arrive, not something to act on", status, location)
	}

	// The portal off: the same listener, and no console to send anybody to.
	withoutPortal := httptest.NewServer(m.manageServer.Handler())
	defer withoutPortal.Close()
	if status, location := ask(withoutPortal, http.MethodGet, "/"); status != http.StatusNotFound || location != "" {
		t.Errorf("with the portal off the root answered %d → %q", status, location)
	}
}
