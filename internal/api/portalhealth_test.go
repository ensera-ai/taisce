// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"strings"
	"testing"
)

// ── Whether formation is failing now, not whether it ever failed ──────────────────────────────
//
// `failed_attempts` is a lifetime total that nothing resets, so its value cannot say whether
// anything is wrong at the moment an operator looks at the page. The tile is marked only while the
// last failure is more recent than the last turn to form, and it carries the time of that failure,
// so a count that stopped rising stops raising an alarm and an operator can tell when it stopped.
//
// The section header is held to the same answer: a page that says nothing needs attention beside a
// marked tile has told the operator two different things and is wrong once.
//
// The limit, asserted here rather than left to be discovered: the health row is instance-wide, so a
// turn forming in any project clears the mark even if another project is still failing.
func TestTheOpsCentreMarksFailedAttemptsOnlyWhileFailuresAreMoreRecentThanFormation(t *testing.T) {
	a := signedInPortal(t, "portal_failure_currency")
	ctx := context.Background()
	// The worker reported in, so the header's own two alarms are quiet and what it says about
	// failures is what is being read here.
	health := func(t *testing.T, set string) {
		t.Helper()
		if _, err := a.pool.Exec(ctx, a.schema.SQL(`UPDATE {schema}.formation_health
			SET heartbeat_at=clock_timestamp(),`+set+` WHERE singleton`)); err != nil {
			t.Fatal(err)
		}
	}
	// lastFailure is the stamp the database holds, formatted the way the page formats it: the
	// assertion is that the page shows the row's own time, not that it shows some time.
	lastFailure := func(t *testing.T) string {
		t.Helper()
		var at *string
		if err := a.pool.QueryRow(ctx, a.schema.SQL(
			`SELECT to_char(last_failure_at AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS') FROM {schema}.formation_health WHERE singleton`)).Scan(&at); err != nil {
			t.Fatal(err)
		}
		if at == nil {
			return ""
		}
		return *at
	}

	// Failing now: twelve attempts have failed, the last of them since anything formed.
	health(t, `formed_count=2,failed_attempts=12,
		last_progress_at=clock_timestamp()-interval '12 minutes',last_failure_at=clock_timestamp()-interval '1 minute'`)
	body, _ := a.page(t, "/portal/")
	failing := tile(t, body, "Failed attempts")
	if !strings.Contains(class(failing), "warn") {
		t.Fatalf("a failure more recent than the last formation must mark the tile: %s", failing)
	}
	if when := lastFailure(t); !strings.Contains(failing, when) {
		t.Fatalf("the tile must say when formation last failed (%s): %s", when, failing)
	}
	if strings.Contains(body, "Nothing needs attention") {
		t.Fatal("the header cannot say nothing needs attention while the failure tile is marked")
	}
	if !strings.Contains(body, "Formation is failing") {
		t.Fatalf("the header must say formation is failing: %s", section(t, body))
	}
	preview(t, "overview-failures-current", body)

	// Recovered: nothing was forgotten — the count is still twelve and the time is still shown —
	// but a turn has formed since the last failure, so the page stops alarming.
	health(t, `formed_count=3,failed_attempts=12,
		last_progress_at=clock_timestamp(),last_failure_at=clock_timestamp()-interval '12 minutes'`)
	body, _ = a.page(t, "/portal/")
	recovered := tile(t, body, "Failed attempts")
	if strings.Contains(class(recovered), "warn") {
		t.Fatalf("a count that stopped rising must not stay marked: %s", recovered)
	}
	if !strings.Contains(recovered, ">12<") {
		t.Fatalf("the lifetime total must survive recovery: %s", recovered)
	}
	if when := lastFailure(t); !strings.Contains(recovered, when) {
		t.Fatalf("a recovered instance must still say when it last failed (%s): %s", when, recovered)
	}
	if !strings.Contains(body, "Nothing needs attention") {
		t.Fatalf("an instance that has recovered needs no attention: %s", section(t, body))
	}
	preview(t, "overview-failures-recovered", body)

	// Nothing has ever formed, and attempts are failing: there is no progress to be more recent
	// than the failure, and a missing stamp must not read as recovery.
	health(t, `formed_count=0,failed_attempts=6,last_progress_at=NULL,last_failure_at=clock_timestamp()`)
	body, _ = a.page(t, "/portal/")
	never := tile(t, body, "Failed attempts")
	if !strings.Contains(class(never), "warn") {
		t.Fatalf("an instance where nothing has ever formed must be marked while attempts fail: %s", never)
	}
	if strings.Contains(body, "Nothing needs attention") {
		t.Fatal("the header cannot say nothing needs attention while nothing has ever formed")
	}

	// A clean instance: no failure has happened, so there is no count, no mark, and no time to show.
	health(t, `formed_count=4,failed_attempts=0,last_progress_at=clock_timestamp(),last_failure_at=NULL`)
	body, _ = a.page(t, "/portal/")
	clean := tile(t, body, "Failed attempts")
	if strings.Contains(class(clean), "warn") {
		t.Fatalf("an instance that has never failed must not be marked: %s", clean)
	}
	if strings.Contains(clean, "last failed") {
		t.Fatalf("an instance that has never failed has no failure time to show: %s", clean)
	}
	if !strings.Contains(body, "Nothing needs attention") {
		t.Fatalf("a clean instance needs no attention: %s", section(t, body))
	}
	preview(t, "overview-clean-after-failures", body)
}

