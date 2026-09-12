// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestMessageChunkIdentityUpgradePreservesIDsAndRefusesAmbiguity(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	all, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var prior []Script
	for _, s := range all {
		if s.Version < 44 {
			prior = append(prior, s)
		}
	}
	for _, ambiguous := range []bool{false, true} {
		name := "chunk_identity_upgrade"
		if ambiguous {
			name += "_ambiguous"
		}
		schema, _ := NewSchema(name)
		if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE; CREATE SCHEMA "+name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { pool.Exec(ctx, "DROP SCHEMA "+name+" CASCADE") })
		if _, err := apply(ctx, pool, schema, prior); err != nil {
			t.Fatal(err)
		}
		source, id := uuid.NewString(), uuid.NewString()
		if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES('p1','fixture');
    INSERT INTO {schema}.observation(observation_id,scope,log_offset,kind,occurred_at,ingested_at,source_role) VALUES('`+source+`','p1',0,'turn',now(),now(),'user');
    INSERT INTO {schema}.turn_message(observation_id,ordinal,role,content,group_ordinal,occurred_at) VALUES('`+source+`',0,'user','present',0,now()),('`+source+`',1,'user','missing',0,now());
    INSERT INTO {schema}.chunk(chunk_id,scope,source_observation_id,source_message_ordinal,text,occurred_at,source_role) VALUES('`+id+`','p1','`+source+`',0,'present',now(),'user')`)); err != nil {
			t.Fatal(err)
		}
		if ambiguous {
			if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.chunk(chunk_id,scope,source_observation_id,source_message_ordinal,text,occurred_at,source_role) VALUES(gen_random_uuid(),'p1',$1,0,'ambiguous',now(),'user')`), source); err != nil {
				t.Fatal(err)
			}
		}
		_, err := Apply(ctx, pool, schema)
		if ambiguous {
			if err == nil {
				t.Fatal("ambiguous identity accepted")
			}
			var version int
			if err := pool.QueryRow(ctx, schema.SQL(`SELECT max(version) FROM {schema}.schema_migration`)).Scan(&version); err != nil || version != 43 {
				t.Fatalf("partial migration %d %v", version, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var retained, assigned string
		if err := pool.QueryRow(ctx, schema.SQL(`SELECT chunk_id::text FROM {schema}.turn_message WHERE ordinal=0`)).Scan(&retained); err != nil || retained != id {
			t.Fatalf("original identity changed %s %v", retained, err)
		}
		if err := pool.QueryRow(ctx, schema.SQL(`SELECT chunk_id::text FROM {schema}.turn_message WHERE ordinal=1`)).Scan(&assigned); err != nil {
			t.Fatal(err)
		}
		if parsed, err := uuid.Parse(assigned); err != nil || parsed == uuid.Nil || assigned == id {
			t.Fatal("missing message has no distinct identity")
		}
		if applied, err := Apply(ctx, pool, schema); err != nil || len(applied) != 0 {
			t.Fatalf("restart reapplied %v %v", applied, err)
		}
	}
}
