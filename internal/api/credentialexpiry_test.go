// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// TestAnExpiredCredentialIsAStrangerAtBothDoors asks every route of both surfaces with a credential
// that has expired and with a token nobody issued, and requires the same refusal.
//
// The comparison is with a real stranger rather than a status written here, so a route this harness
// does not mount is not mistaken for one that opened — and so "exactly as a stranger" is the thing
// checked, body included: an expired credential that got a refusal of its own would tell whoever
// holds a stolen one that it was once good.
func TestAnExpiredCredentialIsAStrangerAtBothDoors(t *testing.T) {
	m := newManaged(t, "api_credential_expiry")
	ctx := context.Background()
	credentials := credential.NewStore(m.pool, string(migrate.ControlSchema))
	own, err := credentials.Resolve(ctx, m.token)
	if err != nil {
		t.Fatal(err)
	}
	const brief = 700 * time.Millisecond
	project, _, err := credentials.IssueExpiring(ctx, "ends-soon", own.Project, credential.ReadWrite, brief)
	if err != nil {
		t.Fatal(err)
	}
	operator, _, err := credentials.IssueOperatorFor(ctx, "ends-soon-operator", brief)
	if err != nil {
		t.Fatal(err)
	}
	// Both work while they live, on a route each door serves.
	if status, body := m.raw(t, http.MethodGet, "/"+api.Version+"/freshness", nil, project); status != http.StatusOK {
		t.Fatalf("a credential inside its lifetime was refused: %d %s", status, body)
	}
	if status, body := rawAgainst(t, m.manage.URL, http.MethodPost, api.ManagementPrefix+"/projects/list", map[string]any{}, operator); status != http.StatusOK {
		t.Fatalf("an operator credential inside its lifetime was refused: %d %s", status, body)
	}

	time.Sleep(brief + 300*time.Millisecond)

	nobody := credential.Prefix + strings.Repeat("A", 43)
	same := func(what string, status int, body []byte, strangerStatus int, strangerBody []byte) {
		t.Helper()
		if status != strangerStatus || string(body) != string(strangerBody) || status < 400 {
			t.Errorf("%s answered an expired credential %d %s and a stranger %d %s; the two must be the same refusal",
				what, status, body, strangerStatus, strangerBody)
		}
	}
	for _, o := range api.Operations() {
		var body map[string]any
		if o.Method != http.MethodGet {
			body = map[string]any{}
		}
		status, raw := m.raw(t, o.Method, "/"+api.Version+o.Path, body, project)
		strangerStatus, strangerBody := m.raw(t, o.Method, "/"+api.Version+o.Path, body, nobody)
		same("memory operation "+o.Name, status, raw, strangerStatus, strangerBody)
	}
	if status, _ := m.raw(t, http.MethodGet, "/"+api.Version+"/freshness", nil, project); status != http.StatusUnauthorized {
		t.Errorf("the route that answered the live credential answers the expired one %d", status)
	}
	for _, o := range api.ManagementOperations() {
		status, body := rawAgainst(t, m.manage.URL, o.Method, api.ManagementPrefix+o.Path, map[string]any{}, operator)
		strangerStatus, strangerBody := rawAgainst(t, m.manage.URL, o.Method, api.ManagementPrefix+o.Path, map[string]any{}, nobody)
		same("management operation "+o.Name, status, body, strangerStatus, strangerBody)
		if status != http.StatusUnauthorized {
			t.Errorf("management operation %s answered an expired operator credential %d", o.Name, status)
		}
	}
	if resp, _ := scrapeMetrics(t, m.manage.URL, operator); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an expired operator credential still scrapes: %d", resp.StatusCode)
	}
}

// TestTheCredentialListSaysWhenEachEndsAndWhenItLastActed is the list an operator reads before
// revoking: which credentials have a date, and which the ledger has seen.
func TestTheCredentialListSaysWhenEachEndsAndWhenItLastActed(t *testing.T) {
	m := newManaged(t, "api_credential_last_acted")
	ctx := context.Background()
	own, err := credential.NewStore(m.pool, string(migrate.ControlSchema)).Resolve(ctx, m.token)
	if err != nil {
		t.Fatal(err)
	}
	type issued struct {
		ID, Token string
		ExpiresAt *time.Time `json:"expires_at"`
	}
	var used, idle issued
	m.manageDo(t, "/credentials/issue", map[string]any{"name": "used", "project": own.Project, "lifetime_days": 30}, m.operator, http.StatusCreated, &used)
	m.manageDo(t, "/credentials/issue", map[string]any{"name": "idle", "project": own.Project}, m.operator, http.StatusCreated, &idle)
	if used.ExpiresAt == nil || time.Until(*used.ExpiresAt) < 29*24*time.Hour || time.Until(*used.ExpiresAt) > 31*24*time.Hour {
		t.Fatalf("a credential issued for thirty days was told it ends at %v", used.ExpiresAt)
	}
	if idle.ExpiresAt != nil {
		t.Fatalf("a credential issued with no lifetime was given a date: %v", idle.ExpiresAt)
	}
	for _, days := range []int{-1, credential.MaxLifetimeDays + 1} {
		m.manageDo(t, "/credentials/issue", map[string]any{"name": "bad", "project": own.Project, "lifetime_days": days}, m.operator, http.StatusBadRequest, nil)
	}

	before := time.Now().Add(-time.Minute)
	if status, body := m.raw(t, http.MethodGet, "/"+api.Version+"/freshness", nil, used.Token); status != http.StatusOK {
		t.Fatalf("the credential could not act: %d %s", status, body)
	}

	var listed struct {
		Credentials []credential.Listed `json:"credentials"`
	}
	m.manageDo(t, "/credentials/list", map[string]any{"project": own.Project}, m.operator, http.StatusOK, &listed)
	seen := map[string]credential.Listed{}
	for _, l := range listed.Credentials {
		seen[l.CredentialID] = l
	}
	if l := seen[used.ID]; l.LastActedAt == nil || l.LastActedAt.Before(before) || l.ExpiresAt == nil || l.Expired {
		t.Errorf("the credential that acted is listed without when, or without its date: %+v", l)
	}
	if l, ok := seen[idle.ID]; !ok || l.LastActedAt != nil || l.ExpiresAt != nil {
		t.Errorf("the credential that never acted is listed as having acted, or with a date: %+v", l)
	}
	// The operator's own credential is in the ledger too: it just issued two credentials.
	m.manageDo(t, "/credentials/list", map[string]any{}, m.operator, http.StatusOK, &listed)
	found := false
	for _, l := range listed.Credentials {
		if l.Kind == credential.KindOperator && l.LastActedAt != nil && l.ExpiresAt != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("no operator credential is listed with both a last act and an expiry: %+v", listed.Credentials)
	}
}
