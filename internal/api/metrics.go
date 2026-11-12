// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// The instance's numbers, in the one format every monitoring system already reads.
//
// # What this is allowed to decide, and what it must not
//
// It decides how an existing number is spelled on the wire, and nothing about what the number is.
// Every value here is read from somewhere that already computes it for the operator's own pages —
// the operational snapshot behind the health report and the ops centre, and the ledger's own count
// of what it allowed and refused. Nothing in this file measures anything, so a metric cannot
// disagree with the page an operator was reading a moment ago, and adding one is not a way to put
// instrumentation on a path that does not have it.
//
// # Why the format is written out by hand
//
// A client library would be a dependency in the write path's process for a format that is a name, a
// space and an integer. The exposition format is small, stable and fully exercised by the test that
// walks every line of this output, so the code is cheaper than the dependency and the dependency
// buys nothing back.
//
// Metrics and no tracing. Spans over memory formation would have to be emitted against a
// convention that does not exist yet — the GenAI semantic conventions have no memory span and no
// release — so every span named now is a span renamed later, and the operator alerting on this
// endpoint needs none of them.
//
// # What a scrape may carry, and the reason it is this narrow
//
// A scrape does not stay where it is taken: it goes to a monitoring system whose access and
// retention are a different trust domain from the operator credential that opened this door. So
// every series here is an instance-wide aggregate, and the exposition writer below has no label
// parameter at all — not an unused one, none — because a per-project label set would make every
// scrape an enumeration of who the instance's customers are, and a label whose values come from
// anybody's words would make a monitoring system a second place memory content lives. Per-project
// detail stays on the management API and the ops centre, behind this same credential, where a
// person asks for it one project at a time.
//
// The ledger holds no words and neither does this. There is no subject, no predicate, no
// credential identifier, no path carrying an id, and nothing derived from stored content.
package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// MetricsPath is the scrape, under the management prefix with every other operator route: these
// are the operator's numbers, and the memory port must not answer them. A project credential
// reaching them would tell every agent holding one how far behind the instance is and how much of
// its capacity is left, which is the shape of the deployment told to a caller who only ever needed
// to write a turn.
const MetricsPath = "/metrics"

// metricPrefix namespaces every series, so an operator scraping this beside anything else can
// select all of it and nothing else with one matcher.
const metricPrefix = "taisce_"

// metricsContentType is the exposition format's own media type, version included. A scrape that
// receives text/plain without it still parses; naming the version is what lets a parser refuse a
// format it does not know rather than guess at one.
const metricsContentType = "text/plain; version=0.0.4; charset=utf-8"

// operationsWindow is how far back the operation counts look.
//
// A bounded window rather than the whole ledger, deliberately. An all-time count would be the
// better shape — a cumulative counter is what a monitoring system differentiates to get a rate, and
// the ledger is append-only so its total never resets — but reading it is a full pass over a table
// that only ever grows, and this endpoint is read by an unattended timer forever. A window is
// served by the ledger's own time index, so a scrape costs what the last five minutes cost and not
// what the instance's whole history costs. The window is published as its own series so that a
// number read off a dashboard carries the conditions it was taken under.
const operationsWindow = 5 * time.Minute

// exposition writes the Prometheus text exposition format.
//
// It is a type rather than three string appends at each call site so that the shape of a sample is
// stated once. There is no parameter that takes a label as a string, which is the boundary this
// file's whole argument rests on — a per-project or per-subject label cannot be added by filling in
// an argument that is already there, only by changing this type, which is a change a reviewer sees
// and the exposition test refuses.
//
// Two labels exist, and neither is a string a caller of this type can supply freely. `le` is a
// bucket's upper edge, taken from a fixed list of integers. `operation` is taken from the surface's
// own operation table and checked against it before it is written. A distribution is how timings
// are published at all; without `le` the only thing that could be published is an average, and an
// average of request times says nothing about the requests an operator is being asked about.
//
// Integers only. A duration is published in milliseconds so that nothing here needs a float: a
// formatter that cannot produce one cannot publish a value with false precision, and a bucket edge
// is a number somebody chose rather than one that was computed.
type exposition struct {
	b strings.Builder
}

