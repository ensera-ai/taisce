// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

// The journey a caller actually performs: find a record, report a doubt about it, read the queue,
// promote the one that was right, and see the graph change only then.
func TestFeedbackIsRecordedListedAndPromotedOverHTTP(t *testing.T) {
	h := newHarness(t, "api_feedback")
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1",
		"messages": []map[string]any{{"role": "user", "content": "I work at Ensera and I live in Dublin."}}}, 201, nil)
	h.form(t)
	var records pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{"data_subject_id": "subject-1"}, 200, &records)
	if len(records.Records) == 0 {
		t.Fatal("nothing to give feedback about")
	}
	target := records.Records[0]

	var written pg.Feedback
	h.do(t, http.MethodPost, "/v1/feedback/record", map[string]any{
		"record_id": target.ID, "note": "that employer is out of date", "proposed_object": "Orbit"}, 200, &written)
	if written.ID == "" || written.RecordID != target.ID || written.PromotedAt != nil {
		t.Fatalf("recorded feedback: %+v", written)
	}

	// Reporting a doubt is not asserting one. The inventory must read exactly as it did.
	var unchanged pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{"data_subject_id": "subject-1"}, 200, &unchanged)
	if len(unchanged.Records) != len(records.Records) || unchanged.Records[0].Version != target.Version {
		t.Fatal("recording feedback changed the record inventory")
	}

	var open pg.FeedbackPage
	h.do(t, http.MethodPost, "/v1/feedback/list", map[string]any{"record_id": target.ID, "open_only": true}, 200, &open)
	if len(open.Feedback) != 1 || open.Feedback[0].ID != written.ID || open.Feedback[0].ProposedObject == nil {
		t.Fatalf("open queue: %+v", open.Feedback)
	}

	var promotion pg.Promotion
	h.do(t, http.MethodPost, "/v1/feedback/promote", map[string]any{
		"id": written.ID, "expected_version": target.Version}, 200, &promotion)
	if promotion.Operation != domain.AuditRecordCorrect || promotion.ReplacementID == nil || promotion.Version == "" {
		t.Fatalf("promotion: %+v", promotion)
	}

	// What promotion produced is an ordinary record, reachable by every route that reads one.
	var citation pg.Citation
	h.do(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": *promotion.ReplacementID}, 200, &citation)
	if citation.Statement != "that employer is out of date" {
		t.Fatalf("the promoted record does not carry the reporter's words: %q", citation.Statement)
	}
	var withdrawn pg.Citation
	h.do(t, http.MethodPost, "/v1/citations/resolve", map[string]any{"id": target.ID}, 200, &withdrawn)
	if withdrawn.Retraction == nil || withdrawn.Retraction.ReplacementFactID == nil ||
		*withdrawn.Retraction.ReplacementFactID != *promotion.ReplacementID {
		t.Fatalf("the promotion did not withdraw its target into the replacement: %+v", withdrawn.Retraction)
	}

	// Promoted once. A retry after a timeout must say so rather than withdrawing a second record.
	status, raw := h.raw(t, http.MethodPost, "/v1/feedback/promote", map[string]any{
		"id": written.ID, "expected_version": target.Version}, h.token)
	if status != http.StatusConflict {
		t.Fatalf("second promotion returned %d: %s", status, raw)
	}
	h.do(t, http.MethodPost, "/v1/feedback/list", map[string]any{"open_only": true}, 200, &open)
	if len(open.Feedback) != 0 {
		t.Fatalf("a promoted feedback is still open: %+v", open.Feedback)
	}
}

