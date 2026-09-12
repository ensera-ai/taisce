// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/notify"
)

// ── The notification pass ─────────────────────────────────────────────────────────────────────
//
// A deployment with no permitted destination does nothing at all, and a store that fails stops the
// pass rather than being swallowed — a delivery whose failure nobody hears about is the shape of
// promise this whole mechanism exists to avoid making.
func TestThePassDoesNothingWithoutADestinationAndStopsWhenTheStoreFails(t *testing.T) {
	ctx := context.Background()

	// No destinations named: not one query is made.
	silent := &countingStore{}
	off := formation.NewNotifications(silent, notify.NewSender(notify.Permit("", false), nil))
	if owed, err := off.Owe(ctx, "p1", 4, 5, 0); err != nil || owed != 0 {
		t.Fatalf("an unconfigured deployment recorded something owed: %d %v", owed, err)
	}
	if delivered, attempted, err := off.Deliver(ctx, 4); err != nil || delivered != 0 || attempted != 0 {
		t.Fatalf("an unconfigured deployment attempted a delivery: %d %d %v", delivered, attempted, err)
	}
	if silent.owed+silent.claims != 0 {
		t.Fatalf("an unconfigured deployment queried the store %d times", silent.owed+silent.claims)
	}

	// A store that cannot say what is due stops the pass and says why.
	broken := &countingStore{claimErr: errors.New("the database is unavailable")}
	on := formation.NewNotifications(broken, notify.NewSender(notify.Permit("hooks.example.com", true), nil))
	if _, _, err := on.Deliver(ctx, 4); err == nil {
		t.Fatal("a store failure was swallowed")
	}

	// Nothing due is not a failure, and does not spend the pass's budget looking.
	empty := &countingStore{}
	quiet := formation.NewNotifications(empty, notify.NewSender(notify.Permit("hooks.example.com", true), nil))
	if delivered, attempted, err := quiet.Deliver(ctx, 8); err != nil || delivered != 0 || attempted != 0 {
		t.Fatalf("an empty queue was not quiet: %d %d %v", delivered, attempted, err)
	}
	if empty.claims != 1 {
		t.Fatalf("an empty queue was asked %d times in one pass", empty.claims)
	}
}

// countingStore answers nothing and counts what it was asked.
type countingStore struct {
	owed     int
	claims   int
	claimErr error
}

func (c *countingStore) Owed(context.Context, string, int64, int64, int) (int, error) {
	c.owed++
	return 0, nil
}

func (c *countingStore) Claim(context.Context) (pg.Due, bool, error) {
	c.claims++
	return pg.Due{}, false, c.claimErr
}

func (c *countingStore) Delivered(context.Context, string, int) error { return nil }

func (c *countingStore) Failed(context.Context, string, int, int, int, string, time.Duration) error {
	return nil
}
