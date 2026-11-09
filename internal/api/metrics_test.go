// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/google/uuid"
)

// expectedMetrics is the whole scrape, named here rather than checked as a subset.
//
// An exact set, deliberately. A subset check holds "the numbers an operator asked for are served"
// and says nothing about the series somebody adds later — and what this endpoint must not grow is
// exactly that: one more number, taken from somewhere that knows which project or which subject it
// belongs to. A test that fails on an addition makes adding one a decision somebody takes
// deliberately, which is the whole of the argument in metrics.go.
//
// Every property below is checked against this set and against every line of the body, so a series
// added here is a series the label and content checks immediately cover.
var expectedMetrics = []string{
	"taisce_formation_backlog",
	"taisce_formation_backlog_limit",
	"taisce_formation_parked",
	"taisce_formation_failing_projects",
	"taisce_worker_responsive",
	"taisce_formation_heartbeat_timestamp_seconds",
	"taisce_formation_oldest_pending_timestamp_seconds",
	"taisce_database_connections",
	"taisce_cluster_connections",
	"taisce_connection_limit",
	"taisce_connection_reserved",
	"taisce_connection_headroom",
	"taisce_operations_allowed",
	"taisce_operations_refused",
	"taisce_operations_window_seconds",
}

// samplePattern is one sample with no label set at all: a prefixed lower-case name, a space, an
// integer or NaN, and nothing else on the line. A metric that grew a label would not match it.
var samplePattern = regexp.MustCompile(`^(taisce_[a-z][a-z0-9_]*) (-?[0-9]+|NaN)$`)

// exposed is one scrape, read.
type exposed struct {
	// numbers holds every series that reported a value, and absent every series that reported NaN.
	// Two maps rather than a sentinel, because the whole point of NaN here is that it is not a
	// number and must not be arithmetic on one.
	numbers map[string]int64
	absent  map[string]bool
}

// num is one series' value, and fails when the series reported no moment at all.
func (e exposed) num(t *testing.T, name string) int64 {
	t.Helper()
	if e.absent[name] {
		t.Fatalf("%s reports no value, and this assertion needs one", name)
	}
	value, ok := e.numbers[name]
	if !ok {
		t.Fatalf("%s is not served", name)
	}
	return value
}

// scrapeMetrics reads the endpoint the way a monitoring system does: one GET, a bearer credential,
// and whatever comes back.
func scrapeMetrics(t *testing.T, base, token string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+api.ManagementPrefix+api.MetricsPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(raw)
}

// parseExposition reads a scrape and refuses anything the format does not permit.
//
// It is strict on purpose. The claim this endpoint makes is not "a parser accepts the output" but
// "every line is a name and one number", because a label set is the one piece of syntax through
// which a project name, a subject or anybody's words could reach a monitoring system. So the first
// thing checked is that no brace appears anywhere in the body, and the second is that every sample
// line matches a pattern with no room for one. Both checks walk the body itself rather than a list
// of names, so they hold over every series the endpoint emits, including ones added after this was
// written.
func parseExposition(t *testing.T, body string) exposed {
	t.Helper()
	if strings.ContainsAny(body, "{}") {
		t.Fatalf("the scrape carries a label set, which is how a project name leaves the instance:\n%s", body)
	}
	out := exposed{numbers: map[string]int64{}, absent: map[string]bool{}}
	helped, typed := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# HELP "):
			name, help, ok := strings.Cut(strings.TrimPrefix(line, "# HELP "), " ")
			if !ok || strings.TrimSpace(help) == "" {
				t.Fatalf("a metric is declared without saying what it is: %q", line)
			}
			helped[name] = true
		case strings.HasPrefix(line, "# TYPE "):
			name, kind, ok := strings.Cut(strings.TrimPrefix(line, "# TYPE "), " ")
			if !ok || kind != "gauge" {
				t.Fatalf("a metric is declared as %q; this endpoint publishes gauges only, because none of its numbers is cumulative: %q", kind, line)
			}
			typed[name] = true
		default:
			match := samplePattern.FindStringSubmatch(line)
			if match == nil {
				t.Fatalf("a sample this endpoint must not serve: %q", line)
			}
			if _, seen := out.numbers[match[1]]; seen || out.absent[match[1]] {
				t.Fatalf("%s is served twice", match[1])
			}
			if match[2] == "NaN" {
				out.absent[match[1]] = true
				break
			}
			value, err := strconv.ParseInt(match[2], 10, 64)
			if err != nil {
				t.Fatalf("%q is not an integer: %v", line, err)
			}
			out.numbers[match[1]] = value
		}
	}
	served := len(out.numbers) + len(out.absent)
	for name := range out.numbers {
		if !helped[name] || !typed[name] {
			t.Fatalf("%s is served without a HELP and a TYPE line, so a scraper cannot say what it is", name)
		}
	}
	for name := range out.absent {
		if !helped[name] || !typed[name] {
			t.Fatalf("%s is served without a HELP and a TYPE line, so a scraper cannot say what it is", name)
		}
	}
	if served != len(expectedMetrics) {
		t.Fatalf("the scrape serves %d numbers, and %d are named: %v %v", served, len(expectedMetrics), out.numbers, out.absent)
	}
	for _, name := range expectedMetrics {
		if _, ok := out.numbers[name]; !ok && !out.absent[name] {
			t.Fatalf("%s is not served: %v", name, out.numbers)
		}
	}
	return out
}

