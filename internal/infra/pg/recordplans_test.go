// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestRecordPagesSeekIndexedPositionsAndHistoryPagesDoNotRepeat(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "record_plans")
	source, err := pg.NewObservationStore(pool).Append(ctx, schema, turnOf(domain.Message{Role: domain.RoleUser, Content: "Controlled plan fixture"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact(fact_id,scope,predicate,statement,cardinality,valid,source_role)
 SELECT md5('record '||n)::uuid,'p1','works_at','plan fixture','many','[2020-01-01,)'::tstzrange,'user' FROM generate_series(1,10000) n`)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.projection_dependency(source_observation_id,scope,projection_kind,projection_id,data_subject_id)
 SELECT $1,'p1','fact',fact_id::text,CASE WHEN get_byte(uuid_send(fact_id),0)%20=0 THEN 'subject-1' ELSE 'other-subject' END FROM {schema}.fact`), source.ID); err != nil {
		t.Fatal(err)
	}
	var factID string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT fact_id::text FROM {schema}.fact ORDER BY fact_id LIMIT 1`)).Scan(&factID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.fact_history(history_id,scope,fact_id,valid,known,source_observation_id)
 SELECT gen_random_uuid(),'p1',$1,'[2020-01-01,)'::tstzrange,tstzrange('2010-01-01'::timestamptz+n*interval '1 day','2010-01-02'::timestamptz+n*interval '1 day'),$2
 FROM generate_series(0,1999) n`), factID, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema.SQL(`ANALYZE {schema}.fact; ANALYZE {schema}.projection_dependency; ANALYZE {schema}.fact_history`)); err != nil {
		t.Fatal(err)
	}
	var middle string
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT fact_id::text FROM {schema}.fact ORDER BY fact_id OFFSET 9000 LIMIT 1`)).Scan(&middle); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query, index string
		args               []any
	}{
		{"project", strings.ReplaceAll(pg.RecordListQueryForTesting, "{comparison}", ">"), "fact_scope_id_uniq", []any{"p1", middle, 21, pg.RecordPreviewCharacters}},
		{"subject", strings.ReplaceAll(pg.SubjectRecordListQueryForTesting, "{comparison}", ">"), "dependency_subject_fact_page_idx", []any{"p1", middle, 21, pg.RecordPreviewCharacters, "subject-1"}},
		{"history", strings.ReplaceAll(pg.RecordHistoryQueryForTesting, "{cursor}", "AND (upper(known),history_id)<($4::timestamptz,$5::uuid)"), "fact_history_page_idx", []any{"p1", factID, 21, time.Date(2011, 1, 1, 0, 0, 0, 0, time.UTC), uuid.Nil.String()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw json.RawMessage
			if err := pool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+schema.SQL(tc.query), tc.args...).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s plan=%s", tc.name, raw)
			var plans []struct{ Plan anchorPlanNode }
			if err := json.Unmarshal(raw, &plans); err != nil {
				t.Fatal(err)
			}
			found := false
			var inspect func(anchorPlanNode)
			inspect = func(n anchorPlanNode) {
				if n.Index == tc.index {
					found = true
				}
				if n.NodeType == "Seq Scan" && (n.Relation == "fact" || n.Relation == "fact_history" || n.Relation == "projection_dependency") {
					t.Fatal("page scanned a complete relation")
				}
				for _, child := range n.Plans {
					inspect(child)
				}
			}
			inspect(plans[0].Plan)
			if !found {
				t.Fatalf("missing expected seek index %s", tc.index)
			}
		})
	}
	store := pg.NewRecordStore(pool, schema)
	seen := map[string]bool{}
	var cursor *pg.RecordHistoryCursor
	var until *time.Time
	for page := 0; page < 21; page++ {
		out, err := store.History(ctx, "p1", factID, cursor, pg.MaxRecordPage)
		if err != nil || len(out.Previous) > pg.MaxRecordPage {
			t.Fatalf("history page: %v", err)
		}
		for _, v := range out.Previous {
			if seen[v.ID] || (until != nil && !v.Known.Until.Before(*until)) || v.EndedByObservationID != source.ID {
				t.Fatal("duplicate, unordered or unattributed history")
			}
			seen[v.ID] = true
			until = v.Known.Until
		}
		cursor = out.Next
		if cursor == nil {
			break
		}
	}
	if len(seen) != 2000 || cursor != nil {
		t.Fatalf("lost history: %d", len(seen))
	}
}
