// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// An erasure or an export naming more source observations than one request may is refused before
// anything is read, and a body that is not JSON is refused before anything is decided. The bound is
// the service's, so a client that skipped its own check still meets it.
func TestAGovernanceRequestOverTheSourceBoundOrNotJSONIsRefusedBeforeAnythingIsRead(t *testing.T) {
	h := newHarness(t, "api_governance_bounds")
	many := make([]string, 10001)
	for i := range many {
		many[i] = uuid.NewString()
	}
	for path, code := range map[string]string{"/v1/erasures": "invalid_erasure", "/v1/exports": "invalid_export"} {
		body := map[string]any{"source_observation_ids": many}
		if path == "/v1/erasures" {
			body["reason"] = "bulk"
		}
		status, raw := h.raw(t, http.MethodPost, path, body, h.token)
		if status != http.StatusBadRequest || !bytes.Contains(raw, []byte(code)) || !bytes.Contains(raw, []byte("10000")) {
			t.Errorf("%s with 10001 sources answered %d %s", path, status, raw)
		}
		if status, raw := h.rawBody(t, http.MethodPost, path, `{"data_subject_id":`, h.token); status != http.StatusBadRequest {
			t.Errorf("%s with a truncated body answered %d %s", path, status, raw)
		}
	}
}

// A person holding more rows than one export returns is refused with export_too_large, never handed
// part of what is held: a partial export is a false answer to an access request. The bound is
// lowered for the test so that one turn crosses it.
func TestAnExportLargerThanOneResponseIsRefusedRatherThanCut(t *testing.T) {
	h := newHarness(t, "api_export_too_large")
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "subject-1",
		"messages":        []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}},
	}, http.StatusCreated, nil)
	was := pg.MaxExportRows
	pg.MaxExportRows = 1
	t.Cleanup(func() { pg.MaxExportRows = was })
	status, raw := h.raw(t, http.MethodPost, "/v1/exports", map[string]any{"data_subject_id": "subject-1"}, h.token)
	if status != http.StatusUnprocessableEntity || !bytes.Contains(raw, []byte("export_too_large")) {
		t.Fatalf("an export over the bound answered %d %s", status, raw)
	}
	if !strings.Contains(string(raw), "by source") {
		t.Fatalf("the refusal does not say what to do instead: %s", raw)
	}
}

// Setting a retention policy names the project it is for; one naming none is refused rather than
// applied to whatever an empty name resolves to.
func TestARetentionPolicyForNoProjectIsRefused(t *testing.T) {
	m := newManaged(t, "api_retention_nameless")
	m.manageDo(t, "/projects/retention", map[string]any{"name": " ", "retention_days": 30}, m.operator, http.StatusBadRequest, nil)
}
