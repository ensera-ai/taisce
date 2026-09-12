// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package formation

import (
	"context"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/notify"
)

// Notifications is the pass that tells a project its memory moved, and the loop that keeps trying.
//
// # Why it is a pass beside formation rather than part of it
//
// Formation's job is to turn stored turns into memory, and it must not be able to fail because
// somebody's webhook is down. So this reads the watermark formation left behind, writes down what is
// owed, and sends on its own schedule. The worst a broken destination can do is fill a queue that an
// operator can read.
type Notifications struct {
	store    NotificationStore
	sender   *notify.Sender
	budget   int
	baseWait time.Duration
}

// NotificationStore is what this pass needs of the store, and no more. Named here rather than taken
// as a concrete type so the pass can be exercised against a store that fails on purpose.
type NotificationStore interface {
	Owed(ctx context.Context, scope string, formed, stored int64, parked int) (int, error)
	Claim(ctx context.Context) (pg.Due, bool, error)
	Delivered(ctx context.Context, id string, status int) error
	Failed(ctx context.Context, id string, attempts, budget, status int, reason string, backoff time.Duration) error
}

// DefaultAttemptBudget is how many times a delivery is tried before it is parked.
//
// Six, with doubling from five seconds, is about five minutes of trying. Longer would keep a queue
// full of news that has stopped being news — the receiver reads the watermark and catches up in one
// call — and shorter would park a destination that restarted.
const DefaultAttemptBudget = 6

// NewNotifications builds the pass.
func NewNotifications(store NotificationStore, sender *notify.Sender) *Notifications {
	return &Notifications{store: store, sender: sender, budget: DefaultAttemptBudget, baseWait: 5 * time.Second}
}

// WithBudget sets the attempt budget and the first wait, for a test that cannot spend five minutes.
func (n *Notifications) WithBudget(budget int, baseWait time.Duration) *Notifications {
	n.budget, n.baseWait = budget, baseWait
	return n
}

// Owe records what a scope's watermark says is worth telling. Cheap and idempotent: the row it would
// write is usually already there, because most passes have no news.
func (n *Notifications) Owe(ctx context.Context, scope string, formed, stored int64, parked int) (int, error) {
	if n == nil || n.sender == nil || !n.sender.Enabled() {
		return 0, nil
	}
	return n.store.Owed(ctx, scope, formed, stored, parked)
}

// Deliver attempts up to `limit` due deliveries and reports how many landed.
//
// Bounded per pass because this shares a worker with formation, and a queue of a thousand failing
// destinations must not be able to stop memory being formed.
func (n *Notifications) Deliver(ctx context.Context, limit int) (delivered int, attempted int, err error) {
	if n == nil || n.sender == nil || !n.sender.Enabled() {
		return 0, 0, nil
	}
	for i := 0; i < limit; i++ {
		if ctx.Err() != nil {
			return delivered, attempted, ctx.Err()
		}
		due, found, err := n.store.Claim(ctx)
		if err != nil {
			return delivered, attempted, err
		}
		if !found {
			return delivered, attempted, nil
		}
		attempted++
		result := n.sender.Send(ctx, notify.Delivery{
			ID: due.ID, Scope: due.Scope, URL: due.URL, Secret: due.Secret,
			FormedThrough: due.FormedThrough, StoredThrough: due.StoredThrough, ParkedTurns: due.ParkedTurns,
		})
		switch {
		case result.Err == nil:
			if err := n.store.Delivered(ctx, due.ID, result.Status); err != nil {
				return delivered, attempted, err
			}
			delivered++
		case !result.Retryable:
			// Final on the first refusal. Trying again would be asking the same question.
			if err := n.store.Failed(ctx, due.ID, n.budget, n.budget, result.Status, result.Reason(), 0); err != nil {
				return delivered, attempted, err
			}
		default:
			if err := n.store.Failed(ctx, due.ID, due.Attempts, n.budget, result.Status,
				result.Reason(), n.wait(due.Attempts)); err != nil {
				return delivered, attempted, err
			}
		}
	}
	return delivered, attempted, nil
}

// wait doubles with each attempt, because the failure that matters most is the one that is not this
// destination's fault: a receiver restarting fails every delivery at once, and a flat retry would
// spend the whole queue's budget inside a minute.
func (n *Notifications) wait(attempts int) time.Duration {
	wait := n.baseWait
	for i := 1; i < attempts && i < 8; i++ {
		wait *= 2
	}
	return wait
}
