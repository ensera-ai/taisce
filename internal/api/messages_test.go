// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"encoding/json"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/passage"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

func sourceChunkForInspection(t *testing.T, h *harness) string {
	t.Helper()
	var id string
	if err := h.pool.QueryRow(context.Background(), h.schema.SQL(`SELECT m.chunk_id::text FROM {schema}.turn_message m JOIN {schema}.observation o USING(observation_id) WHERE o.scope='p1' ORDER BY o.log_offset,m.ordinal LIMIT 1`)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A preview continues into its exact message, even when neighboring text plausibly matches. Every
// page carries the digest that prevents a caller combining independently changed source versions.
func TestMessageWindowsResolveSavedPassagesAcrossGenerationChanges(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, "api_message_reference")
	content := strings.Repeat("مرحبا世界🧠", 90) + " PostgreSQL chosen by the assistant."
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "PostgreSQL chosen by the assistant. Neighboring message."}, {"role": "assistant", "content": content}}}, 201, nil)
	fixture := attachPassages(t, h, nil)
	fixture.activate(t, "p1", "v1")
	var found passage.Page
	h.do(t, http.MethodPost, "/v1/passages/search", map[string]any{"question": "PostgreSQL", "limit": 1, "source_role": "assistant"}, 200, &found)
	if len(found.Passages) != 1 || found.Passages[0].PreviewComplete {
		t.Fatalf("fixture needs one truncated source %+v", found)
	}
	ref := found.Passages[0]
	if len(ref.ContentDigest) != 64 {
		t.Fatal("preview lacks a continuation digest")
	}
	fixture.activate(t, "p1", "v2")
	calls := fixture.calls.Load()
	assembled := ref.Preview
	next := ref.PreviewByteEnd
	for next < len(content) {
		var window pg.SourceMessageWindow
		h.do(t, http.MethodPost, "/v1/messages/get", map[string]any{"chunk_id": ref.ChunkID, "byte_start": next, "byte_limit": 17, "expected_digest": ref.ContentDigest}, 200, &window)
		if window.SourceID != ref.SourceID || window.Ordinal != 1 || window.Role != "assistant" || !window.OccurredAt.Equal(ref.OccurredAt) || window.ContentDigest != ref.ContentDigest || window.ByteStart != next || window.ByteEnd <= next || len(window.Text) > 17 || !utf8.ValidString(window.Text) || window.MessageBytes != len(content) {
			t.Fatalf("wrong source window %+v", window)
		}
		assembled += window.Text
		next = window.ByteEnd
		if (window.NextByteStart == nil) != (next == len(content)) {
			t.Fatal("incorrect continuation")
		}
	}
	if assembled != content || fixture.calls.Load() != calls {
		t.Fatal("source was mixed, truncated, or inferred")
	}
	var full pg.SourceMessageWindow
	h.do(t, http.MethodPost, "/v1/messages/get", map[string]any{"chunk_id": "urn:uuid:" + ref.ChunkID}, 200, &full)
	if !full.Complete || full.NextByteStart != nil || full.Text != content {
		t.Fatalf("complete bounded source %+v", full)
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.turn_message SET content='changed source' WHERE chunk_id=$1::uuid`), ref.ChunkID); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPost, "/v1/messages/get", map[string]any{"chunk_id": ref.ChunkID, "expected_digest": ref.ContentDigest}, 409, nil)
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "subject-1", "reason": "test"}, 200, nil)
	h.do(t, http.MethodPost, "/v1/messages/get", map[string]any{"chunk_id": ref.ChunkID}, 404, nil)
}

func TestMessageWindowsBoundInputsAndDoNotExposeForeignOrExpiredSources(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, "api_message_bounds")
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-private", "messages": []map[string]any{{"role": "user", "content": "مرحبا secret source"}}}, 201, nil)
	id := sourceChunkForInspection(t, h)
	var first pg.SourceMessageWindow
	h.do(t, http.MethodPost, "/v1/messages/get", map[string]any{"chunk_id": id, "byte_limit": 5}, 200, &first)
	if first.ByteEnd != 4 || first.Text != "مر" || first.Complete {
		t.Fatalf("split Unicode code point %+v", first)
	}
	for _, body := range []map[string]any{
		{}, {"chunk_id": "bad"}, {"chunk_id": id, "byte_limit": 0}, {"chunk_id": id, "byte_limit": 3}, {"chunk_id": id, "byte_limit": 4097},
		{"chunk_id": id, "byte_start": -1}, {"chunk_id": id, "byte_start": 65537},
		{"chunk_id": id, "byte_start": 1}, {"chunk_id": id, "byte_start": 1, "expected_digest": first.ContentDigest},
		{"chunk_id": id, "byte_start": 100, "expected_digest": first.ContentDigest},
		{"chunk_id": id, "expected_digest": "bad"}, {"chunk_id": id, "expected_digest": strings.Repeat("A", 64)},
		{"chunk_id": id, "scope": "p2"},
	} {
		h.do(t, http.MethodPost, "/v1/messages/get", body, 400, nil)
	}
	h.do(t, http.MethodPost, "/v1/messages/get", map[string]any{"chunk_id": id, "expected_digest": strings.Repeat("0", 64)}, 409, nil)
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	token, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).IssueWithAccess(ctx, "other-reader", "p2", credential.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	status, foreign := h.raw(t, http.MethodPost, "/v1/messages/get", map[string]any{"chunk_id": id}, token)
	absentStatus, absent := h.raw(t, http.MethodPost, "/v1/messages/get", map[string]any{"chunk_id": uuid.NewString()}, token)
	if status != 404 || absentStatus != 404 || string(foreign) != string(absent) {
		t.Fatalf("foreign existence exposed %d/%d", status, absentStatus)
	}
	entries, err := pg.NewAuditStore(h.pool, h.schema).Recent(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.Operation == domain.AuditMessageGet {
			count++
			if entry.PrincipalKind != domain.PrincipalCredential {
				t.Fatal("incorrect read attribution")
			}
		}
	}
	raw, _ := json.Marshal(entries)
	if count == 0 || strings.Contains(string(raw), "secret source") || strings.Contains(string(raw), first.ContentDigest) || strings.Contains(string(raw), id) || strings.Contains(string(raw), "subject-private") {
		t.Fatal("source identity/content in permanent audit")
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.observation SET retention_until=clock_timestamp()-interval '1 second' WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	if sweep, err := pg.NewRetentionStore(h.pool, h.schema).Sweep(ctx, "p1", 100); err != nil || sweep.Observations != 1 {
		t.Fatalf("expiry %+v %v", sweep, err)
	}
	h.do(t, http.MethodPost, "/v1/messages/get", map[string]any{"chunk_id": id}, 404, nil)
}

func TestMessageWindowsRefuseOversizedOrUnavailableAuthoritativeStorage(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, "api_message_storage")
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "original"}}}, 201, nil)
	id := sourceChunkForInspection(t, h)
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.turn_message SET content=repeat('x',65537) WHERE chunk_id=$1::uuid`), id); err != nil {
		t.Fatal(err)
	}
	status, raw := h.raw(t, http.MethodPost, "/v1/messages/get", map[string]any{"chunk_id": id}, h.token)
	if status != 500 || len(raw) > 1024 {
		t.Fatal("oversized source escaped the read bound")
	}
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.turn_message RENAME TO unavailable_message`)); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPost, "/v1/messages/get", map[string]any{"chunk_id": id}, 500, nil)
}
