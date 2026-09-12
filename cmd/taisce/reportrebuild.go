// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"time"

	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/report"
)

// MaxReportPage bounds one invocation. Each community written is one model call, and an operator who
// asked for a thousand would hold a terminal open for an hour with nothing committed until it ended.
// A hundred is the same ceiling `rebuild project` puts on sources, for the same reason.
const MaxReportPage = 100

// reportRebuildCommand writes the reports a project is missing, now.
//
// # Why this command exists
//
// A report is deleted when the facts under it change, and the worker's subject pass writes it back a
// few per tick. That is correct — a report describing a claim the system has stopped believing is
// worse than a missing one — and it leaves a window in which a recall that would have returned
// a theme returns nothing. `rebuild staleness` says how large that window is; without this command
// the only answer to it was to wait.
//
// # Why it is the subject pass and not something new
//
// The pass partitions the graph, then writes what `Unwritten` returns. Running it here means an
// operator's rewrite and the worker's are the same code: the partition is not recomputed differently,
// the writer identity is the same, and a report written by hand is indistinguishable from one written
// on a tick — which is what makes it safe for both to be running at once.
//
// # Why it needs the model and the operator connection
//
// Writing a report is a model call, so this cannot be the connection `status` and `cancel` use. The
// identity it stamps comes from the configured reporter, so a deployment whose report prompt changed
// rewrites into the new identity simply by running it.
func reportRebuildCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("rebuild reports", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	scope := flags.String("project", "", "project")
	limit := flags.Int("limit", formation.DefaultPolicy().ReportsPerPass, "communities per invocation (1..100)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if _, err := pg.NewSchema(*scope); err != nil || flags.NArg() != 0 || *limit < 1 || *limit > MaxReportPage {
		return pg.ErrInvalidGeneration
	}
	config, err := inference.ConfigFromEnv()
	if err != nil {
		return err
	}
	// One turn budget per community, as the worker gives itself, and the operator's budget if they
	// set one: a report over a large community is the same size of call as an extraction
	// over a large turn, and a deployment that needed longer turns needs longer reports.
	perReport, err := configuredTurnBudget(formation.DefaultPolicy().TurnBudget)
	if err != nil {
		return err
	}
	budget := perReport * time.Duration(*limit)
	config.Timeout = perReport
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	pool, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := requireProject(ctx, pool, *scope); err != nil {
		return err
	}
	stop := startCLIActivity(ctx, "writing the reports this project is missing")
	defer stop()
	pass, err := formation.NewSubjects(pg.NewCommunityStore(pool, schema),
		report.New(inference.NewReporter(config))).Run(ctx, *scope, *limit)
	stop()
	if err != nil {
		return err
	}
	written := reportRebuildResult{Project: *scope, Entities: pass.Entities,
		Communities: pass.Communities, Written: pass.Written, Failed: pass.Failed}
	if fancyOutput(out) {
		_, err = io.WriteString(out, renderReportRebuild(currentTheme(), written))
		return err
	}
	return json.NewEncoder(out).Encode(withStaleness(ctx, pool, schema, *scope, written))
}

// reportRebuildResult is what one invocation did, in the vocabulary the subject pass already uses.
//
// Written and Failed are this invocation's, never a total: a community whose material a model refused
// is counted here and tried again by the next invocation or the next tick, because `Unwritten` selects
// on the absence of a report rather than on a record of having tried.
type reportRebuildResult struct {
	Project     string `json:"project"`
	Entities    int    `json:"entities"`
	Communities int    `json:"communities"`
	Written     int    `json:"reports_written"`
	Failed      int    `json:"reports_failed"`
}
