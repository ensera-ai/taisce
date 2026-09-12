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

// ── The boundary between end users under one credential ───────────────────────────────────────
//
// One credential, many end users. An application that serves several people through one project
// credential can reach every one of their objects, because the credential is what the deployment
// authenticates and a data subject is not a principal. Naming the subject on a read makes the
// requirement part of the statement that selects the row rather than a habit in the application:
// another person's object is answered exactly as one that does not exist, because to that caller it
// does not.
func TestAnObjectReadUnderTheWrongPersonIsAnsweredAsOneThatIsNotThere(t *testing.T) {
	h := newHarness(t, "api_artifact_subject")
	marta := uuid.NewString()
	h.do(t, http.MethodPost, "/v1/artifacts/put", map[string]any{
		"id": marta, "data_subject_id": "marta", "kind": "state", "name": "session",
		"content": []byte("marta's session"),
	}, http.StatusOK, nil)

	// Named correctly, it reads.
	var mine struct {
		ID      string `json:"id"`
		Content []byte `json:"content"`
		Version string `json:"version"`
	}
	h.do(t, http.MethodPost, "/v1/artifacts/get", map[string]any{"id": marta, "data_subject_id": "marta"}, http.StatusOK, &mine)
	if string(mine.Content) != "marta's session" {
		t.Fatalf("the owner could not read their own object: %q", mine.Content)
	}

	// Named as somebody else's, it is not there — and byte for byte the same answer as an id that
	// was never issued, so a caller learns nothing by asking.
	wrong, wrongBody := h.raw(t, http.MethodPost, "/v1/artifacts/get",
		map[string]any{"id": marta, "data_subject_id": "tomas"}, h.token)
	absent, absentBody := h.raw(t, http.MethodPost, "/v1/artifacts/get",
		map[string]any{"id": uuid.NewString(), "data_subject_id": "tomas"}, h.token)
	if wrong != http.StatusNotFound || absent != http.StatusNotFound || !bytes.Equal(wrongBody, absentBody) {
		t.Fatalf("one person's object was distinguishable from nothing: %d %d %s %s", wrong, absent, wrongBody, absentBody)
	}

	// Deleting under the wrong person is the same mistake, spent, and is refused the same way.
	if status, _ := h.raw(t, http.MethodPost, "/v1/artifacts/delete",
		map[string]any{"id": marta, "expected_version": mine.Version, "data_subject_id": "tomas"}, h.token); status != http.StatusNotFound {
		t.Fatalf("another person's object was deleted: %d", status)
	}
	h.do(t, http.MethodPost, "/v1/artifacts/get", map[string]any{"id": marta, "data_subject_id": "marta"}, http.StatusOK, nil)

	// Unnamed, it behaves exactly as it did before the field existed, which is what the freeze
	// requires of an added request field.
	h.do(t, http.MethodPost, "/v1/artifacts/get", map[string]any{"id": marta}, http.StatusOK, nil)
	h.do(t, http.MethodPost, "/v1/artifacts/delete",
		map[string]any{"id": marta, "expected_version": mine.Version}, http.StatusOK, nil)
}
