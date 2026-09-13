// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

func TestCLIVisualsAdaptWithoutLosingMeaning(t *testing.T) {
	for _, tc := range []struct {
		name  string
		theme cliTheme
		want  []string
	}{
		{"wide color", cliTheme{width: 110, color: true}, []string{"TAISCE", "PostgreSQL", "entity-embeddings", "\x1b["}},
		{"narrow ascii", cliTheme{width: 48, ascii: true}, []string{"TAISCE", "observe -> form", "audit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderHelp(tc.theme)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %q in %q", want, got)
				}
			}
			if tc.theme.ascii && strings.Contains(got, "\x1b[") {
				t.Fatal("ASCII fallback contained ANSI escapes")
			}
		})
	}
}

func TestCLIStatusRendersHealthyDegradedCompletedAndCancelledStates(t *testing.T) {
	theme := cliTheme{width: 80, ascii: true}
	healthy := renderHealth(theme, pg.OperationalHealth{Pending: 2, PendingLimit: 10, WorkerResponsive: true,
		DatabaseConnections: 2, ClusterConnections: 3, MaxConnections: 100}, true)
	if !strings.Contains(healthy, "instance healthy") || !strings.Contains(healthy, "###............... 2/10") || !strings.Contains(healthy, "configured") {
		t.Fatalf("healthy status %q", healthy)
	}
	degraded := renderHealth(theme, pg.OperationalHealth{Pending: 10, PendingLimit: 10}, false)
	if !strings.Contains(degraded, "instance degraded") || !strings.Contains(degraded, "stale or absent") || !strings.Contains(degraded, "not configured") {
		t.Fatalf("degraded status %q", degraded)
	}
	for _, status := range []string{"active", "completed", "cancelled"} {
		got := renderRebuild(theme, pg.RebuildJob{ID: "job\x1b[31m", Status: status, Through: 9, Rebuilt: 4, Skipped: 1,
			CancelRequested: status == "cancelled"})
		if !strings.Contains(got, "rebuild "+status) || !strings.Contains(got, "5/10") || strings.Contains(got, "\x1b[31m") {
			t.Fatalf("%s rebuild %q", status, got)
		}
	}
	// A report rewrite says which of three things happened, because "0 written" means something
	// different when nothing was missing than when the model refused everything it was asked.
	for _, tc := range []struct {
		name, want string
		result     reportRebuildResult
	}{
		{"wrote some", "reports written", reportRebuildResult{Communities: 3, Entities: 9, Written: 3}},
		{"nothing missing", "nothing was missing", reportRebuildResult{Communities: 3, Entities: 9}},
		{"some refused", "some reports were refused", reportRebuildResult{Communities: 3, Entities: 9, Written: 1, Failed: 2}},
	} {
		got := renderReportRebuild(theme, tc.result)
		if !strings.Contains(got, tc.want) || !strings.Contains(got, "3 over 9 entities") {
			t.Fatalf("%s: %q", tc.name, got)
		}
	}
	// A project name reaches this from a flag, so it is cleaned before it is printed: a terminal
	// escape in a name would otherwise repaint an operator's screen from a string they typed.
	if got := renderReportRebuild(theme, reportRebuildResult{Project: "p1\x1b[31m"}); strings.Contains(got, "\x1b[31m") {
		t.Fatalf("a project name repainted the terminal: %q", got)
	}
}

func TestCLIOutputModesAndDynamicTextAreSafe(t *testing.T) {
	var out bytes.Buffer
	t.Setenv(envCLIOutput, "fancy")
	if !fancyOutput(&out) {
		t.Fatal("explicit fancy output was ignored")
	}
	t.Setenv(envCLIOutput, "json")
	if fancyOutput(&out) {
		t.Fatal("explicit JSON output became decorative")
	}
	clean := cleanTerminal("project\x1b[31m\nsecret\tname")
	if clean != "project[31m secret name" || strings.ContainsRune(clean, '\x1b') {
		t.Fatalf("unsafe terminal text %q", clean)
	}
	failure := renderFailure(cliTheme{width: 40, ascii: true}, errors.New("bad\x1b[2J\nnext"))
	if strings.ContainsRune(failure, '\x1b') || !strings.Contains(failure, "command failed") {
		t.Fatalf("unsafe failure %q", failure)
	}
}

func TestDisplayClippingCountsWideAndCombiningRunes(t *testing.T) {
	if got := clipDisplay("A界e\u0301Z", 4); got != "A界e\u0301…" {
		t.Fatalf("wide clipping %q", got)
	}
}

func TestCLIThemeConfigurationAndCancelledActivity(t *testing.T) {
	t.Setenv("COLUMNS", "52")
	t.Setenv(envCLIASCII, "1")
	t.Setenv("NO_COLOR", "1")
	theme := currentTheme()
	if theme.width != 52 || !theme.ascii || theme.color {
		t.Fatalf("theme %+v", theme)
	}
	t.Setenv("COLUMNS", "invalid")
	if currentTheme().width != 96 {
		t.Fatal("invalid width did not use the safe default")
	}
	t.Setenv("TAISCE_INFERENCE_EMBEDDING_MODEL", "embedder")
	if !inferenceIsConfigured() {
		t.Fatal("configured inference was hidden")
	}
	t.Setenv(envCLIOutput, "fancy")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stop := startCLIActivity(ctx, "safe\x1b[31m activity")
	stop()
}

