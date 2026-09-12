// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// A controlled proposal stream exercises actual formation, persistence and HTTP recall separately
// from the live-model corpus. Quotes remain real input bytes, including the Arabic fixture.
type speakerModel struct{}

func (speakerModel) Propose(_ context.Context, m domain.Message, _ domain.Ontology) ([]extract.Proposal, error) {
	if strings.HasPrefix(m.Content, "John said") {
		return []extract.Proposal{{Subject: "I", Predicate: "lives_in", Object: "London", Quote: m.Content, Statement: m.Content, Polarity: domain.PolarityReported, Tense: domain.TensePresent}}, nil
	}
	if m.Content == "أنا أسكن في عمان." {
		return []extract.Proposal{{Subject: "أنا", Predicate: "lives_in", Object: "عمان", Quote: m.Content, Statement: m.Content, Polarity: domain.PolarityAsserted, Tense: domain.TensePresent}}, nil
	}
	parts := strings.Split(strings.TrimSuffix(m.Content, "."), " and ")
	var out []extract.Proposal
	for _, part := range parts {
		words := strings.Fields(part)
		if len(words) != 4 {
			return nil, fmt.Errorf("bad fixture %q", part)
		}
		predicate := "lives_in"
		if words[1] == "work" || words[1] == "works" {
			predicate = "works_at"
		}
		out = append(out, extract.Proposal{Subject: words[0], Predicate: predicate, Object: words[3], Quote: part,
			Statement: part + ".", Polarity: domain.PolarityAsserted, Tense: domain.TensePresent})
	}
	return out, nil
}

func speakerFormer(t *testing.T, h *harness) *formation.Former {
	t.Helper()
	v, err := pg.LoadVocabulary(context.Background(), h.pool, h.schema)
	if err != nil {
		t.Fatal(err)
	}
	return formation.NewFormer(pg.NewObservationStore(h.pool), pg.NewFactStore(h.pool), extract.NewWith(speakerModel{}, v))
}

func speakerObservation(t *testing.T, h *harness, subject, content string, at time.Time) domain.Observation {
	t.Helper()
	var receipt retryReceipt
	h.do(t, http.MethodPost, "/v1/observations", map[string]any{"data_subject_id": subject, "occurred_at": at,
		"messages": []map[string]any{{"role": "user", "content": content}}}, http.StatusCreated, &receipt)
	return domain.Observation{ID: receipt.ID, Scope: "p1", DataSubjectID: subject, OccurredAt: at}
}

type speakerBundle struct {
	Anchors []struct {
		Name string `json:"name"`
	} `json:"anchors"`
	Facts []struct {
		ID       string `json:"fact_id"`
		Object   string `json:"object"`
		Evidence struct {
			Quote         string `json:"quote"`
			ObservationID string `json:"observation_id"`
		} `json:"evidence"`
	} `json:"facts"`
}

func TestSpeakersStaySeparateThroughCorrectionTraversalExportAndErasure(t *testing.T) {
	h := newHarness(t, "api_speakers")
	ctx := context.Background()
	former := speakerFormer(t, h)
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	const alice = "opaque-alice-identity"
	const bob = "opaque-bob-identity"
	observations := []domain.Observation{
		speakerObservation(t, h, alice, "I live in Dublin and I work at Ensera.", start),
		speakerObservation(t, h, bob, "I live in Amman and I work at Ensera.", start),
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, o := range observations {
		wg.Add(1)
		go func(o domain.Observation) {
			defer wg.Done()
			r, e := former.Form(ctx, h.schema, o)
			if e == nil && r.FactsAsserted != 2 {
				e = fmt.Errorf("formed %+v", r)
			}
			results <- e
		}(o)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var speakers, companies, current int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT
  (SELECT count(*) FROM {schema}.entity WHERE identity_kind='speaker'),
  (SELECT count(*) FROM {schema}.entity WHERE normalized_name='ensera'),
  (SELECT count(*) FROM {schema}.fact WHERE predicate='lives_in' AND upper_inf(valid))`)).Scan(&speakers, &companies, &current); err != nil {
		t.Fatal(err)
	}
	if speakers != 2 || companies != 1 || current != 2 {
		t.Fatalf("speakers=%d companies=%d current residences=%d", speakers, companies, current)
	}
	var bobBefore, bobAfter string
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT to_jsonb(f)::text FROM {schema}.fact f WHERE data_subject_id=$1 AND predicate='lives_in'`), bob).Scan(&bobBefore); err != nil {
		t.Fatal(err)
	}
	correction := speakerObservation(t, h, alice, "I live in Paris.", start.Add(24*time.Hour))
	if _, err := former.Form(ctx, h.schema, correction); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT to_jsonb(f)::text FROM {schema}.fact f WHERE data_subject_id=$1 AND predicate='lives_in'`), bob).Scan(&bobAfter); err != nil {
		t.Fatal(err)
	}
	if bobBefore != bobAfter {
		t.Fatal("Alice's correction changed Bob's fact")
	}
	for _, tc := range []struct{ subject, city, forbidden string }{{alice, "Paris", "Amman"}, {bob, "Amman", "Paris"}} {
		status, wire := h.raw(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Where do I live and work?", "data_subject_id": tc.subject, "hops": 2}, h.token)
		if status != 200 {
			t.Fatalf("recall %d: %s", status, wire)
		}
		var bundle speakerBundle
		if err := json.Unmarshal(wire, &bundle); err != nil {
			t.Fatal(err)
		}
		if len(bundle.Facts) != 2 || !strings.Contains(string(wire), tc.city) || strings.Contains(string(wire), tc.forbidden) {
			t.Fatalf("incorrect speaker traversal: %s", wire)
		}
		if strings.Contains(string(wire), alice) || strings.Contains(string(wire), bob) {
			t.Fatal("protected identifier leaked into displayed recall")
		}
	}
	// Selecting no attribution must not turn an ambiguous pronoun into everybody's identity.
	var unscoped speakerBundle
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Where do I live?"}, 200, &unscoped)
	if len(unscoped.Facts) != 0 || len(unscoped.Anchors) != 0 {
		t.Fatal("unscoped first-person question anchored all speakers")
	}
	var unknown speakerBundle
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Ensera", "data_subject_id": "unknown"}, 200, &unknown)
	if len(unknown.Facts) != 0 || len(unknown.Anchors) != 0 {
		t.Fatal("unknown subject widened the read")
	}
	exported, err := pg.NewExporter(h.pool, h.schema).Export(ctx, h.schema, "p1", alice)
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(exported)
	if strings.Contains(string(wire), bob) || strings.Contains(string(wire), "Amman") {
		t.Fatal("Alice export includes Bob")
	}
	receipt, err := pg.NewEraser(h.pool).Erase(ctx, h.schema, "p1", alice, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	for kind, residual := range receipt.Residual {
		if residual != 0 {
			t.Fatalf("residual %s=%d", kind, residual)
		}
	}
	var after speakerBundle
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "Where do I work?", "data_subject_id": bob, "hops": 2}, 200, &after)
	if len(after.Facts) != 2 {
		t.Fatalf("erasing Alice removed Bob's facts: %+v", after)
	}
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT count(*) FROM {schema}.entity WHERE normalized_name='ensera'`)).Scan(&companies); err != nil || companies != 1 {
		t.Fatalf("shared company lost: %d %v", companies, err)
	}
}

