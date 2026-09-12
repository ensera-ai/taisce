package api

import (
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

// A trend is a percentage against the window before, "new" when there was nothing before, and is
// coloured good or bad only where the direction means something: refusals.
func TestATrendSaysHowANumberMovedAndColoursOnlyRefusals(t *testing.T) {
	for _, c := range []struct {
		current, previous int64
		has, upIsBad      bool
		text, direction   string
	}{
		{5, 3, false, false, "", ""},
		{4, 4, true, false, "no change", "flat"},
		{3, 0, true, false, "new", "up"},
		{3, 0, true, true, "new", "up bad"},
		{150, 100, true, false, "+50%", "up"},
		{50, 100, true, false, "-50%", "down"},
		{50, 100, true, true, "-50%", "down good"},
		{120, 100, true, true, "+20%", "up bad"},
		{1000, 1001, true, false, "under 1%", "down"},
	} {
		text, direction := trend(c.current, c.previous, c.has, c.upIsBad)
		if text != c.text || direction != c.direction {
			t.Errorf("trend(%d, %d, %v, %v) = %q %q, want %q %q", c.current, c.previous, c.has, c.upIsBad, text, direction, c.text, c.direction)
		}
	}
}

// A window is one the page offers or the default; a query cannot name one of its own.
func TestAWindowIsOneThePageOffers(t *testing.T) {
	for key, want := range map[string]string{"24h": "24h", "all": "all", "90d": "90d", "": "7d", "1y": "7d", "'; drop": "7d"} {
		if got := pickRange(key).Key; got != want {
			t.Errorf("pickRange(%q) = %q, want %q", key, got, want)
		}
	}
	if got := overviewHref("7d", "a b"); got != "/portal/?range=7d&project=a+b" {
		t.Fatalf("overviewHref escaped nothing: %q", got)
	}
}

// The chart draws in the window's own scale and names a bucket at the precision its width needs; the
// ranking within one project is its busiest operations, at most six, scaled to the busiest.
func TestTheActivityIsShapedForThePageFromTheLedgersCounts(t *testing.T) {
	now := time.Date(2026, 9, 11, 18, 0, 0, 0, time.UTC)
	idle := newPortalActivity(pickRange("24h"), "", pg.Activity{
		Bucket: time.Hour, Series: []pg.ActivityBucket{{Start: now.Add(-2 * time.Hour)}, {Start: now.Add(-time.Hour)}},
	}, now)
	if idle.Bars[0].Allowed != 0 || idle.Bars[1].Refused != 0 || idle.Compared != "" || idle.RankTitle != "Busiest projects" {
		t.Fatalf("an idle window is not flat, or compares with nothing: %+v", idle)
	}
	if !strings.HasPrefix(idle.Bars[0].Title, "Sep 11 16:00") || idle.Axis[2] != "now" {
		t.Fatalf("an hourly bucket is not named by its hour: %q %v", idle.Bars[0].Title, idle.Axis)
	}
	if got := bucketLabel(now, 24*time.Hour); got != "Sep 11" {
		t.Fatalf("a daily bucket is named %q", got)
	}

	var operations []pg.OperationActivity
	for i := 0; i < 8; i++ {
		operations = append(operations, pg.OperationActivity{Operation: string(rune('a' + i)), Allowed: int64(80 - 10*i)})
	}
	busy := newPortalActivity(pickRange("7d"), "p1", pg.Activity{
		HasPrevious: true, Bucket: 6 * time.Hour, Operations: operations,
		Current:  pg.ActivityTotals{Operations: 10, Refused: 4, Erasures: 1, ErasedRows: 7},
		Previous: pg.ActivityTotals{Refused: 2},
		Series:   []pg.ActivityBucket{{Start: now, Allowed: 4, Refused: 4}, {Start: now, Allowed: 2}},
	}, now)
	if busy.RankTitle != "Busiest operations" || len(busy.Ranking) != 6 || busy.Ranking[0].Share != 100 || busy.Ranking[5].Share != 37 {
		t.Fatalf("one project's ranking is not its six busiest operations to scale: %+v", busy.Ranking)
	}
	if busy.Bars[0].Allowed != 50 || busy.Bars[0].Refused != 50 || busy.Bars[1].Allowed != 25 {
		t.Fatalf("bars are not in the window's own scale: %+v", busy.Bars)
	}
	if busy.Refused.Trend != "+100%" || busy.Refused.Direction != "up bad" || busy.Erasures.Detail != "7 rows removed" || busy.Compared != "vs the previous 7d" {
		t.Fatalf("the cards misread the window: %+v %+v %q", busy.Refused, busy.Erasures, busy.Compared)
	}
	for _, r := range busy.Ranges {
		if r.Current != (r.Label == "7d") || !strings.Contains(r.Href, "project=p1") {
			t.Fatalf("a window link lost its place or its project: %+v", r)
		}
	}
}

// The switcher lists every project and all of them, marks the one shown, and says which it is.
func TestTheSwitcherMarksTheProjectShown(t *testing.T) {
	s := switcher([]string{"p1", "p2"}, "p2", func(n string) string { return "/x/" + n })
	if s.Label != "p2" || len(s.Items) != 3 || s.Items[0].Label != "All projects" || s.Items[0].Current || !s.Items[2].Current || s.Items[1].Href != "/x/p1" {
		t.Fatalf("switcher: %+v", s)
	}
	if all := switcher([]string{"p1"}, "", func(string) string { return "" }); all.Label != "All projects" || !all.Items[0].Current {
		t.Fatalf("an unnarrowed switcher: %+v", all)
	}
}
