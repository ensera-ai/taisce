// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"fmt"
	"net/url"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// The overview's activity: the ledger counted over a window the operator chooses, drawn as cards, a
// chart and a ranking. Everything on it is an aggregate of the ledger, which holds no words, so the
// console shows what an instance is doing without reading anything anybody said. This file
// only shapes numbers for the page; the counting is the ledger store's.

// portalRange is one window the overview offers.
type portalRange struct {
	Key, Label string
	// Length is the window; zero is the whole ledger.
	Length time.Duration
	// Buckets is how many bars the chart draws. Chosen per window so a bar is a unit a person thinks
	// in: an hour of a day, a quarter of a day of a week, a day of a month.
	Buckets int
}

// portalRanges are the windows offered. Closed: a query parameter picks one of these, and cannot ask
// the ledger for a window of its own choosing.
var portalRanges = []portalRange{
	{"all", "All time", 0, 40},
	{"24h", "24h", 24 * time.Hour, 24},
	{"7d", "7d", 7 * 24 * time.Hour, 28},
	{"30d", "30d", 30 * 24 * time.Hour, 30},
	{"90d", "90d", 90 * 24 * time.Hour, 45},
}

// portalDefaultRange is a week: long enough to show a pattern, short enough that yesterday is visible.
const portalDefaultRange = "7d"

func pickRange(key string) portalRange {
	for _, r := range portalRanges {
		if r.Key == key {
			return r
		}
	}
	for _, r := range portalRanges {
		if r.Key == portalDefaultRange {
			return r
		}
	}
	return portalRanges[0]
}

// overviewHref is the overview for a window and, optionally, one project.
func overviewHref(rangeKey, project string) string {
	href := "/portal/?range=" + url.QueryEscape(rangeKey)
	if project != "" {
		href += "&project=" + url.QueryEscape(project)
	}
	return href
}

// portalLink is a place the page can go, and whether it is the one being shown.
type portalLink struct {
	Label, Href string
	Current     bool
}

// portalSwitch moves between projects without leaving the page's kind: the overview narrows to one,
// the ledger filters to one, and a project page becomes another's.
type portalSwitch struct {
	Label string
	Items []portalLink
}

func switcher(names []string, current string, href func(string) string) *portalSwitch {
	out := &portalSwitch{Label: "All projects",
		Items: []portalLink{{Label: "All projects", Href: href(""), Current: current == ""}}}
	for _, name := range names {
		out.Items = append(out.Items, portalLink{Label: name, Href: href(name), Current: name == current})
		if name == current {
			out.Label = name
		}
	}
	return out
}

type portalActivity struct {
	Project  string
	Ranges   []portalLink
	Refresh  string
	Updated  string
	Compared string
	// The four numbers an operator reads first.
	Turns, Recalls, Refused, Erasures portalCard
	Total, TotalRefused               int64
	Bars                              []portalBar
	Axis                              []string
	// RankTitle and Ranking are the busiest projects across the instance, or the busiest operations
	// within one project when the page is narrowed to it.
	RankTitle  string
	Ranking    []portalRank
	Operations []pg.OperationActivity
}

type portalCard struct {
	Value     int64
	Detail    string
	Trend     string
	Direction string
}

// portalBar is one bucket as heights: percentages of the busiest bucket, so the chart's scale is the
// window's own and an idle week does not draw as a flat line at the bottom of somebody else's scale.
type portalBar struct {
	Allowed, Refused int
	Title            string
}

type portalRank struct {
	Label, Href         string
	Operations, Refused int64
	Share               int
}