// The scrape serves the numbers an operator alerts on — the formation backlog against the limit
// that makes it start refusing writes, the parked count, the projects that are failing on their
// own, whether a worker has answered and when it last did, the database sessions in use against the
// server's real ceiling, and operations allowed and refused apart — on the management surface, in
// the exposition format, with its media type named.
func TestInstanceMetricsServeTheNumbersAnOperatorAlertsOnOverTheManagementSurface(t *testing.T) {
	m := newManaged(t, "api_metrics_serve")
	resp, body := scrapeMetrics(t, m.manage.URL, m.operator)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the scrape returned %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("a scraper is told the body is %q, and cannot refuse a format it does not know", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("a cached scrape is a reading of an unknown moment, got %q", got)
	}
	values := parseExposition(t, body)
	// The database's own ceiling has to be a real one, or the headroom below it means nothing.
	if values.num(t, "taisce_connection_limit") <= 0 {
		t.Fatalf("the server's connection ceiling is served as %d", values.num(t, "taisce_connection_limit"))
	}
	if values.num(t, "taisce_cluster_connections") <= 0 || values.num(t, "taisce_database_connections") <= 0 {
		t.Fatalf("this test holds connections open, so sessions in use cannot be zero: %v", values.numbers)
	}
	// The headroom is published rather than left to be subtracted, so it has to agree with the
	// three numbers it comes from — a headroom that did not would be the one number an operator
	// trusts and the one that is wrong.
	ceiling := values.num(t, "taisce_connection_limit") - values.num(t, "taisce_connection_reserved") -
		values.num(t, "taisce_cluster_connections")
	if ceiling < 0 {
		ceiling = 0
	}
	if values.num(t, "taisce_connection_headroom") != ceiling {
		t.Fatalf("headroom %d does not follow from the limit, the reservation and the sessions in use: %v",
			values.num(t, "taisce_connection_headroom"), values.numbers)
	}
	// A fresh instance has no worker, and the flag says so rather than defaulting to healthy. The
	// moment behind the flag reports nothing at all, which is not the same as a worker that reported
	// at the epoch: an operator's own staleness rule must not fire on an instance that has one.
	if values.num(t, "taisce_worker_responsive") != 0 {
		t.Fatalf("no worker has reported, and the instance says %d", values.num(t, "taisce_worker_responsive"))
	}
	if !values.absent["taisce_formation_heartbeat_timestamp_seconds"] {
		t.Fatalf("no worker has ever reported, and the scrape names a moment it did: %v", values.numbers)
	}
	// Nothing is waiting either, and an empty backlog has no oldest turn. A zero here would read as
	// a turn stored in 1970 and would fire every staleness rule written against it.
	if !values.absent["taisce_formation_oldest_pending_timestamp_seconds"] {
		t.Fatalf("the backlog is empty and the scrape names an oldest turn: %v", values.numbers)
	}
	if values.num(t, "taisce_formation_backlog") != 0 || values.num(t, "taisce_formation_parked") != 0 ||
		values.num(t, "taisce_formation_failing_projects") != 0 {
		t.Fatalf("a fresh instance is behind: %v", values.numbers)
	}
	if values.num(t, "taisce_formation_backlog_limit") <= 0 {
		t.Fatalf("the backlog is measured against %d, so a backlog cannot be read as a share of anything",
			values.num(t, "taisce_formation_backlog_limit"))
	}
	// The window is part of the reading: a count without it is a number with no conditions.
	if values.num(t, "taisce_operations_window_seconds") <= 0 {
		t.Fatalf("the operation counts cover %d seconds", values.num(t, "taisce_operations_window_seconds"))
	}
	// Nothing has been done to this instance, so both outcomes are zero — and they stay zero
	// however many times it is scraped. A scrape arrives on a timer forever and the ledger has no
	// delete, so an audited scrape would grow the table whose rows it counts and the operation
	// count would end up measuring the monitoring of itself.
	for i := 0; i < 3; i++ {
		_, body := scrapeMetrics(t, m.manage.URL, m.operator)
		again := parseExposition(t, body)
		if again.num(t, "taisce_operations_allowed") != 0 || again.num(t, "taisce_operations_refused") != 0 {
			t.Fatalf("scraping put %d allowed and %d refused operations on the ledger",
				again.num(t, "taisce_operations_allowed"), again.num(t, "taisce_operations_refused"))
		}
	}
}

