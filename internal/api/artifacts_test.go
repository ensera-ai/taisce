// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

func TestArtifactHTTPJourneyStoresBinaryStateWithoutFormingOrCrossingProjects(t *testing.T) {
	h := newHarness(t, "api_artifacts")
	ctx := context.Background()
	request := pg.ArtifactPut{ID: uuid.NewString(), DataSubjectID: "owner-1", Kind: "file", Name: "../../opaque-name.bin", Content: []byte{0, 255, 128, 'x'}}
	var receipt pg.ArtifactWrite
	h.do(t, http.MethodPost, "/v1/artifacts/put", artifactHTTPBody(request), 200, &receipt)
	var got pg.Artifact
	h.do(t, http.MethodPost, "/v1/artifacts/get", map[string]any{"id": receipt.ID}, 200, &got)
	if !bytes.Equal(got.Content, request.Content) || got.Name != request.Name {
		t.Fatal("binary bytes or opaque name changed")
	}
	// Names are metadata: no filename is interpreted as a local path.
	h.form(t)
	var facts, chunks int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT (SELECT count(*) FROM {schema}.fact),(SELECT count(*) FROM {schema}.chunk)`)).Scan(&facts, &chunks); err != nil || facts != 0 || chunks != 0 {
		t.Fatal("opaque bytes entered formation")
	}
	var page pg.ArtifactPage
	h.do(t, http.MethodPost, "/v1/artifacts/list", map[string]any{"data_subject_id": "owner-1", "limit": 1}, 200, &page)
	if len(page.Artifacts) != 1 || page.Artifacts[0].ID != receipt.ID {
		t.Fatal("stored file absent from inventory")
	}
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	token, _, err := credential.NewStore(h.pool, string(migrate.ControlSchema)).Issue(ctx, "artifact-other", "p2")
	if err != nil {
		t.Fatal(err)
	}
	foreignStatus, foreign := h.raw(t, http.MethodPost, "/v1/artifacts/get", map[string]any{"id": receipt.ID}, token)
	unknownStatus, unknown := h.raw(t, http.MethodPost, "/v1/artifacts/get", map[string]any{"id": uuid.NewString()}, token)
	if foreignStatus != 404 || unknownStatus != 404 || !bytes.Equal(foreign, unknown) {
		t.Fatal("foreign identity reveals existence")
	}
	request.ExpectedVersion = receipt.Version
	request.Content = []byte("replacement")
	var updated pg.ArtifactWrite
	h.do(t, http.MethodPost, "/v1/artifacts/put", artifactHTTPBody(request), 200, &updated)
	h.do(t, http.MethodPost, "/v1/artifacts/put", artifactHTTPBody(request), 200, &receipt)
	if !receipt.Replayed || receipt.Version != updated.Version {
		t.Fatal("HTTP retry repeated overwrite")
	}
	request.Content = []byte("competing replacement")
	h.do(t, http.MethodPost, "/v1/artifacts/put", artifactHTTPBody(request), 409, nil)
	h.do(t, http.MethodPost, "/v1/exports", map[string]any{"data_subject_id": "owner-1"}, 200, nil)
	h.do(t, http.MethodPost, "/v1/artifacts/delete", map[string]any{"id": updated.ID, "expected_version": request.ExpectedVersion}, 409, nil)
	h.do(t, http.MethodPost, "/v1/artifacts/delete", map[string]any{"id": updated.ID, "expected_version": updated.Version}, 200, nil)
	h.do(t, http.MethodPost, "/v1/artifacts/get", map[string]any{"id": updated.ID}, 404, nil)
	request.ExpectedVersion = ""
	h.do(t, http.MethodPost, "/v1/artifacts/put", artifactHTTPBody(request), 409, nil)
}

func TestArtifactHTTPRefusalsAreBoundedAndDoNotExposeStorageDiagnostics(t *testing.T) {
	h := newHarness(t, "api_artifact_refusals")
	ctx := context.Background()
	for _, route := range []string{"put", "get", "list", "delete"} {
		if status, _ := h.rawBody(t, http.MethodPost, "/v1/artifacts/"+route, `{"unknown":1}`, h.token); status != 400 {
			t.Fatal("unknown field accepted")
		}
		if status, _ := h.rawBody(t, http.MethodPost, "/v1/artifacts/"+route, `{"id":`, h.token); status != 400 {
			t.Fatal("invalid JSON accepted")
		}
	}
	for _, body := range []string{`{}`, `{"id":"bad"}`, `{"content":[0,255]}`, `{"content":null}`, `{"content":"AP=="}`} {
		if status, _ := h.rawBody(t, http.MethodPost, "/v1/artifacts/put", body, h.token); status != 400 {
			t.Fatalf("ambiguous request accepted: %d", status)
		}
	}
	h.do(t, http.MethodPost, "/v1/artifacts/get", map[string]any{"id": "bad"}, 400, nil)
	h.do(t, http.MethodPost, "/v1/artifacts/delete", map[string]any{"id": "bad"}, 400, nil)
	h.do(t, http.MethodPost, "/v1/artifacts/list", map[string]any{"limit": 101}, 400, nil)
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`UPDATE {schema}.agent_storage_policy SET max_object_bytes=1,max_bytes=1,max_objects=1 WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	request := pg.ArtifactPut{ID: uuid.NewString(), DataSubjectID: "private-owner", Kind: "state", Name: "private-name", Content: []byte("too large")}
	h.do(t, http.MethodPost, "/v1/artifacts/put", artifactHTTPBody(request), 429, nil)
	request.Content = []byte{0}
	var receipt pg.ArtifactWrite
	h.do(t, http.MethodPost, "/v1/artifacts/put", artifactHTTPBody(request), 200, &receipt)
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`ALTER TABLE {schema}.audit_entry ADD CONSTRAINT private_storage_diagnostic CHECK(operation<>'artifact.put') NOT VALID`)); err != nil {
		t.Fatal(err)
	}
	request.ExpectedVersion = receipt.Version
	request.Content = []byte{1}
	status, body := h.raw(t, http.MethodPost, "/v1/artifacts/put", artifactHTTPBody(request), h.token)
	if status != 500 || bytes.Contains(body, []byte("private")) {
		t.Fatalf("storage diagnostic exposed: %d", status)
	}
	var current pg.Artifact
	h.do(t, http.MethodPost, "/v1/artifacts/get", map[string]any{"id": receipt.ID}, 200, &current)
	if current.Version != receipt.Version || !bytes.Equal(current.Content, []byte{0}) {
		t.Fatal("failed audited write changed bytes")
	}
	h.do(t, http.MethodPost, "/v1/erasures", map[string]any{"data_subject_id": "private-owner", "reason": "requested"}, 200, nil)
	h.do(t, http.MethodPost, "/v1/artifacts/get", map[string]any{"id": receipt.ID}, 404, nil)
	// GET/LIST database failures remain generic as well.
	if _, err := h.pool.Exec(ctx, h.schema.SQL(`DROP TABLE {schema}.agent_artifact CASCADE`)); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"get", "list", "delete"} {
		payload := map[string]any{}
		if route != "list" {
			payload["id"] = receipt.ID
		}
		if route == "delete" {
			payload["expected_version"] = receipt.Version
		}
		status, body := h.raw(t, http.MethodPost, "/v1/artifacts/"+route, payload, h.token)
		var response map[string]any
		if status != 500 || json.Unmarshal(body, &response) != nil || strings.Contains(string(body), "does not exist") {
			t.Fatalf("unbounded storage error: %d", status)
		}
	}
}

func artifactHTTPBody(r pg.ArtifactPut) map[string]any {
	return map[string]any{"id": r.ID, "expected_version": r.ExpectedVersion, "data_subject_id": r.DataSubjectID, "kind": r.Kind, "name": r.Name, "content": r.Content}
}