func newPortalActivity(rng portalRange, project string, a pg.Activity, now time.Time) *portalActivity {
	out := &portalActivity{
		Project: project,
		Refresh: overviewHref(rng.Key, project),
		Updated: now.UTC().Format("15:04:05 UTC"),
		Total:   a.Current.Operations, TotalRefused: a.Current.Refused,
		Operations: a.Operations,
	}
	for _, r := range portalRanges {
		out.Ranges = append(out.Ranges, portalLink{Label: r.Label, Href: overviewHref(r.Key, project), Current: r.Key == rng.Key})
	}
	if a.HasPrevious {
		out.Compared = "vs the previous " + rng.Label
	}
	card := func(current, previous int64, upIsBad bool, detail string) portalCard {
		text, direction := trend(current, previous, a.HasPrevious, upIsBad)
		return portalCard{Value: current, Detail: detail, Trend: text, Direction: direction}
	}
	out.Turns = card(a.Current.TurnsStored, a.Previous.TurnsStored, false, "turns written by observations")
	out.Recalls = card(a.Current.Recalls, a.Previous.Recalls, false, "recall and context requests served")
	out.Refused = card(a.Current.Refused, a.Previous.Refused, true, "operations the surface refused")
	out.Erasures = card(a.Current.Erasures, a.Previous.Erasures, false, fmt.Sprintf("%d rows removed", a.Current.ErasedRows))

	var peak int64
	for _, b := range a.Series {
		peak = max(peak, b.Allowed+b.Refused)
	}
	for _, b := range a.Series {
		bar := portalBar{Title: fmt.Sprintf("%s · %d allowed, %d refused", bucketLabel(b.Start, a.Bucket), b.Allowed, b.Refused)}
		if peak > 0 {
			bar.Allowed, bar.Refused = int(b.Allowed*100/peak), int(b.Refused*100/peak)
		}
		out.Bars = append(out.Bars, bar)
	}
	if n := len(a.Series); n > 0 {
		out.Axis = []string{bucketLabel(a.Series[0].Start, a.Bucket), bucketLabel(a.Series[n/2].Start, a.Bucket), "now"}
	}

	if project == "" {
		out.RankTitle = "Busiest projects"
		for _, p := range a.Projects {
			out.Ranking = append(out.Ranking, portalRank{Label: p.Project, Href: overviewHref(rng.Key, p.Project),
				Operations: p.Operations, Refused: p.Refused})
		}
	} else {
		out.RankTitle = "Busiest operations"
		for i, o := range a.Operations {
			if i == 6 {
				break
			}
			out.Ranking = append(out.Ranking, portalRank{Label: o.Operation, Operations: o.Allowed + o.Refused, Refused: o.Refused})
		}
	}
	var top int64
	for _, r := range out.Ranking {
		top = max(top, r.Operations)
	}
	for i := range out.Ranking {
		out.Ranking[i].Share = share(out.Ranking[i].Operations, top)
	}
	return out
}

// trend says how a number moved against the window before, and whether that is worth a colour.
//
// A percentage, because "+40" means nothing without the number it grew from. A count that grew
// from nothing is "new" rather than an infinite percentage. Only refusals are coloured as good or
// bad: more refusals is what an attack looks like from here, while more turns stored is only more
// use, and colouring it would be the console having an opinion it cannot defend.
func trend(current, previous int64, has, upIsBad bool) (string, string) {
	switch {
	case !has:
		return "", ""
	case current == previous:
		return "no change", "flat"
	case previous == 0:
		return "new", direction(true, upIsBad)
	}
	pct := (current - previous) * 100 / previous
	text := fmt.Sprintf("%+d%%", pct)
	if pct == 0 {
		text = "under 1%"
	}
	return text, direction(current > previous, upIsBad)
}

func direction(up, upIsBad bool) string {
	switch {
	case up && upIsBad:
		return "up bad"
	case up:
		return "up"
	case upIsBad:
		return "down good"
	default:
		return "down"
	}
}

// bucketLabel names a bucket at the precision its width needs: an hour needs the time, a day does not.
func bucketLabel(start time.Time, width time.Duration) string {
	if width < 24*time.Hour {
		return start.UTC().Format("Jan 2 15:04")
	}
	return start.UTC().Format("Jan 2")
}
