// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"encoding/json"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/ensera-ai/taisce/internal/erasurerecord"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/google/uuid"
)

// An erasure leaves a trace in the process log, and `taisce erasure replay` applies again the
// erasures a restore undid.
//
// # What this file is allowed to decide, and what it must not
//
// It decides when a trace is written and what is done on reading one back: which erasures a
// restored database no longer remembers, and what each of them names in what the database now
// holds. It does not decide what an erasure removes or how its residual is counted — a replayed
// erasure is the store's own walk, with the store's own receipt — and it does not decide what a
// trace says, which is the erasurerecord package's.
//
// # The order a restore is recovered in
//
// Restore, replay, then serve. A replay erases by identifier, so one run after the instance has
// gone back to work erases what a person wrote since as well. That is the right answer for
// somebody who asked to be forgotten and never came back, and the wrong one for somebody who
// did — and nothing here can tell which. The receipt a replay writes is what makes it safe to run
// twice, not what makes it safe to run late.

// envErasureRecordKey holds the key the traces are digested with. It lives with the deployment's
// other secrets: in the database it would be restored along with what it protects, and in the log
// it would sit beside what it keys.
const envErasureRecordKey = "TAISCE_ERASURE_RECORD_KEY"

// erasureRecorder is the recorder a serving process hands its eraser, or nil when no key is set.
//
// No key is not an error. Refusing to erase because a trace could not be written would trade the
// act for the record of it. It is said once, loudly, at start: an operator who has not set the key
// learns from the log what a restore will do, and not from a restore.
func erasureRecorder(log *slog.Logger) (pg.ErasureRecorder, error) {
	value := strings.TrimSpace(os.Getenv(envErasureRecordKey))
	if value == "" {
		log.Warn("erasures leave no trace outside the database: a restore to a point before an erasure brings back what it removed, and nothing will remove it again",
			"set", envErasureRecordKey)
		return nil, nil
	}
	key, err := erasurerecord.ParseKey(value)
	if err != nil {
		// A key that is set and unusable stops the process. Starting without it would be the
		// unset case, silently, in a deployment whose operator believes they set one.
		return nil, fmt.Errorf("%s: %w", envErasureRecordKey, err)
	}
	return func(requestID, project, kind string, identifiers []string) error {
		for _, record := range key.Records(requestID, project, kind, identifiers) {
			log.Info("erasure recorded", "record", record.Line(),
				"request_id", requestID, "project", project, "part", record.Part, "parts", record.Parts)
		}
		return nil
	}, nil
}

// What became of one erasure a replay was given.
const (
	replayApplied     = "applied"
	replayWouldApply  = "would apply"
	replayAlreadyHeld = "already held"
	replayNothingHeld = "nothing held"
	replayRefused     = "refused"
)