// A project's feedback, and the records it is about, are invisible from another project — and the
// refusals are identical, so existence cannot be read off which one came back.
func TestFeedbackRevealsNothingAcrossProjects(t *testing.T) {
	h := newHarness(t, "api_feedback_isolation")
	ctx := context.Background()
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1",
		"messages": []map[string]any{{"role": "user", "content": "I live in Dublin."}}}, 201, nil)
	h.form(t)
	var records pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{"data_subject_id": "subject-1"}, 200, &records)
	target := records.Records[0]
	var written pg.Feedback
	h.do(t, http.MethodPost, "/v1/feedback/record", map[string]any{
		"record_id": target.ID, "note": "he moved", "proposed_object": "Oslo"}, 200, &written)

	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	token, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).Issue(ctx, "other-feedback-project", "p2")
	if err != nil {
		t.Fatal(err)
	}
	status, foreign := h.raw(t, http.MethodPost, "/v1/feedback/promote",
		map[string]any{"id": written.ID, "expected_version": target.Version}, token)
	if status != http.StatusNotFound {
		t.Fatalf("foreign promotion returned %d: %s", status, foreign)
	}
	status, missing := h.raw(t, http.MethodPost, "/v1/feedback/promote",
		map[string]any{"id": uuid.NewString(), "expected_version": target.Version}, token)
	if status != http.StatusNotFound || string(missing) != string(foreign) {
		t.Fatalf("feedback existence leaked: %s then %s", foreign, missing)
	}
	status, refused := h.raw(t, http.MethodPost, "/v1/feedback/record",
		map[string]any{"record_id": target.ID, "note": "a foreign note"}, token)
	if status != http.StatusNotFound {
		t.Fatalf("feedback was attached to another project's record: %d %s", status, refused)
	}
	status, body := h.raw(t, http.MethodPost, "/v1/feedback/list", map[string]any{}, token)
	var empty pg.FeedbackPage
	if status != http.StatusOK || json.Unmarshal(body, &empty) != nil || len(empty.Feedback) != 0 {
		t.Fatalf("another project's queue was readable: %d %s", status, body)
	}
	// And the note never reached the other project's answer in any form.
	if _, body := h.raw(t, http.MethodPost, "/v1/feedback/list",
		map[string]any{"record_id": target.ID}, token); len(body) > 0 &&
		json.Unmarshal(body, &empty) == nil && len(empty.Feedback) != 0 {
		t.Fatalf("narrowing to a foreign record disclosed its feedback: %s", body)
	}
}

// Every refusal the routes answer with, over HTTP, in the shape a client parses. A status a caller
// cannot distinguish from another is a client that retries the wrong thing.
func TestFeedbackRoutesRefuseWhatTheyCannotServe(t *testing.T) {
	h := newHarness(t, "api_feedback_refusals")
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1",
		"messages": []map[string]any{{"role": "user", "content": "I live in Dublin."}}}, 201, nil)
	h.form(t)
	var records pg.RecordPage
	h.do(t, http.MethodPost, "/v1/records/list", map[string]any{"data_subject_id": "subject-1"}, 200, &records)
	target := records.Records[0]
	var written pg.Feedback
	h.do(t, http.MethodPost, "/v1/feedback/record", map[string]any{
		"record_id": target.ID, "note": "he moved", "proposed_object": "Oslo"}, 200, &written)

	for _, tc := range []struct {
		name, path, body string
		want             int
	}{
		{"no record", "record", `{"note":"orphan"}`, http.StatusBadRequest},
		{"no note", "record", `{"record_id":"` + target.ID + `"}`, http.StatusBadRequest},
		{"a blank note", "record", `{"record_id":"` + target.ID + `","note":"   "}`, http.StatusBadRequest},
		{"an empty proposal", "record", `{"record_id":"` + target.ID + `","note":"n","proposed_object":""}`, http.StatusBadRequest},
		{"a repeated field", "record", `{"record_id":"` + target.ID + `","note":"a","note":"b"}`, http.StatusBadRequest},
		{"a record that is not there", "record", `{"record_id":"` + uuid.NewString() + `","note":"n"}`, http.StatusNotFound},
		{"a limit below the floor", "list", `{"limit":0}`, http.StatusBadRequest},
		{"a limit past the bound", "list", `{"limit":101}`, http.StatusBadRequest},
		{"an unusable cursor", "list", `{"after":{"id":"bad","recorded_at":"2026-03-01T00:00:00Z"}}`, http.StatusBadRequest},
		{"an unparseable record filter", "list", `{"record_id":"bad"}`, http.StatusBadRequest},
		{"no expected version", "promote", `{"id":"` + written.ID + `"}`, http.StatusBadRequest},
		{"a stale expected version", "promote", `{"id":"` + written.ID + `","expected_version":"` + uuid.NewString() + `"}`, http.StatusConflict},
		{"feedback that is not there", "promote", `{"id":"` + uuid.NewString() + `","expected_version":"` + target.Version + `"}`, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := h.rawBody(t, http.MethodPost, "/v1/feedback/"+tc.path, tc.body, h.token)
			if status != tc.want {
				t.Fatalf("returned %d, want %d: %s", status, tc.want, body)
			}
		})
	}
}

