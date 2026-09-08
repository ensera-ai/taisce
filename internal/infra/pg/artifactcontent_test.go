// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

func TestArtifactContentRequiresCanonicalBase64AndPreservesEmptyBytes(t *testing.T) {
	for _, raw := range []string{`null`, `[0,255]`, `123`, `"bad"`, `"Zh=="`, `"Zg==\n"`, `"`} {
		var value pg.ArtifactContent
		if err := value.UnmarshalJSON([]byte(raw)); err == nil {
			t.Fatalf("ambiguous bytes accepted: %q", raw)
		}
	}
	for _, raw := range []string{`""`, `"AP+A"`} {
		var value pg.ArtifactContent
		if err := json.Unmarshal([]byte(raw), &value); err != nil || value == nil {
			t.Fatal("valid byte sequence refused")
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != raw {
			t.Fatal("binary wire round trip changed")
		}
	}
	var value pg.ArtifactContent
	if err := value.UnmarshalJSON(nil); err == nil {
		t.Fatal("empty input accepted")
	}
	tooLarge := append([]byte{'"'}, bytes.Repeat([]byte{'A'}, (pg.MaxArtifactBytes/3+2)*4)...)
	tooLarge = append(tooLarge, '"')
	if err := value.UnmarshalJSON(tooLarge); err == nil {
		t.Fatal("oversized decoded content accepted")
	}
}
func TestArtifactSourcesCannotChangeOwnerOrEnterFormation(t *testing.T) {
	ctx := context.Background()
	pool, schema, store := artifactFixture(t, "artifact_source_guard")
	if _, err := store.Put(ctx, "p1", uuid.NewString(), artifactRequest()); err != nil {
		t.Fatal(err)
	}
	for _, set := range []string{"data_subject_id='other'", "scope='p2'", "kind='turn'", "source_role='user'", "formed_at=NULL", "retention_until=NULL"} {
		if _, err := pool.Exec(ctx, schema.SQL("UPDATE {schema}.observation SET "+set+" WHERE kind='artifact'")); err == nil {
			t.Fatalf("source mutation accepted: %s", set)
		}
	}
}
