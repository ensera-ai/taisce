// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/recall"
)

type controlledRecallResponse struct {
	Facts      []json.RawMessage        `json:"facts"`
	Characters int                      `json:"characters"`
	Truncated  bool                     `json:"truncated"`
	Controls   recall.EffectiveControls `json:"controls"`
}

func seedRecallSources(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleTool, domain.RoleAssistant, domain.RoleSystem} {
		content := "Atlas works at Ensera — وثيقة🌍 " + string(role)
		turn := domain.Turn{Scope: "p1", DataSubjectID: "source-owner", OccurredAt: time.Now().UTC(), Messages: []domain.Message{{Role: role, Content: content}}}
		obs, err := pg.NewObservationStore(h.pool).Append(ctx, h.schema, turn)
		if err != nil {
			t.Fatal(err)
		}
		attribution := ""
		if role == domain.RoleUser {
			attribution = "source-owner"
		}
		_, err = pg.NewFactStore(h.pool).Assert(ctx, h.schema, "p1", obs.ID, role, attribution, domain.Claim{Subject: "Atlas", Predicate: "works_at", Object: "Ensera", Statement: content, Cardinality: domain.CardinalityMany, Quote: content, ByteEnd: len(content), ValidFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// This fixture is portable to SDK conformance runners. Seed one named fact per stored source role,
// with a 100000-character server ceiling, and apply the request/response expectations unchanged.
func TestRecallControlConformanceOverHTTP(t *testing.T) {
	h := newHarness(t, "api_recall_controls")
	seedRecallSources(t, h)
	var fixture struct {
		ServerMaxCharacters int `json:"server_max_characters"`
		Cases               []struct {
			Name      string         `json:"name"`
			Request   map[string]any `json:"request"`
			Status    int            `json:"status"`
			Facts     int            `json:"facts"`
			Roles     []string       `json:"roles"`
			Hops      int            `json:"hops"`
			Budget    int            `json:"budget"`
			Truncated bool           `json:"truncated"`
		} `json:"cases"`
	}
	data, err := os.ReadFile("testdata/recall-controls.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			status, body := h.raw(t, http.MethodPost, "/v1/recalls", tc.Request, h.token)
			if status != tc.Status {
				t.Fatalf("status %d want %d: %s", status, tc.Status, body)
			}
			if status != 200 {
				return
			}
			var got controlledRecallResponse
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Facts) != tc.Facts || got.Truncated != tc.Truncated || got.Controls.MaxCharacters != tc.Budget || got.Controls.MaxCharacters > fixture.ServerMaxCharacters || got.Controls.Hops != tc.Hops || !slices.Equal(got.Controls.SourceRoles, tc.Roles) {
				t.Fatalf("wrong controls/result: %+v", got)
			}
			cost := 0
			for _, raw := range got.Facts {
				var fact map[string]any
				if err := json.Unmarshal(raw, &fact); err != nil {
					t.Fatal(err)
				}
				if !slices.Contains(tc.Roles, fact["source_role"].(string)) {
					t.Fatal("unselected source role returned")
				}
				for _, key := range []string{"subject", "predicate", "object", "statement", "source_role"} {
					cost += utf8.RuneCountInString(fact[key].(string))
				}
				evidence := fact["evidence"].(map[string]any)
				cost += utf8.RuneCountInString(evidence["quote"].(string)) + utf8.RuneCountInString(evidence["context"].(string))
				for _, key := range []string{"path", "via"} {
					for _, v := range fact[key].([]any) {
						cost += utf8.RuneCountInString(v.(string))
					}
				}
			}
			if got.Characters != cost || cost > tc.Budget {
				t.Fatalf("budget accounting %d vs %d, limit %d", got.Characters, cost, tc.Budget)
			}
		})
	}
}

func TestRecallControlsPreserveTemporalSubjectErasureAndProjectBoundaries(t *testing.T) {
	h := newHarness(t, "api_recall_control_scope")
	seedRecallSources(t, h)
	ctx := context.Background()
	roles := []string{"user", "tool", "assistant", "system"}
	for _, extra := range []map[string]any{{"as_of": "1990-01-01T00:00:00Z"}, {"as_known_at": "2000-01-01T00:00:00Z"}, {"data_subject_id": "other-person"}} {
		extra["question"] = "Atlas"
		extra["source_roles"] = roles
		var got controlledRecallResponse
		h.do(t, http.MethodPost, "/v1/recalls", extra, 200, &got)
		if len(got.Facts) != 0 {
			t.Fatal("explicit roles bypassed a read filter")
		}
	}
	if err := migrate.ProvisionScope(ctx, h.pool, h.schema.String(), "p2"); err != nil {
		t.Fatal(err)
	}
	credentials := credential.NewStore(h.pool, string(migrate.ControlSchema))
	foreign, _, err := credentials.Issue(ctx, "recall-other-project", "p2")
	if err != nil {
		t.Fatal(err)
	}
	status, body := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Atlas", "source_roles": roles}, foreign)
	var got controlledRecallResponse
	if status != 200 || json.Unmarshal(body, &got) != nil || len(got.Facts) != 0 {
		t.Fatal("source selection widened project access")
	}
	reader, _, err := credentials.IssueWithAccess(ctx, "recall-second-client", "p1", credential.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token        string
		budget, want int
	}{{h.token, 1, 0}, {reader, 100000, 4}} {
		status, body := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Atlas", "max_characters": tc.budget, "source_roles": roles}, tc.token)
		if status != 200 || json.Unmarshal(body, &got) != nil || len(got.Facts) != tc.want || got.Characters > tc.budget {
			t.Fatal("clients did not receive their own budgets")
		}
	}
	if _, err := pg.NewEraser(h.pool).Erase(ctx, h.schema, "p1", "source-owner", "test"); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Atlas", "source_roles": roles}, 200, &got)
	if len(got.Facts) != 0 {
		t.Fatal("role selection resurrected erased source content")
	}
	entries, err := pg.NewAuditStore(h.pool, h.schema).Recent(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(entries)
	if strings.Contains(string(encoded), "Atlas") || strings.Contains(string(encoded), "source-owner") {
		t.Fatal("recall controls leaked content to audit")
	}
}
