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
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
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

// clockSlack is how far ahead of this process the database's clock is allowed to be before the
// scrape starts missing what just happened.
//
// A ledger row is stamped by the database, and the window is taken on this process's clock. Where
// the two disagree — and they do, by a hundred milliseconds between a service and its database on
// one developer machine, measured — a row written a moment before the scrape is stamped after the
// window's end and dropped from the count. Over a run of scrapes nothing is lost, because the next
// window covers it, but the number an operator reads is then always missing the most recent
// operations, which are the ones a run of refusals shows up in first.
//
// So the window's end is pushed out by a tolerance and its length kept exactly operationsWindow.
// Five seconds is far more skew than two hosts sharing a clock source ever show and a sixtieth of
// the window, so it costs the reading nothing. What it does not cover: a database clock minutes
// ahead of the service, which this cannot fix and which breaks the ledger's own ordering long
// before it breaks a metric.
//
// This is a mitigation on one caller, not the fix (#169). The fix is to resolve the window where the
// rows are stamped — `clock_timestamp() - interval` inside the statement — so that both ends of it
// come from one clock. That belongs in the ledger's activity read, which this shares with the
// operations centre, and the centre has the same defect for the same reason: a window one caller
// quietly compensates for is a defect that gets forgotten, so it is recorded rather than patched
// here.
const clockSlack = 5 * time.Second

// exposition writes the Prometheus text exposition format.
//
// It is a type rather than three string appends at each call site so that the shape of a sample is
// stated once: a name, no labels, an integer. There is no label parameter, which is the boundary
// this file's whole argument rests on — a per-project or per-subject label cannot be added by
// filling in an argument that is already there, only by changing this type, which is a change a
// reviewer sees and the exposition test refuses.
//
// Integers only. Every number published here is a count, a limit or a flag, so nothing needs a
// float, and a formatter that cannot produce one cannot accidentally publish a timing.
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
	until := time.Now().UTC().Add(clockSlack)
	activity, err := m.stores.Audit.Activity(r.Context(), "", until.Add(-operationsWindow), until, 1)
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