// The feedback writer's bounds are the correction's, and neither of them is the bound on how many
// spellings one entity may carry. So a report that was accepted can still be refused at promotion,
// and the route has to say which bound that was rather than answering with an internal error.
func TestPromotionOverHTTPNamesTheEntityNameBoundItHits(t *testing.T) {
	h := newHarness(t, "api_feedback_name_bound")
	ctx := context.Background()
	facts, observations := pg.NewFactStore(h.pool), pg.NewObservationStore(h.pool)
	// Built through the stores rather than through the route: sixty-five observations and a
	// formation pass each would make this a test of how long the harness takes.
	spell := func(owner, name string) {
		t.Helper()
		text := "Atlas works at " + name
		stored, err := observations.Append(ctx, h.schema, domain.Turn{Scope: "p1", DataSubjectID: owner,
			OccurredAt: time.Now().UTC(), Messages: []domain.Message{{Role: domain.RoleUser, Content: text}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := facts.Assert(ctx, h.schema, "p1", stored.ID, domain.RoleUser, owner, domain.Claim{
			Subject: "Atlas", Predicate: "works_at", Object: name, Statement: text,
			Cardinality: domain.CardinalityMany, Quote: text, ByteEnd: len(text)}); err != nil {
			t.Fatal(err)
		}
	}
	spell("base", "Ensera")
	spell("shouted", "ENSERA")
	for i := 1; i < pg.MaxEntityNameVariants; i++ {
		spell("variants", strings.Repeat(" ", i)+"Ensera")
	}
	var target, version string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT fact_id::text,version::text FROM {schema}.fact
        WHERE scope='p1' AND upper_inf(known) ORDER BY fact_id LIMIT 1`)).Scan(&target, &version); err != nil {
		t.Fatal(err)
	}
	var written pg.Feedback
	h.do(t, http.MethodPost, "/v1/feedback/record", map[string]any{"record_id": target,
		"note": "spell it with the padding", "proposed_object": strings.Repeat(" ", pg.MaxEntityNameVariants) + "Ensera"}, 200, &written)
	status, body := h.raw(t, http.MethodPost, "/v1/feedback/promote",
		map[string]any{"id": written.ID, "expected_version": version}, h.token)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "invalid_record_mutation") {
		t.Fatalf("returned %d: %s", status, body)
	}
	if !strings.Contains(string(body), "spelling") {
		t.Fatalf("the refusal does not say which bound it hit: %s", body)
	}
}

// A deployment wired without the feedback store refuses the three routes rather than panicking on
// the first call. The guard exists because Stores is a struct a caller fills in, so a capability can
// be left out — and a nil dereference inside a handler takes the whole process down, including every
// project that had nothing to do with the call.
func TestFeedbackRoutesRefuseWhenTheDeploymentHasNoFeedbackStore(t *testing.T) {
	h := newHarness(t, "api_feedback_unwired")
	unwired := httptest.NewServer(api.NewServer(
		credential.NewStore(h.pool, string(migrate.ControlSchema)), api.Stores{
			Audit:        pg.NewAuditStore(h.pool, h.schema),
			Projects:     pg.NewProjectStore(h.pool, h.schema),
			Observations: pg.NewObservationStore(h.pool),
		}, h.schema, nil).Handler())
	t.Cleanup(unwired.Close)

	for _, route := range []struct{ path, body string }{
		{"/v1/feedback/record", `{"record_id":"6a5e3a2c-1f0b-4a4e-9a1e-0c2b5a7d1f30","note":"n"}`},
		{"/v1/feedback/list", `{}`},
		{"/v1/feedback/promote", `{"id":"6a5e3a2c-1f0b-4a4e-9a1e-0c2b5a7d1f30","expected_version":"6a5e3a2c-1f0b-4a4e-9a1e-0c2b5a7d1f31"}`},
	} {
		t.Run(route.path, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, unwired.URL+route.path, strings.NewReader(route.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+h.token)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusInternalServerError {
				t.Fatalf("returned %d: %s", response.StatusCode, body)
			}
			// And it says nothing about why, which is the rule for an internal refusal: a caller
			// learns the request did not happen, not how this deployment is assembled.
			if !strings.Contains(string(body), "internal") || strings.Contains(string(body), "nil") {
				t.Fatalf("the refusal described the deployment: %s", body)
			}
		})
	}
}