type replayedErasure struct {
	RequestID string `json:"request_id"`
	Project   string `json:"project"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	Why       string `json:"why,omitempty"`
	// Matched is how many people or turns in this database the trace names. Counts only: the
	// command's output is read by an operator and kept in a terminal's history.
	Matched  int `json:"matched"`
	Deleted  int `json:"deleted"`
	Residual int `json:"residual"`
}

type replayResult struct {
	Records        int               `json:"records"`
	DryRun         bool              `json:"dry_run"`
	AuditPrincipal string            `json:"audit_principal,omitempty"`
	Erasures       []replayedErasure `json:"erasures"`
}

func erasureCommand(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	const usage = "usage: taisce erasure replay --records <file, or - for standard input> [--dry-run]"
	if len(args) == 0 || args[0] != "replay" {
		return errors.New(usage)
	}
	flags := flag.NewFlagSet("erasure replay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("records", "", "log lines holding erasure records")
	dry := flags.Bool("dry-run", false, "report what would be erased and erase nothing")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *path == "" {
		return errors.New(usage)
	}
	key, err := erasurerecord.ParseKey(strings.TrimSpace(os.Getenv(envErasureRecordKey)))
	if err != nil {
		return fmt.Errorf("%s: %w. A replay needs the key the records were written under", envErasureRecordKey, err)
	}
	lines := in
	if *path != "-" {
		file, err := os.Open(*path)
		if err != nil {
			return fmt.Errorf("open the records: %w", err)
		}
		defer file.Close()
		lines = file
	}
	records, err := erasurerecord.Read(lines)
	if err != nil {
		return err
	}
	erasures, err := erasurerecord.Gather(records)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	admin, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()

	result := replayResult{Records: len(records), DryRun: *dry, Erasures: []replayedErasure{}}
	if !*dry {
		result.AuditPrincipal = uuid.NewString()
	}
	eraser, audit := pg.NewEraser(admin), pg.NewAuditStore(admin, schema)
	failed := 0
	for _, erasure := range erasures {
		outcome := replayedErasure{RequestID: erasure.RequestID, Project: erasure.Project, Kind: erasure.Kind}
		refuse := func(why string) {
			outcome.State, outcome.Why = replayRefused, why
			failed++
		}
		switch held, err := eraser.Held(ctx, schema, erasure.RequestID); {
		case err != nil:
			return fmt.Errorf("look for the receipt of erasure %s: %w", erasure.RequestID, err)
		case held:
			outcome.State = replayAlreadyHeld
		case erasure.KeyID != key.ID():
			// Digests made under another key match nothing, and "nothing held" would be a lie.
			refuse("its records were written under a different key")
		case !erasure.Complete:
			refuse("part of its record is missing from the lines given")
		case requireProject(ctx, admin, erasure.Project) != nil:
			refuse("this database has no such project")
		default:
			held, err := eraser.Erasable(ctx, schema, erasure.Project, erasure.Kind)
			if err != nil {
				return err
			}
			var named []string
			for _, identifier := range held {
				if erasure.Digests[key.Digest(erasure.Project, erasure.Kind, identifier)] {
					named = append(named, identifier)
				}
			}
			outcome.Matched = len(named)
			switch {
			case len(named) == 0:
				// Erased before the backup was taken, or never here. Nothing to do and no receipt
				// to write: a receipt names what it erased, and a digest is not a name.
				outcome.State = replayNothingHeld
			case *dry:
				outcome.State = replayWouldApply
			default:
				receipts, err := reapply(ctx, eraser, schema, erasure, named)
				if err != nil {
					return fmt.Errorf("apply erasure %s again: %w", erasure.RequestID, err)
				}
				outcome.State = replayApplied
				for _, receipt := range receipts {
					for _, n := range receipt.Deleted {
						outcome.Deleted += n
					}
					for _, n := range receipt.Residual {
						outcome.Residual += n
					}
				}
				if outcome.Residual > 0 {
					failed++
				}
				if err := audit.Append(ctx, domain.AuditEntry{
					Operation: domain.AuditErase, Principal: result.AuditPrincipal, PrincipalKind: domain.PrincipalOperator,
					Project: erasure.Project, Magnitude: outcome.Deleted, Outcome: domain.OutcomeAllowed,
				}); err != nil {
					return fmt.Errorf("erasure %s was applied again and the ledger did not record it: %w", erasure.RequestID, err)
				}
			}
		}
		result.Erasures = append(result.Erasures, outcome)
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d erasures were refused or left a residual; the report says which", failed, len(erasures))
	}
	return nil
}

// reapply runs the store's erasure for what a trace named.
//
// A trace of a person names one. Should it ever match more — two identifiers with one digest,
// which the digest's width makes a matter of theory — the first is erased under the erasure's own
// identifier and the rest under new ones, because a receipt's identifier is unique and every one of
// them was asked for.
func reapply(ctx context.Context, eraser *pg.Eraser, schema pg.Schema, erasure erasurerecord.Erasure, named []string) ([]domain.Erasure, error) {
	if erasure.Kind == erasurerecord.KindSource {
		receipt, err := eraser.ReapplySources(ctx, schema, erasure.Project, erasure.RequestID, named)
		return []domain.Erasure{receipt}, err
	}
	var receipts []domain.Erasure
	for i, subject := range named {
		id := erasure.RequestID
		if i > 0 {
			id = uuid.NewString()
		}
		receipt, err := eraser.ReapplySubject(ctx, schema, erasure.Project, id, subject)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	return receipts, nil
}