// gauge writes one sample: a value that goes up and down and means nothing cumulatively.
//
// Everything here is a gauge, including the operation counts, and none of them is named `_total`.
// A `_total` suffix is a promise that the number only rises from a fixed origin; these are a
// window's counts, and a parser or a person who read the suffix as cumulative would compute a rate
// that is wrong by however much the window moved.
func (e *exposition) gauge(name, help string, value int64) {
	e.b.WriteString("# HELP " + metricPrefix + name + " " + help + "\n")
	e.b.WriteString("# TYPE " + metricPrefix + name + " gauge\n")
	e.b.WriteString(metricPrefix + name + " " + strconv.FormatInt(value, 10) + "\n")
}

// timestamp writes a moment as the seconds since the epoch a monitoring system expects, or NaN
// when the instance has no such moment to report.
//
// NaN rather than a zero, and this is the whole reason the method exists. A zero is 1970, so
// `time() - taisce_formation_oldest_pending_timestamp_seconds` on an empty backlog would read as
// fifty-six years behind and fire the alert the series was added to make possible. Every comparison
// against NaN is false, so an absent moment raises nothing and an operator's rule says what they
// meant it to say.
//
// NaN is written as the literal the format defines, not formatted from a float. There is still no
// float formatter in this file, which is what keeps a timing from being published by accident.
func (e *exposition) timestamp(name, help string, at *time.Time) {
	e.b.WriteString("# HELP " + metricPrefix + name + " " + help + "\n")
	e.b.WriteString("# TYPE " + metricPrefix + name + " gauge\n")
	value := "NaN"
	if at != nil {
		value = strconv.FormatInt(at.Unix(), 10)
	}
	e.b.WriteString(metricPrefix + name + " " + value + "\n")
}

// counter writes a count that only rises, from a total the instance keeps durably.
//
// The one place `_total` appears. A gauge above is a window's count and must not be read as
// cumulative; this is cumulative, kept in the database rather than in a process so that it does not
// fall to zero when one restarts, and a rate over it is the rate it says it is.
func (e *exposition) counter(name, help string, value int64) {
	e.b.WriteString("# HELP " + metricPrefix + name + "_total " + help + "\n")
	e.b.WriteString("# TYPE " + metricPrefix + name + "_total counter\n")
	e.b.WriteString(metricPrefix + name + "_total " + strconv.FormatInt(value, 10) + "\n")
}

// counterByPurpose writes one counter family with a series for every purpose a generation model is
// called for.
//
// The third label, and closed like the other two: a purpose is one of the set the inference package
// declares, each with its own prompt. Every purpose is written whether or not a call has been made
// for it, so the series exist before the first call and a rule written against one does not have to
// handle it appearing.
func (e *exposition) counterByPurpose(name, help string, stored map[string]int64) {
	family := metricPrefix + name + "_total"
	e.b.WriteString("# HELP " + family + " " + help + "\n")
	e.b.WriteString("# TYPE " + family + " counter\n")
	for _, purpose := range inference.Purposes() {
		e.b.WriteString(family + `{purpose="` + string(purpose) + `"} ` +
			strconv.FormatInt(stored[pg.PurposeCounter(name, string(purpose))], 10) + "\n")
	}
}

// declaredCounter reports whether a stored counter's name is one this endpoint publishes.
func declaredCounter(name string) bool {
	if name == pg.CounterAdmissionRefused {
		return true
	}
	for _, counter := range pg.PurposeCounters {
		for _, purpose := range inference.Purposes() {
			if name == pg.PurposeCounter(counter, string(purpose)) {
				return true
			}
		}
	}
	return false
}

