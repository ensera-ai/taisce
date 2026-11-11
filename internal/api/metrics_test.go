// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/inference"
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
	"taisce_admission_refused_total",
}

// expectedDistributions are the families published as histograms, and what each one measures.
//
// The same exact-set argument as above, with one more thing pinned: which family may carry an
// operation. A family that is not about requests has no business naming one.
var expectedDistributions = map[string]struct {
	kind        pg.MeasurementKind
	byOperation bool
}{
	"taisce_operation_duration_milliseconds":      {pg.OperationDuration, true},
	"taisce_formation_turn_duration_milliseconds": {pg.FormationTurnDuration, false},
	"taisce_formation_lag_milliseconds":           {pg.FormationLag, false},
}

// expectedPurposeCounters are the counters published once per purpose a generation model is called
// for. The third and last label this endpoint carries, and closed like the other two.
var expectedPurposeCounters = []string{
	"taisce_model_calls_total",
	"taisce_model_calls_unmetered_total",
	"taisce_model_prompt_tokens_total",
	"taisce_model_completion_tokens_total",
	"taisce_model_replies_cut_total",
}

// purposeSample is one line of a per-purpose counter: the label is a purpose, and nothing else fits.
var purposeSample = regexp.MustCompile(`^(taisce_model_[a-z_]+_total)\{purpose="([a-z]+)"\} ([0-9]+)$`)

// checkPurposeSample holds one such line to the closed set of purposes and of counters.
func checkPurposeSample(t *testing.T, line string) (string, int64) {
	t.Helper()
	m := purposeSample.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("a sample this endpoint must not serve: %q", line)
	}
	declared := false
	for _, name := range expectedPurposeCounters {
		declared = declared || name == m[1]
	}
	known := false
	for _, purpose := range inference.Purposes() {
		known = known || string(purpose) == m[2]
	}
	if !declared || !known {
		t.Fatalf("a counter or a purpose nobody declared: %q", line)
	}
	value, err := strconv.ParseInt(m[3], 10, 64)
	if err != nil {
		t.Fatalf("%q is not an integer: %v", line, err)
	}
	name, _, _ := strings.Cut(line, " ")
	return name, value
}

// distributionSample is one line of a histogram. The whole of what a label set may be is written
// into the pattern: an operation, a bucket edge that is digits or +Inf, both or neither, in that
// order, and nothing else. There is no place in it for a project, a subject or a word.
var distributionSample = regexp.MustCompile(
	`^(taisce_[a-z_]+_milliseconds)_(bucket|sum|count)(?:\{(?:operation="([a-z][a-z0-9_.]*)")?(,)?(?:le="([0-9]+|\+Inf)")?\})? ([0-9]+)$`)

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
	// series holds every histogram line by its full name, label set included, exactly as served.
	series map[string]int64
}

