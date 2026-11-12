// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// Reads a caller was entitled to are counted on the ledger, not listed there one by one.
//
// # What this is allowed to decide, and what it must not
//
// It decides how many ledger rows a run of freshness reads by one credential becomes. It does not
// decide whether a read is served, who may make one, or what the ledger says about anything that
// changes or discloses memory: every write, erasure, export, recall and refusal is still a row of
// its own, written before the caller is answered.
//
// # Why freshness, and only freshness
//
// The ledger exists so that an act is on the record. A freshness read discloses two offsets and a
// count about the caller's own project, to a caller already entitled to all of that project. It
// is also the one operation built to be polled: an agent asks whether memory has caught up, once a
// second, for as long as it runs. Listed one by one that is 86,400 rows a day that can never be
// removed, each its own insert and commit on a path that reads nothing else, in a table whose
// purpose is the acts that matter.
//
// So the record keeps what an operator needs from those rows and drops the rest. *That* a
// credential read freshness, and when it started, is written at once: the first read in a window
// is a row, as it always was, so a credential is seen on the ledger the moment it acts. *How many
// times* it went on reading in that window is one more row when the window closes, whose magnitude
// is the count.
//
// Nothing else is treated this way. A recall discloses memory, and is listed. A refusal is
// somebody trying something they may not, and is listed.
//
// # What it costs
//
//   - A process that dies loses the count of the window it was in: the first read is on the
//     ledger, the number that followed it is not. That is at most a window per credential, of an
//     operation that changed and disclosed nothing.
//   - The count is written when the window closes, so on the ledger it is stamped up to a window
//     later than the reads it counts.
//   - Each serving process counts for itself. A credential polling through two replicas is two
//     first-reads and two counts a window, not one.
//
// # Why this is bounded
//
// The windows are kept by credential, and only for credentials that resolved: a caller holding
// nothing never reaches the handler that calls this. So the map is no larger than the number of
// credentials the instance has issued that are polling, and an entry leaves when its window closes.

// entitledReadWindow is how long one credential's freshness reads are gathered into a count.
//
// A minute: long enough that a poller at one a second is two rows where it was sixty, short enough
// that the count an operator sees is never more than a minute behind and a crash never loses more
// than a minute of it.
const entitledReadWindow = time.Minute

// EntitledReads gathers the freshness reads of each credential into a first row and a count.
type EntitledReads struct {
	audit  *pg.AuditStore
	log    *slog.Logger
	window time.Duration

	mu   sync.Mutex
	open map[readKey]*readWindow
	// ended holds the counts of windows a later read replaced before Close reached them.
	ended []countedReads
}

type readKey struct{ credential, project string }

// countedReads is one window's count, waiting to be written.
type countedReads struct {
	key     readKey
	further int
}

type readWindow struct {
	ends time.Time
	// further is how many reads followed the one that opened the window.
	further int
}

// NewEntitledReads counts over the standard window. Something must call Run, or Close when the
// process stops: a window nobody closes is a count nobody writes.
func NewEntitledReads(audit *pg.AuditStore, log *slog.Logger) *EntitledReads {
	if log == nil {
		log = slog.Default()
	}
	return &EntitledReads{audit: audit, log: log, window: entitledReadWindow, open: map[readKey]*readWindow{}}
}

// noted records one freshness read and says whether it is the first of its window, which the
// caller writes to the ledger at once. Any other is counted here and written when the window
// closes.
//
// It takes a lock and touches a map. It does not write, which is the point: after the first, a
// read on this path costs the ledger nothing until the window closes.
func (e *EntitledReads) noted(grant credential.Grant, now time.Time) (first bool) {
	key := readKey{grant.CredentialID, grant.Project}
	e.mu.Lock()
	defer e.mu.Unlock()
	if w, ok := e.open[key]; ok && now.Before(w.ends) {
		w.further++
		return false
	}
	// No window, or one that ended and that Close has not reached yet. Its count is kept for
	// Close to write, not overwritten by the window that replaces it.
	if w, ok := e.open[key]; ok && w.further > 0 {
		e.ended = append(e.ended, countedReads{key, w.further})
	}
	e.open[key] = &readWindow{ends: now.Add(e.window)}
	return true
}

// Close writes the count of every window that had ended by asOf, and forgets it.
//
// A window with no further reads writes nothing: its one read is already a row. A count that
// cannot be written is logged and dropped rather than kept to try again — the ledger's own rule,
// that a failed append does not fail or delay the operation it describes, applied to a count.
func (e *EntitledReads) Close(ctx context.Context, asOf time.Time) {
	e.mu.Lock()
	due := e.ended
	e.ended = nil
	for key, w := range e.open {
		if asOf.Before(w.ends) {
			continue
		}
		if w.further > 0 {
			due = append(due, countedReads{key, w.further})
		}
		delete(e.open, key)
	}
	e.mu.Unlock()
	for _, c := range due {
		if err := e.audit.Append(ctx, domain.AuditEntry{
			Operation:     domain.AuditFreshness,
			Principal:     c.key.credential,
			PrincipalKind: domain.PrincipalCredential,
			Project:       c.key.project,
			Magnitude:     c.further,
			Outcome:       domain.OutcomeAllowed,
		}); err != nil {
			e.log.Error("the audit ledger did not record a count of freshness reads",
				"principal", c.key.credential, "reads", c.further, "error", err)
		}
	}
}

// Run closes windows as they end, until the context does, and then closes whatever is still open.
//
// The last step is what a rolling restart relies on: a process told to stop writes the counts it
// was holding, through a context of its own, since the one it was given has just ended.
func (e *EntitledReads) Run(ctx context.Context) {
	ticker := time.NewTicker(e.window / 4)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			e.Close(final, time.Now().Add(e.window))
			cancel()
			return
		case now := <-ticker.C:
			e.Close(ctx, now)
		}
	}
}