// distributions writes one family of histograms: how long a kind of thing took.
//
// Every distribution in the slice is of one kind, and the family is declared once. A name on a
// distribution becomes labels through the family's own labeller, and only if that labeller
// recognises it as one of a set declared in code: a row in the table with any other name is an
// error rather than a series, because the table is written by the serving processes and this is the
// last place a value nobody declared could be stopped before it reaches a monitoring system.
//
// Buckets are cumulative and end with +Inf, which is the count. Totals come from rows that only
// rise, so this is a histogram in the format's own sense and `rate()` over it means what it says.
func (e *exposition) distributions(name, help string, labelled func(string) (string, bool), all []pg.Distribution) error {
	family := metricPrefix + name + "_milliseconds"
	e.b.WriteString("# HELP " + family + " " + help + "\n")
	e.b.WriteString("# TYPE " + family + " histogram\n")
	for _, d := range all {
		label, prefix := "", ""
		if d.Name != "" {
			declared := false
			if label, declared = labelled(d.Name); !declared {
				return fmt.Errorf("a stored measurement of %s is named %q, which is not a name this endpoint declares for it", name, d.Name)
			}
			prefix = label + ","
		}
		for i, bound := range d.Bounds {
			e.b.WriteString(family + "_bucket{" + prefix + `le="` + strconv.FormatInt(bound, 10) + `"} ` +
				strconv.FormatInt(d.Cumulative[i], 10) + "\n")
		}
		e.b.WriteString(family + "_bucket{" + prefix + `le="+Inf"} ` + strconv.FormatInt(d.Count, 10) + "\n")
		braces := ""
		if label != "" {
			braces = "{" + label + "}"
		}
		e.b.WriteString(family + "_sum" + braces + " " + strconv.FormatInt(d.SumMs, 10) + "\n")
		e.b.WriteString(family + "_count" + braces + " " + strconv.FormatInt(d.Count, 10) + "\n")
	}
	return nil
}

// byOperation labels a distribution with a memory operation, if the memory surface declares one by
// that name. The table is the closed set a label may be drawn from.
func byOperation(name string) (string, bool) {
	for _, o := range Operations() {
		if o.Name == name {
			return `operation="` + name + `"`, true
		}
	}
	return "", false
}

// byManagementOperation labels a distribution with a management operation from that surface's
// table, or with one of the two names the portal is timed as.
func byManagementOperation(name string) (string, bool) {
	if name == pg.PortalPage || name == pg.PortalAction {
		return `operation="` + name + `"`, true
	}
	for _, o := range ManagementOperations() {
		if o.Name == name {
			return `operation="` + name + `"`, true
		}
	}
	return "", false
}

// byModelCall labels a distribution with the purpose a model was called for and whether it
// answered. Both are closed: the purposes are the inference package's, the outcomes are two.
func byModelCall(name string) (string, bool) {
	for _, purpose := range inference.TimedPurposes() {
		for _, answered := range []bool{true, false} {
			if name == pg.ModelCallName(string(purpose), answered) {
				outcome := pg.ModelFailed
				if answered {
					outcome = pg.ModelAnswered
				}
				return `purpose="` + string(purpose) + `",outcome="` + outcome + `"`, true
			}
		}
	}
	return "", false
}

// unlabelled is the labeller of a family that has one series. A name on one of its rows is a name
// nobody declared.
func unlabelled(string) (string, bool) { return "", false }

func (e *exposition) String() string { return e.b.String() }

// flag is a condition as a number, because the format has no other way to say one.
func flag(set bool) int64 {
	if set {
		return 1
	}
	return 0
}

