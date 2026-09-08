// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestArtifactMetadataSearchTreatsNamesLiterallyAndNeverSearchesBytes(t *testing.T) {
	h := newHarness(t, "api_artifact_search")
	for _, name := range []string{"Report_%one", "Report_axone", `C:\notes\one`} {
		r := pg.ArtifactPut{ID: uuid.NewString(), DataSubjectID: "owner-1", Kind: "file", Name: name, Content: []byte("hidden-content-marker")}
		h.do(t, http.MethodPost, "/v1/artifacts/put", artifactHTTPBody(r), 200, nil)
	}
	for _, tc := range []struct {
		prefix, kind string
		count        int
	}{{"Report_%", "file", 1}, {"Report", "file", 2}, {"report", "file", 0}, {`C:\notes`, "file", 1}, {"hidden-content-marker", "file", 0}, {"Report", "state", 0}} {
		var page pg.ArtifactPage
		h.do(t, http.MethodPost, "/v1/artifacts/list", map[string]any{"name_prefix": tc.prefix, "kind": tc.kind, "data_subject_id": "owner-1", "limit": 1}, 200, &page)
		found := len(page.Artifacts)
		if page.Next != nil {
			var rest pg.ArtifactPage
			h.do(t, http.MethodPost, "/v1/artifacts/list", map[string]any{"name_prefix": tc.prefix, "kind": tc.kind, "data_subject_id": "owner-1", "after": page.Next.ID}, 200, &rest)
			found += len(rest.Artifacts)
		}
		if found != tc.count {
			t.Fatalf("literal metadata search %q/%s: %d, want %d", tc.prefix, tc.kind, found, tc.count)
		}
	}
	for _, bad := range []map[string]any{{"kind": "document"}, {"name_prefix": strings.Repeat("x", 257)}, {"name_prefix": "\x00"}} {
		h.do(t, http.MethodPost, "/v1/artifacts/list", bad, 400, nil)
	}
	store := pg.NewArtifactStore(h.pool, h.schema)
	if _, err := store.List(context.Background(), "p1", "", "", 1, pg.ArtifactFilter{}, pg.ArtifactFilter{}); err == nil {
		t.Fatal("ambiguous filter configuration accepted")
	}
	var page pg.ArtifactPage
	h.do(t, http.MethodPost, "/v1/artifacts/list", map[string]any{"kind": "file"}, 200, &page)
	if len(page.Artifacts) != 3 {
		t.Fatal("kind-only filter failed")
	}
	h.do(t, http.MethodPost, "/v1/artifacts/list", map[string]any{"name_prefix": "Report"}, 200, &page)
	if len(page.Artifacts) != 2 {
		t.Fatal("name-only filter failed")
	}
}
