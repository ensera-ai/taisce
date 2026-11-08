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