// Every caller that is not an operator is refused, with the answer a stranger gets: no credential,
// a token nobody issued, a project credential, and an operator credential that has been revoked.
// The memory port serves no scrape at all, so a project credential cannot learn how the instance is
// doing even by asking the door it is allowed through.
func TestInstanceMetricsAreRefusedToEveryCallerThatIsNotALiveOperator(t *testing.T) {
	m := newManaged(t, "api_metrics_door")
	ctx := context.Background()
	for _, stranger := range []string{"", "tsk_notatoken", m.token} {
		resp, body := scrapeMetrics(t, m.manage.URL, stranger)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("the scrape answered a caller holding %q with %d: %s", stranger, resp.StatusCode, body)
		}
		if strings.Contains(body, "taisce_") {
			t.Fatalf("a refused scrape carried numbers: %s", body)
		}
	}
	// The memory port is where an agent's project credential goes, and it has no scrape to find:
	// not under the management prefix, and not at the conventional path either.
	for _, path := range []string{"/metrics", api.ManagementPrefix + api.MetricsPath} {
		for _, token := range []string{m.token, m.operator} {
			if status, body := m.raw(t, http.MethodGet, path, nil, token); status != http.StatusNotFound {
				t.Fatalf("the memory port answered %s with %d: %s", path, status, body)
			}
		}
	}
	// A scrape is a read. Writing to it is not a thing the surface offers.
	if status, body := rawAgainst(t, m.manage.URL, http.MethodPost, api.ManagementPrefix+api.MetricsPath,
		map[string]any{}, m.operator); status != http.StatusMethodNotAllowed {
		t.Fatalf("a POST to the scrape returned %d: %s", status, body)
	}
	// A revoked operator is a stranger. Issued here rather than taken from the registry's list so
	// that the credential this test revokes is only this test's.
	credentials := credential.NewStore(m.pool, string(migrate.ControlSchema))
	token, grant, err := credentials.IssueOperator(ctx, "metrics-revoked-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	if resp, body := scrapeMetrics(t, m.manage.URL, token); resp.StatusCode != http.StatusOK {
		t.Fatalf("a live operator credential was refused the scrape with %d: %s", resp.StatusCode, body)
	}
	if err := credentials.Revoke(ctx, grant.CredentialID); err != nil {
		t.Fatal(err)
	}
	if resp, body := scrapeMetrics(t, m.manage.URL, token); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a revoked operator credential still scrapes, with %d: %s", resp.StatusCode, body)
	}
}