// metrics answers a scrape with the instance's aggregate numbers.
//
// Behind operated, like every other management route, so a scrape presents the operator credential
// the way an operator's own call does and a project credential is refused here with the answer a
// stranger gets.
//
// # Why an allowed scrape is not on the ledger
//
// Every other management operation is, and this one deliberately is not. A scrape arrives on a
// timer — thousands of times a day, forever — and the ledger has no delete: recording it would
// grow the table whose rows this endpoint counts, so the operation count would be measuring the
// monitoring of itself. What is recorded is the refusal, by the door above, under the sampling
// budget that bounds what an anonymous caller can make the instance write.
//
// # Why a partial scrape is a failed scrape
//
// The ops centre drops a panel it could not read and renders the rest, because a person is looking
// at it and can see the gap. Nothing is looking at this. A series that silently stops being
// published reads as a condition that stopped happening, so either every number is served or the
// scrape fails and the monitoring system marks the target down, which is the one signal that cannot
// be mistaken for good news.
func (m *ManagementServer) metrics(w http.ResponseWriter, r *http.Request, _ credential.Grant) {
	if m.stores.Health == nil {
		// A deployment wired without the operational reader has no numbers to publish. Refusing the
		// scrape is the honest answer; an empty body would be a target that is up and says nothing
		// is wrong.
		m.log.Error("a metrics scrape arrived at a deployment with no operational health reader")
		writeError(w, http.StatusServiceUnavailable, codeInternal, "the instance's numbers could not be read")
		return
	}
	health, err := m.stores.Health(r.Context())
	if err != nil {
		m.log.Error("metrics: read operational health", "error", err)
		writeError(w, http.StatusServiceUnavailable, codeInternal, "the instance's numbers could not be read")
		return
	}
	// The window's length, not its end: the end is read from the clock that stamped the rows. A
	// scrape that computed it here would miss whatever the database wrote in the interval between
	// the two clocks, every time, and the operations centre reads the same window.
	activity, err := m.stores.Audit.Activity(r.Context(), "", operationsWindow, 1)
	if err != nil {
		m.log.Error("metrics: count the ledger", "error", err)
		writeError(w, http.StatusServiceUnavailable, codeInternal, "the instance's numbers could not be read")
		return
	}

	var e exposition
	// Is the instance behind? The backlog is turns stored and not yet formed, and the limit is what
	// it is measured against: a backlog at its limit is an instance that refuses the next turn, so
	// the two together say whether being behind has started costing a caller anything.
	e.gauge("formation_backlog", "Turns stored across every project and not yet formed.", health.Pending)
	e.gauge("formation_backlog_limit", "Unfinished turns the instance accepts before refusing a write.", health.PendingLimit)
	e.gauge("formation_parked", "Turns the driver gave up on, across every project.", health.Parked)
	// How many projects hold a turn that has been tried and did not form. A count and no names: the
	// instance's own stamps cannot answer it, because any project forming turns moves them while
	// another fails every attempt, and this is the number the operations centre marks its tile on —
	// so an operator alerting on it alerts on the same thing the page would have shown them.
	e.gauge("formation_failing_projects", "Projects holding a turn that has been tried and did not form.", health.FailingProjects)
	// Has a worker answered recently, and how far behind is the oldest thing waiting?
	//
	// The flag is the simple answer, and it carries the instance's threshold rather than the
	// operator's: a worker counts as answering if it reported inside the formation budget plus
	// slack. The two moments are what let an operator who wants their own staleness window compute
	// it, instead of inheriting ours — `time() - heartbeat` against whatever they decide is too
	// long. Both are published because the flag serves whoever does not want to write a rule and
	// the moments serve whoever does.
	e.gauge("worker_responsive", "1 when a worker reported within the formation heartbeat window, 0 otherwise.", flag(health.WorkerResponsive))
	e.timestamp("formation_heartbeat_timestamp_seconds", "When a worker last reported, or NaN if none ever has.", health.HeartbeatAt)
	e.timestamp("formation_oldest_pending_timestamp_seconds", "When the oldest unformed turn was stored, or NaN if the backlog is empty.", health.OldestPendingAt)
	// Database pressure. Sessions in use are reported against the ceiling and the reservation
	// separately, and the headroom is published rather than left to be subtracted: each process
	// sizes its own pool, the chart chooses how many processes and the server sets the ceiling, and
	// the failure this exists to catch is nobody ever comparing the three.
	e.gauge("database_connections", "Client sessions open against this instance's database.", health.DatabaseConnections)
	e.gauge("cluster_connections", "Client sessions open against the whole database server.", health.ClusterConnections)
	e.gauge("connection_limit", "Client sessions the database server permits in total.", health.MaxConnections)
	e.gauge("connection_reserved", "Sessions the database server holds back for superuser recovery.", health.ReservedConnections)
	e.gauge("connection_headroom", "Sessions that could still be opened before the server refuses.", health.ConnectionHeadroom())
	// Is it refusing things? Allowed and refused apart, because the ratio is the signal: a run of
	// refusals against steady traffic is what an attack looks like from outside, and a total that
	// mixes them hides it.
	e.gauge("operations_allowed", "Operations the ledger recorded as allowed in the recent window.", activity.Current.Operations-activity.Current.Refused)
	e.gauge("operations_refused", "Operations the ledger recorded as refused in the recent window.", activity.Current.Refused)
	e.gauge("operations_window_seconds", "How far back the operation counts look.", int64(operationsWindow/time.Second))

	// How long things take. Totals the serving and forming processes add to every few seconds, so
	// these are a little behind the processes and are not an exact count of anything — the ledger
	// is that. They are what says whether the instance is slow, which nothing above can: a backlog
	// says formation is behind, and not whether that is a slow model or a deep queue; an operation
	// count says requests were served, and not how long anybody waited.
	if m.stores.Measured == nil {
		m.log.Error("a metrics scrape arrived at a deployment with no reader for its timings")
		writeError(w, http.StatusServiceUnavailable, codeInternal, "the instance's numbers could not be read")
		return
	}
	measured, err := m.stores.Measured(r.Context())
	if err != nil {
		m.log.Error("metrics: read the measurements", "error", err)
		writeError(w, http.StatusServiceUnavailable, codeInternal, "the instance's numbers could not be read")
		return
	}
	byKind := map[pg.MeasurementKind][]pg.Distribution{}
	for _, d := range measured.Distributions {
		byKind[d.Kind] = append(byKind[d.Kind], d)
	}
	for _, family := range []struct {
		kind       pg.MeasurementKind
		name, help string
		labelled   func(string) (string, bool)
	}{
		{pg.OperationDuration, "operation_duration", "How long a request to a memory operation took, from the door to the response.", byOperation},
		{pg.FormationTurnDuration, "formation_turn_duration", "How long a turn took to form, for turns that formed.", unlabelled},
		{pg.FormationLag, "formation_lag", "How long a turn waited between being stored and being formed.", unlabelled},
		{pg.ModelCallDuration, "model_call_duration", "How long one call to a model took, by purpose and by whether the provider answered.", byModelCall},
		{pg.ManagementDuration, "management_duration", "How long a request to the management surface or the portal took, from the door to the response.", byManagementOperation},
	} {
		if err := e.distributions(family.name, family.help, family.labelled, byKind[family.kind]); err != nil {
			m.log.Error("metrics: write the measurements", "error", err)
			writeError(w, http.StatusServiceUnavailable, codeInternal, "the instance's numbers could not be read")
			return
		}
	}
	// Turned away at the door for want of capacity. No ledger row is written for one, so without
	// this a process refusing a third of its requests looks exactly like an idle one.
	e.counter("admission_refused", "Requests refused because the process had no capacity for them.", measured.Counters[pg.CounterAdmissionRefused])
	// What generation calls cost. A stored counter this endpoint does not declare fails the scrape
	// for the reason an undeclared operation does: the totals are written by the processes that
	// serve, and this is the last place a name nobody reviewed could be stopped.
	for name := range measured.Counters {
		if !declaredCounter(name) {
			m.log.Error("metrics: a stored counter is not one this endpoint declares", "counter", name)
			writeError(w, http.StatusServiceUnavailable, codeInternal, "the instance's numbers could not be read")
			return
		}
	}
	e.counterByPurpose(pg.CounterModelCalls, "Generation calls made, by purpose.", measured.Counters)
	e.counterByPurpose(pg.CounterModelCallsUnmetered, "Generation calls the provider reported no usage for, by purpose.", measured.Counters)
	e.counterByPurpose(pg.CounterModelPromptTokens, "Tokens sent to a generation model, by purpose, where the provider said.", measured.Counters)
	e.counterByPurpose(pg.CounterModelCompletionTokens, "Tokens a generation model wrote, by purpose, where the provider said.", measured.Counters)
	e.counterByPurpose(pg.CounterModelRepliesCut, "Replies that reached a ceiling and were refused whole, by purpose.", measured.Counters)

	w.Header().Set("Content-Type", metricsContentType)
	// A scrape is a reading of now. A cached one is a reading of whenever it was taken, which an
	// operator cannot tell apart from an instance that stopped changing.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(e.String())); err != nil {
		// The status is written, so this cannot become an error response; a scraper sees a short
		// read and treats the target as down.
		m.log.Error("metrics: write the scrape", "error", err)
	}
}
