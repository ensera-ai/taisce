// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestADeliveryCannotBeFiledUnderAProjectItsEndpointIsNotIn holds the one row that leaves the
// instance to the project of the address it is sent to.
//
// A delivery is posted to its endpoint's address. Filed under one project and pointed at another
// project's endpoint, it would tell that endpoint's owner about somebody else's formation. The
// statement that writes deliveries takes the project and the endpoint from one row, so it never
// has — but that was the statement's doing. Here the row is written by hand, the way a statement
// written later might, and the database has to be what refuses it.
func TestADeliveryCannotBeFiledUnderAProjectItsEndpointIsNotIn(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := tenant(t, pool, "delivery_project")
	defer pool.Exec(ctx, "DROP SCHEMA "+schema.String()+" CASCADE")
	if _, err := pool.Exec(ctx, schema.SQL(`INSERT INTO {schema}.project(scope,label) VALUES('p2','p2')`)); err != nil {
		t.Fatal(err)
	}
	var endpoint string
	if err := pool.QueryRow(ctx, schema.SQL(`
		INSERT INTO {schema}.notification_endpoint (endpoint_id, scope, url, secret)
		VALUES (gen_random_uuid(), 'p1', 'https://hooks.example.test/p1', decode(repeat('ab', 32), 'hex'))
		RETURNING endpoint_id::text`)).Scan(&endpoint); err != nil {
		t.Fatal(err)
	}
	file := func(project string) error {
		_, err := pool.Exec(ctx, schema.SQL(`
			INSERT INTO {schema}.notification_delivery (delivery_id, scope, endpoint_id, formed_through, stored_through)
			VALUES (gen_random_uuid(), $1, $2::uuid, 1, 1)`), project, endpoint)
		return err
	}

	err := file("p2")
	var refused *pgconn.PgError
	if !errors.As(err, &refused) || refused.Code != "23503" || refused.ConstraintName != "notification_delivery_endpoint_in_project_fk" {
		t.Fatalf("a delivery under another project than its endpoint's must be refused by the key that links them, got %v", err)
	}
	if err := file("p1"); err != nil {
		t.Fatalf("a delivery in its endpoint's project was refused: %v", err)
	}
	// The link still removes a delivery with its endpoint, as the one it replaces did.
	if _, err := pool.Exec(ctx, schema.SQL(`DELETE FROM {schema}.notification_endpoint WHERE scope='p1' AND endpoint_id=$1::uuid`), endpoint); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx, schema.SQL(`SELECT count(*) FROM {schema}.notification_delivery WHERE scope='p1'`)).Scan(&left); err != nil || left != 0 {
		t.Fatalf("removing an endpoint left %d of its deliveries (%v)", left, err)
	}
}