// The scrape carries no label at all, and nothing from memory. A project exists, a turn has been
// stored under a subject with words in it, and none of those strings is anywhere in the body —
// asserted against the whole body, because a scrape goes to a monitoring system whose access and
// retention are not the operator credential's.
//
// The checks walk the body rather than a list of series, so they hold over everything the endpoint
// emits: a series added later is covered by this test the day it is added, and parseExposition's
// exact-set check is what forces whoever adds one to come here and say so.
func TestInstanceMetricsCarryNoProjectLabelAndNothingFromMemory(t *testing.T) {
	m := newManaged(t, "api_metrics_labels")
	marker := strings.ReplaceAll(uuid.NewString(), "-", "")
	project := "scrapeproj_" + marker[:12]
	subject := "scrapesubject-" + marker[12:24]
	content := "I work at Ensera. I live in Dublin. " + marker + " is in this sentence."
	m.manageDo(t, "/projects/create", map[string]any{"name": project}, m.operator, http.StatusCreated, nil)
	m.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": subject, "messages": []map[string]any{{"role": "user", "content": content}},
	}, http.StatusCreated, nil)
	// A refusal too, so the counts this scrape serves were produced by real words being rejected
	// and the words still do not appear.
	m.manageDo(t, "/projects/retention", map[string]any{"name": project + "_absent", "retention_days": 30},
		m.operator, http.StatusNotFound, nil)

	resp, body := scrapeMetrics(t, m.manage.URL, m.operator)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the scrape returned %d: %s", resp.StatusCode, body)
	}
	// parseExposition refuses a brace anywhere and refuses any sample line that is not a name and
	// one number, so a per-project label set cannot pass it on any series.
	values := parseExposition(t, body)
	// The instance now holds a project, a subject and a turn, so this is the state in which a leak
	// would happen rather than an idle one.
	if values.num(t, "taisce_formation_backlog") == 0 {
		t.Fatal("the turn this test stored is not in the backlog, so the scrape was taken of an idle instance")
	}
	for _, secret := range []string{project, subject, marker, "Ensera", "Dublin", "p1", m.operator, m.token} {
		if strings.Contains(body, secret) {
			t.Fatalf("the scrape carries %q, which came from inside the instance:\n%s", secret, body)
		}
	}
	// And the complete argument, stated as one assertion rather than as a list of words to look
	// for: every sample line is a name from the declared set followed by an integer or NaN. With no
	// braces anywhere in the body, that leaves the body made of author-written text and numbers,
	// with no syntax a value from inside the instance could occupy — neither a label, which would
	// need a brace, nor a name, which would have to be one of the fifteen and is not.
	//
	// A word check was tried here first and was the wrong shape: `failing_projects` is the plural
	// of a thing counted, not a series keyed on one, so looking for "project" in a line failed on
	// an aggregate that is exactly what this endpoint is supposed to serve.
	named := map[string]bool{}
	for _, name := range expectedMetrics {
		named[name] = true
	}
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, " ")
		if !ok || !named[name] {
			t.Fatalf("a sample names %q, which is not one of the series this endpoint declares: %q", name, line)
		}
		if !regexp.MustCompile(`^(-?[0-9]+|NaN)$`).MatchString(value) {
			t.Fatalf("%s carries %q, which is not a number: a series' value is the other place data could ride out", name, value)
		}
	}
}

