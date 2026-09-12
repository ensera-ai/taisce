// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
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