// TestTheProgressMeterRefusesToInventProgress holds the two cases where a meter would otherwise
// print something untrue.
//
// A meter is read as a fact about how far along something is, so the interesting inputs are the ones
// where there is no such fact. A total of zero is not "nothing done" — a division by it is a panic,
// and treating it as a full or empty bar would state a completion nobody measured; it says
// `unknown`. A count past the total happens when a pass is re-driven over work already counted, and
// a bar wider than its own track is a rendering bug dressed as progress; the count is clamped.
//
// Both are one-line branches, which is exactly why they are worth a named test: nothing else in the
// suite ever passes a zero total, so the first caller who does would find out in front of an
// operator.
func TestTheProgressMeterRefusesToInventProgress(t *testing.T) {
	if got := meter(3, 0, 10, true); got != "unknown" {
		t.Fatalf("a meter with no total said %q, and it cannot know", got)
	}
	if got := meter(0, -1, 10, true); got != "unknown" {
		t.Fatalf("a negative total said %q", got)
	}
	over := meter(99, 10, 10, true)
	if over != "########## 10/10" {
		t.Fatalf("a count past the total rendered %q, wanted it clamped to the total", over)
	}
}

// TestANarrowTerminalStillGetsAPanelWideEnoughToRead pins the floor under the panel width.
//
// The width comes from the terminal, and a terminal can be narrower than the content is legible in.
// Without the floor the box characters and padding consume the line and the panel degrades into
// unreadable fragments — worse than a panel that overflows, because overflow is obvious and
// fragments look like corruption.
func TestANarrowTerminalStillGetsAPanelWideEnoughToRead(t *testing.T) {
	narrow := statusPanel(cliTheme{width: 10, ascii: true}, "state", []string{"one"})
	widest := 0
	for _, line := range strings.Split(strings.TrimRight(narrow, "\n"), "\n") {
		if n := displayWidth(line); n > widest {
			widest = n
		}
	}
	if widest < 36 {
		t.Fatalf("a 10-column terminal produced a %d-column panel; the floor is 36", widest)
	}
}

// TestClippingBelowTwoColumnsDropsTheMarkerRatherThanTheContent is the limit at which the marker
// stops being worth a column.
//
// The limit bounds the CONTENT and the marker is added beyond it — `clipDisplay("A界é Z", 4)` above
// returns four columns of value plus the `…`. That convention breaks down at one column: a marker
// there would be as wide as everything it was appended to, and a reader would see a bare `…`
// standing in for a value that was one character long. So under two columns the marker is dropped
// and the content keeps the space.
func TestClippingBelowTwoColumnsDropsTheMarkerRatherThanTheContent(t *testing.T) {
	if got := clipDisplay("abcdef", 1); got != "a" {
		t.Fatalf("clipping to one column produced %q, wanted the column spent on content", got)
	}
	if got := clipDisplay("abcdef", 2); got != "ab…" {
		t.Fatalf("clipping to two columns produced %q, wanted two columns and the marker", got)
	}
}

// TestEveryLineOfAPanelEndsInTheSameColumn holds the panel to its own geometry.
//
// A panel is a claim that its lines belong together, and the border is how a reader sees that. A
// right edge that wanders reads as corruption, and on a status screen corruption is the one thing an
// operator must not have to wonder about. So every line of the panel — top border, rows, bottom
// border — is the same display width, in both glyph sets and at narrow, ordinary and wide terminals.
//
// It also holds the label column. The rows are built with the label padded to twelve cells so the
// values line up; the text sanitiser used to collapse that padding, and the title's surrounding
// spaces with it, which made the top border two cells wider than the rows. The minimum-width test
// above passed on that defect because the widest line was the misdrawn one — which is why this test
// compares lines to each other rather than to a number.
func TestEveryLineOfAPanelEndsInTheSameColumn(t *testing.T) {
	health := pg.OperationalHealth{Pending: 2, PendingLimit: 10, Parked: 1, FormedCount: 3,
		DatabaseConnections: 2, ClusterConnections: 3, MaxConnections: 100, WorkerResponsive: true}
	borders := []string{"╭", "│", "╰", "+", "|"}
	for _, ascii := range []bool{false, true} {
		for _, width := range []int{40, 70, 120} {
			rendered := renderHealth(cliTheme{width: width, ascii: ascii}, health, true)
			var panel []string
			for _, line := range strings.Split(rendered, "\n") {
				trimmed := strings.TrimLeft(line, " ")
				for _, b := range borders {
					if strings.HasPrefix(trimmed, b) {
						panel = append(panel, line)
						break
					}
				}
			}
			if len(panel) < 3 {
				t.Fatalf("ascii=%v width=%d: found %d panel lines in %q", ascii, width, len(panel), rendered)
			}
			want := displayWidth(panel[0])
			for _, line := range panel[1:] {
				if got := displayWidth(line); got != want {
					t.Fatalf("ascii=%v width=%d: a line is %d cells where the top border is %d:\n%s",
						ascii, width, got, want, strings.Join(panel, "\n"))
				}
			}
			for _, label := range []string{"formation", "backlog", "work", "PostgreSQL", "inference"} {
				if !strings.Contains(rendered, fmt.Sprintf("%-12s ", label)) {
					t.Fatalf("ascii=%v width=%d: the %q label lost its column:\n%s", ascii, width, label, rendered)
				}
			}
		}
	}
}
