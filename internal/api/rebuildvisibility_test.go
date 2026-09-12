// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// A caller polling freshness learns that the project is being reinterpreted, and a caller asking a
// question does not pay for that. Recall answering from two extractors at once is a state the
// product already had; what it did not have was any way for a client to know.
func TestFreshnessOverHTTPSaysAProjectIsBeingReinterpreted(t *testing.T) {
	h := newHarness(t, "api_rebuild_visible")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}, 201, nil)
	h.form(t)

	var quiet map[string]any
	h.do(t, http.MethodGet, "/v1/freshness", nil, 200, &quiet)
	if _, present := quiet["rebuilding"]; present {
		t.Fatalf("a project nobody is rebuilding carries the field: %+v", quiet)
	}

	jobs := pg.NewRebuildJobStore(h.pool)
	key, actor := uuid.NewString(), uuid.NewString()
	version := "extract/v2:" + strings.Repeat("c", 64)
	session, err := jobs.Acquire(ctx, h.schema, "p1", key, version, actor)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	var active struct {
		Stored     *int64 `json:"stored"`
		Rebuilding *struct {
			ReinterpretedThrough  int64 `json:"reinterpreted_through"`
			ReinterpretingThrough int64 `json:"reinterpreting_through"`
			Acknowledged          int64 `json:"sources_acknowledged"`
			Skipped               int64 `json:"sources_skipped"`
		} `json:"rebuilding"`
	}
	status, raw := h.raw(t, http.MethodGet, "/v1/freshness", nil, h.token)
	if status != http.StatusOK || json.Unmarshal(raw, &active) != nil {
		t.Fatalf("freshness returned %d: %s", status, raw)
	}
	if active.Rebuilding == nil {
		t.Fatalf("a project mid-rebuild says nothing: %s", raw)
	}
	if active.Stored == nil || active.Rebuilding.ReinterpretingThrough != *active.Stored {
		t.Fatalf("the job stops at the offset it recorded, and the wire disagrees: %s", raw)
	}
	if active.Rebuilding.Acknowledged != 0 || active.Rebuilding.Skipped != 0 {
		t.Fatalf("a job that has done nothing reports work: %s", raw)
	}

	// Offsets and counts, and nothing else. The job record holds no source identifier, subject or
	// message text, and this asserts that none arrived by another route.
	body := string(raw)
	for _, secret := range []string{"subject-1", "Ensera", key, actor, version, "job_id", "lease"} {
		if strings.Contains(body, secret) {
			t.Fatalf("freshness disclosed %q: %s", secret, body)
		}
	}

	// The read path is untouched: a recall taken during a rebuild is the same call it always was,
	// and nothing about the job reaches its answer.
	var bundle map[string]any
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Where do I work?"}, 200, &bundle)
	if _, leaked := bundle["rebuilding"]; leaked {
		t.Fatal("the rebuild state reached the read path")
	}
}