func TestFormationRefusesUnboundAndReportedSpeakersAndBindsArabic(t *testing.T) {
	h := newHarness(t, "api_speaker_policy")
	ctx := context.Background()
	former := speakerFormer(t, h)
	for _, tc := range []struct {
		subject, content string
		facts, rejected  int
	}{
		{"", "I live in Dublin.", 0, 1}, {"alice", "John said: I live in London.", 0, 1},
		{"arabic-subject", "أنا أسكن في عمان.", 1, 0}, {"alice", "Marta works at Ensera.", 1, 0},
	} {
		o := speakerObservation(t, h, tc.subject, tc.content, time.Now().UTC())
		r, err := former.Form(ctx, h.schema, o)
		if err != nil || r.FactsAsserted != tc.facts || r.ClaimsRejected != tc.rejected {
			t.Fatalf("%q: %+v %v", tc.content, r, err)
		}
	}
	var result speakerBundle
	h.do(t, http.MethodPost, "/v1/recalls", map[string]any{"question": "أين أسكن؟", "data_subject_id": "arabic-subject"}, 200, &result)
	if len(result.Facts) != 1 || result.Facts[0].Evidence.Quote != "أنا أسكن في عمان." {
		t.Fatalf("Arabic binding or quote lost: %+v", result)
	}
	var named int
	if err := h.pool.QueryRow(ctx, h.schema.SQL(`SELECT count(*) FROM {schema}.entity WHERE normalized_name='marta' AND identity_kind='named' AND speaker_subject_id IS NULL`)).Scan(&named); err != nil || named != 1 {
		t.Fatalf("named person was rebound: %d %v", named, err)
	}
}

func TestSpeakerBindingChecksTheStoredSourceRatherThanCallerArguments(t *testing.T) {
	h := newHarness(t, "api_speaker_source")
	ctx := context.Background()
	store := pg.NewObservationStore(h.pool)
	facts := pg.NewFactStore(h.pool)
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAssistant, domain.RoleTool} {
		o, err := store.Append(ctx, h.schema, domain.Turn{Scope: "p1", DataSubjectID: "alice", Messages: []domain.Message{{Role: role, Content: "I live in Dublin."}}})
		if err != nil {
			t.Fatal(err)
		}
		claim := domain.Claim{Subject: "I", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "I live in Dublin.", Quote: "I live in Dublin", ByteEnd: 16}
		for _, tc := range []struct{ project, subject string }{{"p1", "bob"}, {"p2", "alice"}, {"p1", ""}, {"p1", "alice"}} {
			_, err := facts.Assert(ctx, h.schema, tc.project, o.ID, domain.RoleUser, tc.subject, claim)
			allowed := role == domain.RoleUser && tc.project == "p1" && tc.subject == "alice"
			if allowed && err != nil {
				t.Fatal(err)
			}
			if !allowed && !errors.Is(err, pg.ErrUnboundSpeaker) {
				t.Fatalf("forged binding accepted: role=%s %+v: %v", role, tc, err)
			}
		}
	}
}

func TestReportMaterialKeepsOpaqueSpeakerReferencesAndSharedOrganizationIdentity(t *testing.T) {
	h := newHarness(t, "api_speaker_material")
	ctx := context.Background()
	former := speakerFormer(t, h)
	for _, subject := range []string{"protected-subject-a", "protected-subject-b"} {
		o := speakerObservation(t, h, subject, "I work at Ensera.", time.Now().UTC())
		if _, err := former.Form(ctx, h.schema, o); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	rows, err := h.pool.Query(ctx, h.schema.SQL(`SELECT entity_id::text FROM {schema}.entity`))
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	_, facts, err := pg.NewCommunityStore(h.pool, h.schema).Material(ctx, "p1", ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 || facts[0].SubjectID == facts[1].SubjectID || facts[0].ObjectID != facts[1].ObjectID || facts[0].SubjectID == "" || facts[0].ObjectID == "" {
		t.Fatalf("identity lost in material: %+v", facts)
	}
	wire, _ := json.Marshal(facts)
	if strings.Contains(string(wire), "protected-subject") {
		t.Fatal("report input contains protected attribution keys")
	}
}