// tile is the markup of the one tile carrying label, so an assertion about the failure tile cannot
// pass on a neighbour's class: the class is written before the label, and the tiles are siblings.
func tile(t *testing.T, body, label string) string {
	t.Helper()
	at := strings.Index(body, label+"</div>")
	if at < 0 {
		t.Fatalf("the page has no tile labelled %q", label)
	}
	start := strings.LastIndex(body[:at], `<div class="tile`)
	if start < 0 {
		t.Fatalf("%q is not inside a tile", label)
	}
	end := len(body)
	for _, boundary := range []string{`<div class="tile`, `<p class="note">`} {
		if i := strings.Index(body[at:], boundary); i >= 0 && at+i < end {
			end = at + i
		}
	}
	return body[start:end]
}

// class is the tile's class attribute, which is what carries the judgement.
func class(markup string) string {
	open := strings.Index(markup, `"`)
	shut := strings.Index(markup[open+1:], `"`)
	if open < 0 || shut < 0 {
		return ""
	}
	return markup[open+1 : open+1+shut]
}

// section is the "How it is running" header, quoted in a failure so the message says what the page
// actually said instead of printing the whole document.
func section(t *testing.T, body string) string {
	t.Helper()
	at := strings.Index(body, `<div class="section-head">`)
	if at < 0 {
		t.Fatal("the page has no health section")
	}
	end := strings.Index(body[at:], `<div class="tiles`)
	if end < 0 {
		return body[at:]
	}
	return body[at : at+end]
}

