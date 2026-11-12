// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// TestAWriterAtCapacityIsToldHowLongATurnTakesToForm holds what the header means.
//
// The wait is the time the instance currently takes to form one turn — between two readings of its
// formed-turn count, divided by how many formed. It is checked against readings whose spacing this
// test chose, so the number has a known right answer.
func TestAWriterAtCapacityIsToldHowLongATurnTakesToForm(t *testing.T) {
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		why      string
		readings [][2]int64 // formed-turn count, seconds after the start
		askAfter int64
		want     string
	}{
		{"nothing is known, so not soon", nil, 0, "30"},
		{"one reading is a starting point and not a rate", [][2]int64{{10, 0}}, 5, "30"},
		{"twenty turns in ten seconds is half a second each, and a second is the least said", [][2]int64{{10, 0}, {30, 10}}, 11, "1"},
		{"two turns in ten seconds", [][2]int64{{10, 0}, {12, 10}}, 11, "5"},
		{"three turns in ten seconds rounds up, because a retry a moment early is refused again", [][2]int64{{10, 0}, {13, 10}}, 11, "4"},
		{"a reading that did not move keeps the last rate", [][2]int64{{10, 0}, {12, 10}, {12, 20}}, 21, "5"},
		{"the rate is the latest stretch, not the average of all of them", [][2]int64{{0, 0}, {100, 10}, {101, 20}}, 21, "10"},
		{"a stretch that included a quiet reading is measured across all of it", [][2]int64{{10, 0}, {10, 10}, {11, 20}}, 21, "20"},
		{"slower than the ceiling says the ceiling", [][2]int64{{10, 0}, {11, 80}}, 81, "30"},
		{"a rate from before the instance stopped forming is not believed", [][2]int64{{10, 0}, {12, 10}}, 10 + 121, "30"},
		{"a count that fell starts over", [][2]int64{{10, 0}, {12, 10}, {3, 20}}, 21, "30"},
	} {
		d := api.NewDrainRate()
		for _, r := range c.readings {
			d.Observe(r[0], at.Add(time.Duration(r[1])*time.Second))
		}
		if got := d.RetryAfter(at.Add(time.Duration(c.askAfter) * time.Second)); got != c.want {
			t.Errorf("%s: told to wait %s seconds, want %s", c.why, got, c.want)
		}
	}
	// A server wired without one still answers, with the ceiling.
	var none *api.DrainRate
	none.Observe(5, at)
	if got := none.RetryAfter(at); got != "30" {
		t.Errorf("with no estimator the wait stated is %s", got)
	}
}

// TestTheRefusalForAFullBacklogCarriesTheEstimate is the header on the refusal itself, from the
// count the workers really write down.
func TestTheRefusalForAFullBacklogCarriesTheEstimate(t *testing.T) {
	m := newManaged(t, "api_drain_rate")
	ctx := context.Background()
	m.manageDo(t, "/backlog/limits", map[string]any{"max_pending_per_project": 1}, m.operator, http.StatusOK, nil)
	turn := map[string]any{"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}}}
	refused := func() string {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, m.server.URL+"/v1/observations", strings.NewReader(
			`{"data_subject_id":"subject-1","messages":[{"role":"user","content":"I also work at Ensera."}]}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+m.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("a write to a full backlog was answered %d", resp.StatusCode)
		}
		return resp.Header.Get("Retry-After")
	}
	formed := func() int64 {
		t.Helper()
		if err := m.measurements.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		n, err := pg.ReadFormedTurns(ctx, m.pool, m.schema)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	m.do(t, http.MethodPost, "/v1/observations", turn, http.StatusCreated, nil)
	// Nothing has formed, so nothing is known: not soon.
	if got := refused(); got != "30" {
		t.Fatalf("with nothing formed the refusal says to wait %q seconds", got)
	}

	// The count the estimator is fed is the instance's own: zero before a turn forms, one after.
	start := time.Now().Add(-8 * time.Second)
	before := formed()
	m.drain.Observe(before, start)
	m.form(t)
	after := formed()
	if before != 0 || after != 1 {
		t.Fatalf("the formed-turn count read %d and then %d around one turn forming", before, after)
	}
	m.drain.Observe(after, start.Add(4*time.Second))

	// Full again, and now the instance is known to have formed one turn in four seconds.
	m.do(t, http.MethodPost, "/v1/observations", turn, http.StatusCreated, nil)
	if got := refused(); got != "4" {
		t.Fatalf("one turn formed in four seconds and the refusal says to wait %q seconds", got)
	}
}