// checkDistributionSample holds one histogram line to what a histogram line may be, and returns its
// full name and value. Every refusal here is about what a label could otherwise carry.
func checkDistributionSample(t *testing.T, line string) (string, int64) {
	t.Helper()
	m := distributionSample.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("a sample this endpoint must not serve: %q", line)
	}
	family, part, operation, comma, le := m[1], m[2], m[3], m[4], m[5]
	want, declared := expectedDistributions[family]
	if !declared {
		t.Fatalf("%q belongs to a family this endpoint does not declare", line)
	}
	if (comma != "") != (operation != "" && le != "") {
		t.Fatalf("a label set is malformed: %q", line)
	}
	if (operation != "") != want.byOperation {
		t.Fatalf("%s carries an operation where it must not, or lacks one where it must: %q", family, line)
	}
	if operation != "" {
		known := false
		for _, o := range api.Operations() {
			known = known || o.Name == operation
		}
		if !known {
			t.Fatalf("a series is labelled with %q, which is not an operation the surface declares: %q", operation, line)
		}
	}
	if (part == "bucket") != (le != "") {
		t.Fatalf("a bucket has no edge, or a total has one: %q", line)
	}
	if le != "" && le != "+Inf" {
		edge, _ := strconv.ParseInt(le, 10, 64)
		known := false
		for _, bound := range pg.Bounds[want.kind] {
			known = known || bound == edge
		}
		if !known {
			t.Fatalf("a bucket edge of %s is not one of the edges declared for %s: %q", le, family, line)
		}
	}
	value, err := strconv.ParseInt(m[6], 10, 64)
	if err != nil {
		t.Fatalf("%q is not an integer: %v", line, err)
	}
	name, _, _ := strings.Cut(line, " ")
	return name, value
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
// "every line is a name and one number, and a label is an operation or a bucket edge", because a
// label set is the one piece of syntax through which a project name, a subject or anybody's words
// could reach a monitoring system. So a line with a brace must be a histogram line, and a histogram
// line is held to a pattern with room for exactly those two labels and to the closed sets their
// values come from. Every check walks the body itself rather than a list of names, so it holds over
// every series the endpoint emits, including ones added after this was written.
func parseExposition(t *testing.T, body string) exposed {
	t.Helper()
	out := exposed{numbers: map[string]int64{}, absent: map[string]bool{}, series: map[string]int64{}}
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
			// What a series is declared as is a promise about how to read it. A window's count
			// declared as a counter would have a rate computed over it that is wrong by however far
			// the window moved; a rising total declared as a gauge would never have one computed.
			_, distribution := expectedDistributions[name]
			want := "gauge"
			switch {
			case distribution:
				want = "histogram"
			case strings.HasSuffix(name, "_total"):
				want = "counter"
			}
			if !ok || kind != want {
				t.Fatalf("%s is declared as %q and is a %s: %q", name, kind, want, line)
			}
			typed[name] = true
		case strings.Contains(line, "_milliseconds_"):
			name, value := checkDistributionSample(t, line)
			if _, seen := out.series[name]; seen {
				t.Fatalf("%s is served twice", name)
			}
			out.series[name] = value
		case strings.HasPrefix(line, "taisce_model_"):
			name, value := checkPurposeSample(t, line)
			if _, seen := out.series[name]; seen {
				t.Fatalf("%s is served twice", name)
			}
			out.series[name] = value
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
	// Each histogram family is declared whether or not anything has been measured yet, so that a
	// monitoring system learns the series exists before it has a value, and each is internally
	// consistent: buckets never fall as the edge rises, and the last one is the count.
	for family, want := range expectedDistributions {
		if !helped[family] || !typed[family] {
			t.Fatalf("%s is not declared with a HELP and a TYPE line", family)
		}
		groups := map[string]bool{}
		for name := range out.series {
			if rest, ok := strings.CutPrefix(name, family+"_count"); ok {
				groups[rest] = true
			}
		}
		for group := range groups {
			prefix := ""
			if group != "" {
				prefix = strings.TrimSuffix(strings.TrimPrefix(group, "{"), "}") + ","
			}
			var previous int64
			for _, bound := range pg.Bounds[want.kind] {
				at, ok := out.series[family+"_bucket{"+prefix+`le="`+strconv.FormatInt(bound, 10)+`"}`]
				if !ok || at < previous {
					t.Fatalf("%s%s: the bucket at %d is missing or below the one before it (%d < %d)", family, group, bound, at, previous)
				}
				previous = at
			}
			total, ok := out.series[family+"_bucket{"+prefix+`le="+Inf"}`]
			if !ok || total < previous || total != out.series[family+"_count"+group] {
				t.Fatalf("%s%s: the last bucket (%d) is not the count (%d)", family, group, total, out.series[family+"_count"+group])
			}
			if _, ok := out.series[family+"_sum"+group]; !ok {
				t.Fatalf("%s%s has a count and no sum", family, group)
			}
		}
	}
	// Every per-purpose counter is served for every purpose, whether or not a call has been made, so
	// the series exist before the first call.
	for _, family := range expectedPurposeCounters {
		if !helped[family] || !typed[family] {
			t.Fatalf("%s is not declared with a HELP and a TYPE line", family)
		}
		for _, purpose := range inference.Purposes() {
			if _, ok := out.series[family+`{purpose="`+string(purpose)+`"}`]; !ok {
				t.Fatalf("%s is not served for %s", family, purpose)
			}
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

// The scrape carries no label drawn from memory, and nothing else from memory either. A project exists, a turn has been
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
	// parseExposition refuses any sample line that is not a name and one number, and any label set
	// that is not an operation the surface declares or a bucket edge, so a per-project label cannot
	// pass it on any series.
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
		if strings.Contains(line, "_milliseconds_") {
			// A histogram line: held to the two labels it may carry and the closed sets their values
			// come from, which is the same argument with a label set allowed in exactly one shape.
			checkDistributionSample(t, line)
			continue
		}
		if strings.HasPrefix(line, "taisce_model_") {
			checkPurposeSample(t, line)
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

// The scrape says how long requests took, by operation, and how long formation took and waited.
//
// Nothing recorded a duration before this. An operator whose agents felt slow had nothing to look
// at, and a backlog said formation was behind without saying whether that was a slow model or a deep
// queue. The numbers are counted in the processes that do the work and read here from the totals
// they write down, so this test drives real requests through the memory door, forms a real turn,
// and reads what the management door then reports.
func TestTheScrapeSaysHowLongRequestsAndFormationTook(t *testing.T) {
	m := newManaged(t, "api_metrics_durations")
	ctx := context.Background()

	// Before anything has been measured the families are declared and hold no series, which is how
	// a monitoring system learns a series exists without being told a number nobody measured.
	_, body := scrapeMetrics(t, m.manage.URL, m.operator)
	for name, value := range parseExposition(t, body).series {
		if strings.Contains(name, "_milliseconds_") || value != 0 {
			t.Fatalf("nothing has been measured and the scrape reports %s = %d", name, value)
		}
	}

	for i := 0; i < 3; i++ {
		m.do(t, http.MethodGet, "/v1/freshness", nil, http.StatusOK, nil)
	}
	// A refused request is a request somebody waited for, and is timed with the rest.
	if status, _ := m.raw(t, http.MethodGet, "/v1/freshness", nil, "not-a-token"); status != http.StatusUnauthorized {
		t.Fatalf("a request with no usable credential was answered %d", status)
	}
	m.do(t, http.MethodPost, "/v1/observations", map[string]any{
		"data_subject_id": "subject-1", "messages": []map[string]any{{"role": "user", "content": "I work at Ensera."}},
	}, http.StatusCreated, nil)
	m.form(t)
	if err := m.measurements.Flush(ctx); err != nil {
		t.Fatalf("write down what was counted: %v", err)
	}

	_, body = scrapeMetrics(t, m.manage.URL, m.operator)
	values := parseExposition(t, body)
	for name, want := range map[string]int64{
		`taisce_operation_duration_milliseconds_count{operation="freshness"}`: 4,
		`taisce_operation_duration_milliseconds_count{operation="observe"}`:   1,
		`taisce_formation_turn_duration_milliseconds_count`:                   1,
		`taisce_formation_lag_milliseconds_count`:                             1,
	} {
		if got, ok := values.series[name]; !ok || got != want {
			t.Errorf("%s is %d (served: %v), want %d", name, got, ok, want)
		}
	}
	// An operation nobody called has no series: a zero for every operation the surface declares
	// would be sixty series saying nothing on every scrape.
	if _, ok := values.series[`taisce_operation_duration_milliseconds_count{operation="recall"}`]; ok {
		t.Error("an operation nobody called is reported as measured")
	}

	// Totals rise and never restart: a second flush with more work adds to what is there, which is
	// what makes a rate over them the rate it says it is.
	m.do(t, http.MethodGet, "/v1/freshness", nil, http.StatusOK, nil)
	if err := m.measurements.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	_, body = scrapeMetrics(t, m.manage.URL, m.operator)
	if got := parseExposition(t, body).series[`taisce_operation_duration_milliseconds_count{operation="freshness"}`]; got != 5 {
		t.Errorf("after one more request the total is %d, want 5", got)
	}
	// Scraping is not itself measured: the scrape is on the management door, and the durations are
	// of memory operations.
	if strings.Contains(body, "metrics") {
		t.Errorf("the scrape reports on itself:\n%s", body)
	}
}

// A request turned away for capacity is counted, and the count is the number turned away.
//
// A refusal for capacity writes no ledger row — a flood could otherwise make the instance write one
// per request — so a process refusing a third of its load looked, on the scrape, exactly like an
// idle one. The count is compared with the 429s the callers actually received, because a counter
// that merely moves is not evidence it counts the right thing.
func TestARequestTurnedAwayForCapacityIsCounted(t *testing.T) {
	m := newManaged(t, "api_metrics_refused")
	ctx := context.Background()

	// Unknown tokens, many at once. Every stranger together may hold one lookup slot, so most of
	// these are refused at the door however fast the registry answers.
	var refused atomic.Int64
	var wg sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				stranger := credential.Prefix + fmt.Sprintf("%021d%022d", worker, i)
				req, err := http.NewRequest(http.MethodGet, m.server.URL+"/v1/freshness", nil)
				if err != nil {
					t.Error(err)
					return
				}
				req.Header.Set("Authorization", "Bearer "+stranger)
				resp, err := m.server.Client().Do(req)
				if err != nil {
					t.Error(err)
					return
				}
				resp.Body.Close()
				if resp.StatusCode == http.StatusTooManyRequests {
					refused.Add(1)
				}
			}
		}(worker)
	}
	wg.Wait()
	if refused.Load() == 0 {
		t.Fatal("640 concurrent requests with unknown tokens were all admitted, so this run cannot show a refusal being counted")
	}
	if err := m.measurements.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	_, body := scrapeMetrics(t, m.manage.URL, m.operator)
	if got := parseExposition(t, body).num(t, "taisce_admission_refused_total"); got != refused.Load() {
		t.Fatalf("the callers received %d refusals for capacity and the scrape counts %d", refused.Load(), got)
	}
}

// A stored measurement the surface cannot vouch for fails the scrape rather than becoming a series.
//
// The totals are written by the serving processes, and the scrape is the last place a value nobody
// declared could be stopped before it reaches a monitoring system. A name that is not a declared
// operation, or buckets that are not this binary's, is therefore an error — and the scrape fails as
// a whole, because a partial one reads as a condition that stopped happening.
func TestAMeasurementTheSurfaceDoesNotDeclareFailsTheScrape(t *testing.T) {
	m := newManaged(t, "api_metrics_undeclared")
	ctx := context.Background()
	buckets := make([]int64, len(pg.Bounds[pg.OperationDuration])+1)

	if _, err := m.pool.Exec(ctx, m.schema.SQL(
		`INSERT INTO {schema}.measurement(kind,name,buckets,sum_ms,count) VALUES('operation_duration','scrapeproj',$1,0,0)`), buckets); err != nil {
		t.Fatal(err)
	}
	if resp, body := scrapeMetrics(t, m.manage.URL, m.operator); resp.StatusCode != http.StatusServiceUnavailable || strings.Contains(body, "scrapeproj") {
		t.Fatalf("a measurement named for something the surface does not declare was served, or its name was: %d %s", resp.StatusCode, body)
	}
	if _, err := m.pool.Exec(ctx, m.schema.SQL(`DELETE FROM {schema}.measurement`)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pool.Exec(ctx, m.schema.SQL(
		`INSERT INTO {schema}.measurement(kind,name,buckets,sum_ms,count) VALUES('operation_duration','recall','{1,2}',0,3)`)); err != nil {
		t.Fatal(err)
	}
	if resp, body := scrapeMetrics(t, m.manage.URL, m.operator); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a measurement with buckets this binary does not use was served: %d %s", resp.StatusCode, body)
	}
	// And the substrate refuses a name that could be read as label syntax, whoever writes it.
	if _, err := m.pool.Exec(ctx, m.schema.SQL(
		`INSERT INTO {schema}.measurement(kind,name,buckets,sum_ms,count) VALUES('operation_duration','x",project="p1',$1,0,0)`), buckets); err == nil {
		t.Fatal("a measurement whose name is label syntax was stored")
	}
}

// The scrape says what generation calls cost, by purpose, and tells unknown from nothing.
//
// A generation call's length is decided by text somebody else wrote, and nothing counted what was
// spent: an operator on a metered provider learned what formation cost from the provider's invoice.
// The counts are kept per purpose because the three have different remedies — a costly extraction is
// about what is being stored, a costly report about how often reports are rewritten.
//
// A call the provider reported no usage for is counted as a call and as unmetered, and adds no
// tokens, so a reader adding up the tokens can see how much of the picture is missing.
func TestTheScrapeSaysWhatGenerationCallsCostByPurpose(t *testing.T) {
	m := newManaged(t, "api_metrics_tokens")
	ctx := context.Background()
	add := func(counter string, purpose inference.Purpose, n int64) {
		m.measurements.Add(pg.PurposeCounter(counter, string(purpose)), n)
	}
	add(pg.CounterModelCalls, inference.PurposeExtraction, 3)
	add(pg.CounterModelPromptTokens, inference.PurposeExtraction, 900)
	add(pg.CounterModelCompletionTokens, inference.PurposeExtraction, 120)
	add(pg.CounterModelCallsUnmetered, inference.PurposeExtraction, 1)
	add(pg.CounterModelCalls, inference.PurposeReport, 1)
	add(pg.CounterModelRepliesCut, inference.PurposeReport, 1)
	if err := m.measurements.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	_, body := scrapeMetrics(t, m.manage.URL, m.operator)
	values := parseExposition(t, body)
	for name, want := range map[string]int64{
		`taisce_model_calls_total{purpose="extraction"}`:             3,
		`taisce_model_calls_unmetered_total{purpose="extraction"}`:   1,
		`taisce_model_prompt_tokens_total{purpose="extraction"}`:     900,
		`taisce_model_completion_tokens_total{purpose="extraction"}`: 120,
		`taisce_model_calls_total{purpose="report"}`:                 1,
		`taisce_model_replies_cut_total{purpose="report"}`:           1,
		`taisce_model_calls_total{purpose="compaction"}`:             0,
	} {
		if got := values.series[name]; got != want {
			t.Errorf("%s is %d, want %d", name, got, want)
		}
	}

	// A stored counter nobody declared fails the scrape, for the reason an undeclared operation
	// does, and its name is not served.
	m.measurements.Add("model_calls.scrapeproject", 1)
	if err := m.measurements.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if resp, body := scrapeMetrics(t, m.manage.URL, m.operator); resp.StatusCode != http.StatusServiceUnavailable || strings.Contains(body, "scrapeproject") {
		t.Fatalf("a counter for a purpose nobody declared was served, or its name was: %d %s", resp.StatusCode, body)
	}
}

// An observer scrapes, and is a stranger everywhere else.
//
// The scrape used to open only for the operator credential, so whatever scraped had to hold the
// credential that can mint a key for any project. An observer is what a monitoring system is given
// instead, and the whole of its value is what it cannot do: every management operation, the portal
// and every memory route are walked here, from the surface's own tables, and each must answer an
// observer exactly as it answers somebody holding nothing.
//
// The tables are walked rather than a list written out, so an operation added later is covered the
// day it is added.
func TestAnObserverScrapesAndIsAStrangerEverywhereElse(t *testing.T) {
	m := newManaged(t, "api_metrics_observer")
	ctx := context.Background()
	credentials := credential.NewStore(m.pool, string(migrate.ControlSchema))
	observer, grant, err := credentials.IssueObserver(ctx, "monitoring")
	if err != nil {
		t.Fatalf("issue an observer: %v", err)
	}

	if resp, body := scrapeMetrics(t, m.manage.URL, observer); resp.StatusCode != http.StatusOK {
		t.Fatalf("an observer was refused the scrape it exists for: %d %s", resp.StatusCode, body)
	} else {
		parseExposition(t, body)
	}
	// The operator still scrapes: an operator may read everything on this surface, and a deployment
	// that scraped with one before observers existed must not stop being monitored by upgrading.
	if resp, body := scrapeMetrics(t, m.manage.URL, m.operator); resp.StatusCode != http.StatusOK {
		t.Fatalf("the operator can no longer scrape: %d %s", resp.StatusCode, body)
	}

	// "The answer a stranger gets" is checked as exactly that: the same route is asked with a
	// well-formed token nobody issued, and the two answers must be the same and must not be a
	// success. Comparing with a real stranger rather than with a status written here means a route
	// this harness does not mount is not mistaken for one that opened.
	nobody := credential.Prefix + strings.Repeat("A", 43)
	stranger := func(what string, status int, body []byte, strangerStatus int, strangerBody []byte) {
		t.Helper()
		if status != strangerStatus || string(body) != string(strangerBody) || status < 400 {
			t.Errorf("%s answered an observer %d %s and a stranger %d %s; the two must be the same refusal",
				what, status, body, strangerStatus, strangerBody)
		}
	}
	for _, o := range api.ManagementOperations() {
		status, body := rawAgainst(t, m.manage.URL, o.Method, api.ManagementPrefix+o.Path, map[string]any{}, observer)
		strangerStatus, strangerBody := rawAgainst(t, m.manage.URL, o.Method, api.ManagementPrefix+o.Path, map[string]any{}, nobody)
		stranger("management operation "+o.Name, status, body, strangerStatus, strangerBody)
		if status != http.StatusUnauthorized {
			t.Errorf("management operation %s answered an observer %d; every management operation is mounted here and must refuse it as unauthenticated", o.Name, status)
		}
	}
	if len(api.ManagementOperations()) < 10 {
		t.Fatalf("only %d management operations were walked; the table this test reads has gone missing", len(api.ManagementOperations()))
	}
	for _, o := range api.Operations() {
		var body map[string]any
		if o.Method != http.MethodGet {
			body = map[string]any{}
		}
		status, raw := m.raw(t, o.Method, "/"+api.Version+o.Path, body, observer)
		strangerStatus, strangerBody := m.raw(t, o.Method, "/"+api.Version+o.Path, body, nobody)
		stranger("memory operation "+o.Name, status, raw, strangerStatus, strangerBody)
	}

	// Nothing an observer was refused is attributed to it as an act: the ledger holds its refusals
	// as it holds a stranger's, and no row names it as having done anything.
	var acted int
	if err := m.pool.QueryRow(ctx, m.schema.SQL(
		`SELECT count(*) FROM {schema}.audit_entry WHERE principal = $1 AND outcome = 'allowed'`), grant.CredentialID).Scan(&acted); err != nil {
		t.Fatal(err)
	}
	if acted != 0 {
		t.Fatalf("the ledger records %d allowed operations by an observer", acted)
	}

	// Revoked, it is a stranger at the scrape too, on the next request.
	if err := credentials.Revoke(ctx, grant.CredentialID); err != nil {
		t.Fatal(err)
	}
	if resp, _ := scrapeMetrics(t, m.manage.URL, observer); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a revoked observer still scrapes: %d", resp.StatusCode)
	}
}