// ── A project failing on its own ──────────────────────────────────────────────────────────────
//
// `formation_health` is one row for the instance: one `last_progress_at`, one `last_failure_at`. So
// the currency those two stamps answer is the instance's, and it is silent about a project — a turn
// forming anywhere moves the progress stamp and clears the mark while another project fails every
// attempt. An instance with one busy project and one broken one read as healthy on the page an
// operator is told to watch, with the evidence present but drawing no eye: the broken project's
// *Behind by* rising, which looks the same as a slow model.
//
// A project is failing now when its backlog holds a turn that has been tried and did not form. That
// needs no second stamp and no window: a turn that formed has left the backlog, and one the driver
// gave up on is parked, so what is left stamped with a failure is work still owed that has already
// gone wrong once. The status takes the worse of the two readings, and the count of such turns is in
// the project's own row — one failing turn is a turn to look at, all of them is an endpoint to look
// at, and a boolean loses the difference.
func TestTheOpsCentreShowsAProjectFailingOnItsOwnWhileTheInstanceReadsAsRecovered(t *testing.T) {
	a := signedInPortal(t, "portal_project_failure")
	ctx := context.Background()
	// The instance's own stamps say recovered: something formed since the last failure. Before this
	// change that alone decided the header.
	if _, err := a.pool.Exec(ctx, a.schema.SQL(`UPDATE {schema}.formation_health
		SET heartbeat_at=clock_timestamp(),formed_count=9,failed_attempts=4,
		    last_progress_at=clock_timestamp(),last_failure_at=clock_timestamp()-interval '9 minutes'
		WHERE singleton`)); err != nil {
		t.Fatal(err)
	}
	body, _ := a.page(t, "/portal/")
	if !strings.Contains(body, "Nothing needs attention") {
		t.Fatalf("with no project failing, a recovered instance needs no attention: %s", section(t, body))
	}
	if stood := tile(t, body, "Stood down"); !strings.Contains(stood, "no replica has waited for another") {
		t.Fatalf("an instance where no worker has stood down says so rather than showing a bare zero: %s", stood)
	}

	// Now one project's backlog holds two turns that were tried and did not form. Nothing about the
	// instance row changes.
	stored := func(t *testing.T, scope string, offsets ...int64) {
		t.Helper()
		for _, offset := range offsets {
			if _, err := a.pool.Exec(ctx, a.schema.SQL(`INSERT INTO {schema}.observation
				(observation_id, scope, log_offset, kind, occurred_at, source_role, data_subject_id,
				 formation_attempts, formation_failed_at, formation_error, content_characters)
				VALUES (gen_random_uuid(), $1, $2, 'turn', now(), 'user', 'someone', 2, clock_timestamp(), 'the model refused', 0)`),
				scope, offset); err != nil {
				t.Fatal(err)
			}
		}
	}
	stored(t, "p1", 0, 1)
	body, _ = a.page(t, "/portal/")
	if !strings.Contains(body, "A project is failing") {
		t.Fatalf("a project failing on its own must mark the header, whatever the instance's stamps say: %s", section(t, body))
	}
	if strings.Contains(body, "Nothing needs attention") {
		t.Fatal("the header cannot say nothing needs attention while a project is failing")
	}
	failed := tile(t, body, "Failed attempts")
	if !strings.Contains(failed, "one project is failing now") {
		t.Fatalf("the tile must say a project is failing rather than when the instance last did: %s", failed)
	}
	// And the tile is marked, although the instance's own stamps say recovered: the currency the
	// page reports is the worse of the two readings, so a marked header beside an unmarked tile
	// cannot happen.
	if !strings.Contains(class(failed), "warn") {
		t.Fatalf("a project failing on its own must mark the tile too: %s", failed)
	}
	// And the project's own row carries how many of its waiting turns have already failed, which is
	// the difference between one turn to look at and an endpoint to look at.
	if !strings.Contains(body, `title="waiting turns that have already been tried and did not form`) {
		t.Fatalf("the projects table must carry the failing count per project: %s", body)
	}
	preview(t, "overview-one-project-failing", body)

	// A turn that forms leaves the backlog, so the project stops failing with no stamp to reset.
	if _, err := a.pool.Exec(ctx, a.schema.SQL(
		`UPDATE {schema}.observation SET formed_at=clock_timestamp() WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	body, _ = a.page(t, "/portal/")
	if !strings.Contains(body, "Nothing needs attention") {
		t.Fatalf("a project whose turns have formed is not failing: %s", section(t, body))
	}

	// A turn the driver gave up on is parked, not failing: it is reported as parked, which is a
	// different thing to act on, and counting it here would mean a project that had given up once
	// read as failing forever.
	if _, err := a.pool.Exec(ctx, a.schema.SQL(
		`UPDATE {schema}.observation SET formed_at=NULL, parked_at=clock_timestamp() WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	body, _ = a.page(t, "/portal/")
	if strings.Contains(body, "A project is failing") {
		t.Fatalf("a parked turn is parked, not failing: %s", section(t, body))
	}
	if !strings.Contains(body, "Turns are parked") {
		t.Fatalf("the header must report the parked turns instead: %s", section(t, body))
	}

	// Several projects failing says how many, so a page read once distinguishes one stuck project
	// from an instance-wide problem.
	if _, err := a.pool.Exec(ctx, a.schema.SQL(
		`UPDATE {schema}.observation SET parked_at=NULL WHERE scope='p1'`)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, a.schema.SQL(
		`INSERT INTO {schema}.project (scope, label) VALUES ('p2', 'Second')`)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pool.Exec(ctx, a.schema.SQL(
		`INSERT INTO {schema}.watermark (scope, log_offset, watermark_at) VALUES ('p2', 0, now())`)); err != nil {
		t.Fatal(err)
	}
	stored(t, "p2", 0)
	body, _ = a.page(t, "/portal/")
	if !strings.Contains(body, "2 projects are failing") {
		t.Fatalf("the header must say how many projects are failing: %s", section(t, body))
	}
	if failed := tile(t, body, "Failed attempts"); !strings.Contains(failed, "2 projects are failing now") {
		t.Fatalf("so must the tile: %s", failed)
	}
	preview(t, "overview-two-projects-failing", body)
}

// A worker that stood down is visible on the page, which is the only place the per-scope lock is
// evidenced.
//
// Two workers against one scope is what the chart's default topology does, and what makes it safe is
// that the scope is drained under an advisory lock, so the second worker finds it busy and stands
// down rather than forming the same turn twice. That count existed for the length of a pass and was
// then discarded. So four workers against one backlog looked identical to one worker and three that
// were wedged, and confirming the property the deployment is sold on meant sampling pg_locks on the
// primary and reading pg_stat_activity to find which pod held it — not available at all to anyone
// without a psql session.
func TestTheOpsCentreShowsHowOftenAWorkerStoodDownForAnother(t *testing.T) {
	a := signedInPortal(t, "portal_stand_down")
	ctx := context.Background()
	if _, err := a.pool.Exec(ctx, a.schema.SQL(`UPDATE {schema}.formation_health
		SET heartbeat_at=clock_timestamp(),stood_down_count=41,last_stood_down_at=clock_timestamp()-interval '20 seconds'
		WHERE singleton`)); err != nil {
		t.Fatal(err)
	}
	body, _ := a.page(t, "/portal/")
	stood := tile(t, body, "Stood down")
	if !strings.Contains(stood, ">41<") {
		t.Fatalf("the page must carry the number of stand-downs: %s", stood)
	}
	if !strings.Contains(stood, "last ") || strings.Contains(stood, "no replica has waited") {
		t.Fatalf("it must say when one last happened: %s", stood)
	}
	// Not a judgement. Zero is normal on one replica and until two workers want the same scope, so
	// there is no state in which this marks the page.
	if strings.Contains(class(stood), "warn") || strings.Contains(class(stood), "bad") {
		t.Fatalf("a worker standing down is the lock working, not a fault: %s", stood)
	}
	if !strings.Contains(body, "Nothing needs attention") {
		t.Fatalf("stand-downs alone need no attention: %s", section(t, body))
	}
	preview(t, "overview-workers-standing-down", body)
}
