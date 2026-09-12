// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestGenerationIndexIsDirectlyOwnedByItsChildPartition(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "embedding_partition_catalog")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	store := pg.NewMessageEmbeddingStore(pool, schema)
	model := pg.EmbeddingModel{Name: "catalog-test", Revision: "v1",
		EndpointHash: strings.Repeat("a", 64), Dimensions: 2560}
	generation, err := store.Start(ctx, "p1", uuid.NewString(), uuid.NewString(), model)
	if err != nil {
		t.Fatal(err)
	}

	var tableAttachments, parentHNSW, childHNSW, indexAttachments int
	err = pool.QueryRow(ctx, `
SELECT
 (SELECT count(*) FROM pg_inherits inheritance
   JOIN pg_class parent ON parent.oid=inheritance.inhparent
   JOIN pg_namespace pn ON pn.oid=parent.relnamespace
   JOIN pg_class child ON child.oid=inheritance.inhrelid
   JOIN pg_namespace cn ON cn.oid=child.relnamespace
  WHERE pn.nspname=$1 AND cn.nspname=$1 AND parent.relname='message_embedding'
    AND child.relname=$2 AND parent.relkind='p' AND child.relkind='r'),
 (SELECT count(*) FROM pg_index indexed
   JOIN pg_class parent ON parent.oid=indexed.indrelid
   JOIN pg_namespace pn ON pn.oid=parent.relnamespace
   JOIN pg_class idx ON idx.oid=indexed.indexrelid
   JOIN pg_am access_method ON access_method.oid=idx.relam
  WHERE pn.nspname=$1 AND parent.relname='message_embedding' AND access_method.amname='hnsw'),
 (SELECT count(*) FROM pg_index indexed
   JOIN pg_class child ON child.oid=indexed.indrelid
   JOIN pg_namespace cn ON cn.oid=child.relnamespace
   JOIN pg_class idx ON idx.oid=indexed.indexrelid
   JOIN pg_am access_method ON access_method.oid=idx.relam
  WHERE cn.nspname=$1 AND child.relname=$2 AND access_method.amname='hnsw'),
 (SELECT count(*) FROM pg_inherits inheritance
   JOIN pg_class child_index ON child_index.oid=inheritance.inhrelid
   JOIN pg_namespace cn ON cn.oid=child_index.relnamespace
  WHERE cn.nspname=$1 AND child_index.relname=$3)`, schema.String(),
		"message_vector_"+strings.ReplaceAll(generation.ID, "-", ""),
		"message_vector_"+strings.ReplaceAll(generation.ID, "-", "")+"_ann").Scan(
		&tableAttachments, &parentHNSW, &childHNSW, &indexAttachments)
	if err != nil {
		t.Fatal(err)
	}
	if tableAttachments != 1 || parentHNSW != 0 || childHNSW != 1 || indexAttachments != 0 {
		t.Fatalf("catalog table_attachment=%d parent_hnsw=%d child_hnsw=%d index_attachment=%d",
			tableAttachments, parentHNSW, childHNSW, indexAttachments)
	}
}