// The numbers are read from the instance, not written into the handler: storing a turn moves the
// backlog up and gives the backlog an age, forming it moves both back, an allowed operation moves
// the allowed count and a refused one moves the refused count — and the two outcomes move
// independently, which is what makes a run of refusals visible from outside.
func TestInstanceMetricsFollowTheBacklogAndTheLedgerRatherThanBeingConstants(t *testing.T) {
	m := newManaged(t, "api_metrics_movement")
	_, body := scrapeMetrics(t, m.manage.URL, m.operator)
	before := parseExposition(t, body)

	stamped := time.Now().Add(-time.Minute)
	m.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "subject-1",
		"messages":        []map[string]any{{"role": "user", "content": "I work at Ensera. I live in Dublin."}},
	}, http.StatusCreated, nil)
	_, body = scrapeMetrics(t, m.manage.URL, m.operator)
	stored := parseExposition(t, body)
	if stored.num(t, "taisce_formation_backlog") != before.num(t, "taisce_formation_backlog")+1 {
		t.Fatalf("one turn was stored and the backlog went from %d to %d",
			before.num(t, "taisce_formation_backlog"), stored.num(t, "taisce_formation_backlog"))
	}
	// The backlog now has an age, where a moment ago it had none. This is the series an operator
	// writes their own staleness rule against, so it has to be a real moment and not the epoch.
	if before.absent["taisce_formation_oldest_pending_timestamp_seconds"] ==
		stored.absent["taisce_formation_oldest_pending_timestamp_seconds"] {
		t.Fatal("storing a turn did not give the backlog an age")
	}
	if oldest := stored.num(t, "taisce_formation_oldest_pending_timestamp_seconds"); oldest < stamped.Unix() {
		t.Fatalf("the oldest pending turn is dated %d, before this test stored one", oldest)
	}
	if stored.num(t, "taisce_operations_allowed") <= before.num(t, "taisce_operations_allowed") {
		t.Fatalf("an allowed write left the allowed count at %d", stored.num(t, "taisce_operations_allowed"))
	}
	if stored.num(t, "taisce_operations_refused") != before.num(t, "taisce_operations_refused") {
		t.Fatalf("nothing was refused and the refused count went from %d to %d",
			before.num(t, "taisce_operations_refused"), stored.num(t, "taisce_operations_refused"))
	}

	// A refusal the ledger records, and only the refused count moves with it.
	m.manageDo(t, "/projects/retention", map[string]any{"name": "no_such_project_here", "retention_days": 30},
		m.operator, http.StatusNotFound, nil)
	_, body = scrapeMetrics(t, m.manage.URL, m.operator)
	refused := parseExposition(t, body)
	if refused.num(t, "taisce_operations_refused") <= stored.num(t, "taisce_operations_refused") {
		t.Fatalf("an operation was refused and the refused count stayed at %d", refused.num(t, "taisce_operations_refused"))
	}

	// Formation drains the backlog, so the number falls as well as rises and the age goes away
	// again: a constant served from a handler would have passed every assertion above and failed
	// these.
	m.form(t)
	_, body = scrapeMetrics(t, m.manage.URL, m.operator)
	formed := parseExposition(t, body)
	if formed.num(t, "taisce_formation_backlog") != before.num(t, "taisce_formation_backlog") {
		t.Fatalf("the turn formed and the backlog is %d, not the %d it started at",
			formed.num(t, "taisce_formation_backlog"), before.num(t, "taisce_formation_backlog"))
	}
	if !formed.absent["taisce_formation_oldest_pending_timestamp_seconds"] {
		t.Fatalf("the backlog drained and still reports an oldest turn: %v", formed.numbers)
	}
	// Draining a scope is not a heartbeat: the heartbeat is written by the driver that runs the
	// passes, not by the drain itself, so forming a turn here leaves the worker series exactly
	// where they were. Asserting that is worth more than it looks — it says these two numbers
	// report a worker reporting in, and not formation having happened to occur.
	if formed.num(t, "taisce_worker_responsive") != before.num(t, "taisce_worker_responsive") ||
		!formed.absent["taisce_formation_heartbeat_timestamp_seconds"] {
		t.Fatalf("draining a scope moved the worker series, which report a heartbeat and not a formation: %v", formed.numbers)
	}
	// So move the thing they actually measure.
	if err := pg.RecordFormationHealth(context.Background(), m.pool, m.schema, 1, 0); err != nil {
		t.Fatal(err)
	}
	_, body = scrapeMetrics(t, m.manage.URL, m.operator)
	beating := parseExposition(t, body)
	if beating.num(t, "taisce_worker_responsive") != 1 {
		t.Fatalf("a worker has just reported and the instance says %d", beating.num(t, "taisce_worker_responsive"))
	}
	if beating.absent["taisce_formation_heartbeat_timestamp_seconds"] {
		t.Fatal("a worker has just reported and the scrape names no heartbeat")
	}
	if beat := beating.num(t, "taisce_formation_heartbeat_timestamp_seconds"); beat < stamped.Unix() {
		t.Fatalf("the heartbeat is dated %d, before this test ran", beat)
	}
}
