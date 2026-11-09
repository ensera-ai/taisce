// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/api"
)

// Every memory operation that reads a body refuses one that is not a whole JSON object with
// 400 invalid_body, and does nothing else: a body the service cannot read is never half-applied, and
// the answer is the same on every route so a client needs one rule for it, not one per path.
func TestEveryOperationRefusesABodyItCannotReadAndDoesNothingElse(t *testing.T) {
	h := newHarness(t, "api_malformed_bodies")
	count := func() int {
		var n int
		if err := h.pool.QueryRow(context.Background(), h.schema.SQL(`SELECT count(*) FROM {schema}.observation`)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	for _, op := range api.Operations() {
		if op.Method != http.MethodPost {
			continue
		}
		status, raw := h.rawBody(t, op.Method, op.Path, `{"id":`, h.token)
		if status != http.StatusBadRequest || !bytes.Contains(raw, []byte("invalid_body")) {
			t.Errorf("%s with a truncated body answered %d %s", op.Name, status, raw)
		}
	}
	if after := count(); after != before {
		t.Fatalf("a refused body changed memory: %d observations, then %d", before, after)
	}
}

// A portal action whose form cannot be parsed is refused with a page that says so, and nothing is
// done: the guard that proves the form came from the portal cannot be read from it either. And signing out forgets the session, so the cookie a browser still holds
// opens nothing afterwards.
func TestThePortalRefusesAnUnreadableFormAndSigningOutForgetsTheSession(t *testing.T) {
	a := signedInPortal(t, "portal_unreadable")
	resp, err := a.browser.Post(a.server.URL+"/portal/actions/suspend", "application/x-www-form-urlencoded", strings.NewReader("project=p1&guard=%zz"))
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "the form could not be read") || a.suspended(t) {
		t.Fatalf("an unreadable form answered %d and suspended=%v", resp.StatusCode, a.suspended(t))
	}
	resp, err = a.browser.PostForm(a.server.URL+"/portal/logout", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasSuffix(resp.Header.Get("Location"), "/portal/login") {
		t.Fatalf("signing out answered %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = a.browser.Get(a.server.URL + "/portal/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the portal opened after signing out: %d", resp.StatusCode)
	}
}
