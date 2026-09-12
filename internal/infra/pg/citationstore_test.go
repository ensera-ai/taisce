// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestCitationPreservesExactMessageBytesAndSurvivesSupersession(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "cite_exact")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	at := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	quote := "أسكن في دبلن"
	content := "مقدمة: " + quote + ". نهاية."
	start := strings.Index(content, quote)
	stored, err := obs.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleAssistant, Content: "An unrelated response."}, domain.Message{Ordinal: 1, Role: domain.RoleUser, Content: content, OccurredAt: at}))
	if err != nil {
		t.Fatal(err)
	}
	id, err := facts.Assert(ctx, schema, "p1", stored.ID, domain.RoleUser, "subject-1", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "The user lives in Dublin.", Confidence: 0.9, Quote: quote, ByteStart: start, ByteEnd: start + len(quote), SourceOrdinal: 1})
	if err != nil {
		t.Fatal(err)
	}
	resolver := pg.NewCitationStore(pool, schema)
	got, err := resolver.Resolve(ctx, "p1", id, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.Status != "current" || len(got.Evidence) != 1 || got.Next != nil {
		t.Fatalf("citation: %+v", got)
	}
	source := got.Evidence[0]
	if source.Quote != quote || source.Ordinal != 1 || source.ByteStart != start || source.ByteEnd != start+len(quote) || source.Role != "user" || !source.OccurredAt.Equal(at) || source.LogOffset != stored.LogOffset || source.Context != content || source.ContextStart != 0 || source.ContextEnd != len(content) || !source.ContextComplete {
		t.Fatalf("source: %+v", source)
	}
	for _, scope := range []string{"p2"} {
		if _, err := resolver.Resolve(ctx, scope, id, nil, 8); !errors.Is(err, pg.ErrCitationNotFound) {
			t.Fatalf("foreign project: %v", err)
		}
	}
	statedAt(t, ctx, obs, facts, schema, at.AddDate(0, 3, 0), "I now live in Amman.", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Amman", Statement: "The user lives in Amman."})
	got, err = resolver.Resolve(ctx, "p1", id, nil, 8)
	if err != nil || got.Status != "validity_closed" || got.Valid.Until == nil || got.Evidence[0].Quote != quote {
		t.Fatalf("saved citation after supersession: %+v, %v", got, err)
	}
	if _, err := pg.NewEraser(pool).Erase(ctx, schema, "p1", "subject-1", "requested"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(ctx, "p1", id, nil, 8); !errors.Is(err, pg.ErrCitationNotFound) {
		t.Fatalf("erased citation: %v", err)
	}
}

func TestCitationPaginationIsBoundedAndDoesNotLoseSupportingSpans(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "cite_pages")
	obs, facts := pg.NewObservationStore(pool), pg.NewFactStore(pool)
	content := strings.Repeat("x", 60000)
	id := statedAt(t, ctx, obs, facts, schema, time.Now().UTC(), content, domain.Claim{Subject: "user", Predicate: "prefers", Cardinality: domain.CardinalityMany, Object: "reading", Statement: "The user likes reading."})
	// Distinct observations support the same fact. Each complete quote and context consumes 120KB.
	for i := 0; i < 4; i++ {
		source, err := obs.Append(ctx, schema, turnOf(domain.Message{Ordinal: 0, Role: domain.RoleUser, Content: content}))
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact_evidence(scope,fact_id,source_observation_id,source_ordinal,quote,byte_start,byte_end,extractor_version) VALUES('p1',$1,$2,0,$3,0,$4,'test')`), id, source.ID, content, len(content))
		if err != nil {
			t.Fatal(err)
		}
	}
	resolver := pg.NewCitationStore(pool, schema)
	for _, limit := range []int{1, 32} {
		seen := map[string]bool{}
		var cursor *pg.CitationCursor
		for page := 0; page < 6; page++ {
			got, err := resolver.Resolve(ctx, "p1", id, cursor, limit)
			if err != nil {
				t.Fatal(err)
			}
			size := len(got.Statement)
			for _, s := range got.Evidence {
				if seen[s.ObservationID] {
					t.Fatal("duplicate support across pages")
				}
				seen[s.ObservationID] = true
				size += len(s.Quote) + len(s.Context) + len(s.ExtractorVersion)
			}
			if size > pg.MaxCitationTextBytes || len(got.Evidence) > limit {
				t.Fatalf("unbounded page: %d bytes, %d rows", size, len(got.Evidence))
			}
			cursor = got.Next
			if cursor == nil {
				break
			}
		}
		if len(seen) != 5 || cursor != nil {
			t.Fatalf("lost evidence: %d sources, cursor %+v", len(seen), cursor)
		}
	}
}

func TestCitationRefusesInvalidPagesAndCorruptOrOversizedSources(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "cite_refusal")
	id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "I live in Dublin.", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "Dublin resident."})
	resolver := pg.NewCitationStore(pool, schema)
	for _, tc := range []struct {
		id, scope string
		limit     int
		cursor    *pg.CitationCursor
	}{
		{"bad", "p1", 8, nil}, {id, "bad scope", 8, nil}, {id, "p1", 0, nil}, {id, "p1", 33, nil}, {id, "p1", 8, &pg.CitationCursor{ObservationID: "bad"}}, {id, "p1", 8, &pg.CitationCursor{ObservationID: uuid.NewString(), Ordinal: -1}},
	} {
		if _, err := resolver.Resolve(ctx, tc.scope, tc.id, tc.cursor, tc.limit); !errors.Is(err, pg.ErrInvalidCitation) {
			t.Fatalf("invalid page: %v", err)
		}
	}
	if _, err := resolver.Resolve(ctx, "p1", uuid.NewString(), nil, 8); !errors.Is(err, pg.ErrCitationNotFound) {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, sql string
		want      error
	}{
		{"mismatch", `UPDATE {schema}.fact_evidence SET quote='forged'`, pg.ErrCitationIntegrity},
		{"quote limit", `UPDATE {schema}.fact_evidence SET quote=repeat('x',65537)`, pg.ErrCitationLimit},
		{"message limit", `UPDATE {schema}.turn_message SET content=repeat('x',65537)`, pg.ErrCitationLimit},
		{"version limit", `UPDATE {schema}.fact_evidence SET extractor_version=repeat('x',257)`, pg.ErrCitationLimit},
		{"statement limit", `UPDATE {schema}.fact SET statement=repeat('x',65537)`, pg.ErrCitationLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Restore each component before applying one independent corruption.
			for _, sql := range []string{`UPDATE {schema}.fact SET statement='Dublin resident.'`, `UPDATE {schema}.turn_message SET content='I live in Dublin.'`, `UPDATE {schema}.fact_evidence SET quote='I live in Dublin.',extractor_version='test'`, tc.sql} {
				if _, err := pool.Exec(ctx, schema.SQL(sql)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := resolver.Resolve(ctx, "p1", id, nil, 8)
			if !errors.Is(err, tc.want) || got.ID != "" || len(got.Evidence) != 0 {
				t.Fatalf("returned partial/corrupt citation: %+v %v", got, err)
			}
		})
	}
}

func TestCitationRejectsBrokenMessageLinksAndUTF8Spans(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "cite_broken")
	content := "أسكن هنا"
	id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), content, domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "Dublin resident."})
	resolver := pg.NewCitationStore(pool, schema)
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.fact_evidence SET byte_start=1`)); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(ctx, "p1", id, nil, 8); !errors.Is(err, pg.ErrCitationIntegrity) {
		t.Fatalf("mid-codepoint span: %v", err)
	}
	// An owner can defeat constraints; resolving damaged provenance must still fail closed.
	for _, sql := range []string{`ALTER TABLE {schema}.fact_evidence DROP CONSTRAINT evidence_message_fk`, `UPDATE {schema}.fact_evidence SET byte_start=0,source_ordinal=42`} {
		if _, err := pool.Exec(ctx, schema.SQL(sql)); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := resolver.Resolve(ctx, "p1", id, nil, 8); !errors.Is(err, pg.ErrCitationIntegrity) || got.ID != "" {
		t.Fatalf("missing message: %+v %v", got, err)
	}
}

func TestCitationCursorRetainsMultipleSpansFromOneMessage(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "cite_spans")
	id := statedAt(t, ctx, pg.NewObservationStore(pool), pg.NewFactStore(pool), schema, time.Now().UTC(), "Dublin and Dublin", domain.Claim{Subject: "user", Predicate: "lives_in", Cardinality: domain.CardinalityOne, Object: "Dublin", Statement: "Dublin resident."})
	if _, err := pool.Exec(ctx, schema.SQL(`UPDATE {schema}.fact_evidence SET quote='Dublin',byte_end=6`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact_evidence(scope,fact_id,source_observation_id,source_ordinal,quote,byte_start,byte_end,extractor_version) SELECT scope,fact_id,source_observation_id,source_ordinal,'Dublin',11,17,extractor_version FROM {schema}.fact_evidence`)); err != nil {
		t.Fatal(err)
	}
	resolver := pg.NewCitationStore(pool, schema)
	first, err := resolver.Resolve(ctx, "p1", id, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Next == nil || len(first.Evidence) != 1 || first.Evidence[0].ByteStart != 0 {
		t.Fatalf("first page: %+v", first)
	}
	second, err := resolver.Resolve(ctx, "p1", id, first.Next, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.Next != nil || len(second.Evidence) != 1 || second.Evidence[0].ByteStart != 11 {
		t.Fatalf("second page: %+v", second)
	}
}
