// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"golang.org/x/text/width"
)

const envCLIOutput = "TAISCE_OUTPUT"
const envCLIASCII = "TAISCE_ASCII"

type cliTheme struct {
	width, amber, blue, green, red, dim, reset int
	ascii, color                               bool
}

func fancyOutput(out io.Writer) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envCLIOutput))) {
	case "fancy":
		return true
	case "json", "plain":
		return false
	}
	file, ok := out.(*os.File)
	if !ok || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func currentTheme() cliTheme {
	w := 96
	if parsed, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && parsed >= 40 && parsed <= 240 {
		w = parsed
	}
	t := cliTheme{width: w, ascii: os.Getenv(envCLIASCII) != "" || strings.EqualFold(os.Getenv("TERM"), "dumb")}
	t.color = os.Getenv("NO_COLOR") == "" && !t.ascii
	return t
}

func paint(enabled bool, code, value string) string {
	if !enabled {
		return value
	}
	return "\x1b[" + code + "m" + value + "\x1b[0m"
}

func cleanTerminal(value string) string {
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(value, "�") {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteByte(' ')
		} else if !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func runeWidth(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.IsControl(r) {
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianFullwidth, width.EastAsianWide:
		return 2
	default:
		return 1
	}
}

func clipDisplay(value string, limit int) string {
	value = cleanTerminal(value)
	used := 0
	var b strings.Builder
	for _, r := range value {
		n := runeWidth(r)
		if used+n > limit {
			if limit >= 2 {
				return strings.TrimSpace(b.String()) + "…"
			}
			break
		}
		used += n
		b.WriteRune(r)
	}
	return b.String()
}

func displayWidth(value string) int {
	total := 0
	for _, r := range cleanTerminal(value) {
		total += runeWidth(r)
	}
	return total
}

func statusPanel(t cliTheme, title string, lines []string) string {
	panelWidth := min(t.width-4, 76)
	if panelWidth < 36 {
		panelWidth = 36
	}
	tl, tr, bl, br, horizontal, vertical := "╭", "╮", "╰", "╯", "─", "│"
	if t.ascii {
		tl, tr, bl, br, horizontal, vertical = "+", "+", "+", "+", "-", "|"
	}
	title = " " + cleanTerminal(title) + " "
	var b strings.Builder
	b.WriteString("  " + tl + title + strings.Repeat(horizontal, max(0, panelWidth-2-displayWidth(title))) + tr + "\n")
	for _, line := range lines {
		line = clipDisplay(line, panelWidth-4)
		b.WriteString("  " + vertical + " " + line + strings.Repeat(" ", max(0, panelWidth-3-displayWidth(line))) + vertical + "\n")
	}
	b.WriteString("  " + bl + strings.Repeat(horizontal, panelWidth-2) + br + "\n")
	return b.String()
}

func renderHelp(t cliTheme) string {
	brand := paint(t.color, "1;38;5;214", "TAISCE")
	blue := func(s string) string { return paint(t.color, "38;5;75", s) }
	green := func(s string) string { return paint(t.color, "38;5;78", s) }
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s  %s\n", brand, paint(t.color, "38;5;250", "persistent memory with attributable evidence"))
	fmt.Fprintf(&b, "  %s\n\n", paint(t.color, "38;5;244", "observe  →  form  →  PostgreSQL  →  recall"))
	groups := []struct {
		title string
		lines []string
	}{
		{"Run", []string{"serve              API and formation worker", "bootstrap          provision the instance", "health             operational status", "probe              readiness/liveness", "version            what this binary is"}},
		{"Memory", []string{"project            create, list, suspend, resume", "formation          inspect or retry parked turns", "recover            restore facts or chunks", "rebuild            reinterpret retained sources", "ingest             load documents as turns"}},
		{"Semantic", []string{"embeddings         source-message generations", "entity-embeddings  entity candidate generations", "report-embeddings  thematic report generations"}},
		{"Govern", []string{"credential         issue, list or revoke access", "operator           mint a management credential", "artifact           inspect storage limits", "audit              verify or seal the ledger"}},
		{"Check", []string{"conformance        run the adapter suite", "help               this list"}},
	}
	for _, group := range groups {
		fmt.Fprintf(&b, "  %s\n", blue(group.title))
		for _, line := range group.lines {
			fmt.Fprintf(&b, "    %s\n", line)
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "  %s  taisce <command> --help\n", green("next"))
	if t.width < 64 {
		return strings.ReplaceAll(b.String(), "  →  ", " -> ")
	}
	return b.String()
}

func meter(current, total int64, cells int, ascii bool) string {
	if total <= 0 {
		return "unknown"
	}
	if current > total {
		current = total
	}
	filled := int(current * int64(cells) / total)
	on, off := "█", "░"
	if ascii {
		on, off = "#", "."
	}
	return strings.Repeat(on, filled) + strings.Repeat(off, cells-filled) + fmt.Sprintf(" %d/%d", current, total)
}

func renderHealth(t cliTheme, h pg.OperationalHealth, inferenceConfigured bool) string {
	state, stateColor := "healthy", "38;5;78"
	if !h.WorkerResponsive || h.Pending >= h.PendingLimit {
		state, stateColor = "degraded", "38;5;203"
	}
	worker := "responsive"
	if !h.WorkerResponsive {
		worker = "stale or absent"
	}
	inferenceState := "not configured"
	if inferenceConfigured {
		inferenceState = "configured"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s  instance %s\n", paint(t.color, "1;38;5;214", "TAISCE"), paint(t.color, stateColor, state))
	b.WriteString(statusPanel(t, "memory path", []string{
		fmt.Sprintf("%-12s %s", "formation", cleanTerminal(worker)),
		fmt.Sprintf("%-12s %s", "backlog", meter(h.Pending, h.PendingLimit, 18, t.ascii)),
		fmt.Sprintf("%-12s %d parked · %d formed · %d failed", "work", h.Parked, h.FormedCount, h.FailedAttempts),
		fmt.Sprintf("%-12s %d instance · %d cluster · %d max", "PostgreSQL", h.DatabaseConnections, h.ClusterConnections, h.MaxConnections),
		fmt.Sprintf("%-12s %s", "inference", inferenceState),
	}))
	fmt.Fprintf(&b, "\n  %s  taisce formation parked --project <name>\n", paint(t.color, "38;5;75", "inspect"))
	return b.String()
}

func renderRebuild(t cliTheme, job pg.RebuildJob) string {
	processed := job.Rebuilt + job.Skipped
	total := job.Through + 1
	statusColor := "38;5;75"
	if job.Status == "completed" {
		statusColor = "38;5;78"
	} else if job.Status == "cancelled" || job.CancelRequested {
		statusColor = "38;5;214"
	}
	return fmt.Sprintf("\n  %s  rebuild %s\n  %-12s %s\n  %-12s %d rebuilt · %d skipped\n  %-12s %s\n\n",
		paint(t.color, "1;38;5;214", "TAISCE"), paint(t.color, statusColor, cleanTerminal(job.Status)),
		"progress", meter(processed, total, 22, t.ascii), "sources", job.Rebuilt, job.Skipped,
		"next", clipDisplay("taisce rebuild status --project <name> --key "+job.ID, max(20, t.width-16)))
}

// A report rewrite has no job to point at afterwards, and no progress bar worth drawing: the count
// that matters is how many subjects are still waiting, which this command reports as staleness beside
// its own result. So the next step is to read that, which is the JSON form of this same call.
func renderReportRebuild(t cliTheme, r reportRebuildResult) string {
	outcome, colour := "reports written", "38;5;78"
	if r.Written == 0 && r.Failed == 0 {
		outcome, colour = "nothing was missing", "38;5;75"
	} else if r.Failed > 0 {
		outcome, colour = "some reports were refused", "38;5;214"
	}
	return fmt.Sprintf("\n  %s  %s\n  %-12s %d written · %d failed\n  %-12s %d over %d entities\n  %-12s %s\n\n",
		paint(t.color, "1;38;5;214", "TAISCE"), paint(t.color, colour, outcome),
		"reports", r.Written, r.Failed, "subjects", r.Communities, r.Entities,
		"next", clipDisplay("TAISCE_OUTPUT=json taisce rebuild reports --project "+cleanTerminal(r.Project), max(20, t.width-16)))
}

func renderFailure(t cliTheme, _ error) string {
	return fmt.Sprintf("\n  %s  %s\n  %s\n  %s\n\n", paint(t.color, "1;38;5;214", "TAISCE"),
		paint(t.color, "38;5;203", "command failed"), "The operation did not complete.",
		paint(t.color, "38;5;75", "next  rerun with TAISCE_OUTPUT=json for diagnostics"))
}

func inferenceIsConfigured() bool {
	return strings.TrimSpace(os.Getenv(inference.EnvEmbeddingModel)) != ""
}

// startCLIActivity owns one bounded redraw loop on stderr. The deferred stop restores the line on
// success, failure, or cancellation; structured stdout remains untouched.
func startCLIActivity(ctx context.Context, label string) func() {
	if !fancyOutput(os.Stderr) {
		return func() {}
	}
	label = clipDisplay(label, 40)
	frames := []string{"◐", "◓", "◑", "◒"}
	if currentTheme().ascii {
		frames = []string{"|", "/", "-", "\\"}
	}
	stop := make(chan struct{})
	var once sync.Once
	done := make(chan struct{})
	go func() {
		defer close(done)
		started := time.Now()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		frame := 0
		clear := func() { fmt.Fprint(os.Stderr, "\r"+strings.Repeat(" ", min(currentTheme().width, 80))+"\r") }
		for {
			select {
			case <-ticker.C:
				fmt.Fprintf(os.Stderr, "\r  %s  %s  %s", frames[frame%len(frames)], label, time.Since(started).Round(time.Second))
				frame++
			case <-ctx.Done():
				clear()
				return
			case <-stop:
				clear()
				return
			}
		}
	}()
	return func() {
		once.Do(func() { close(stop) })
		<-done
	}
}
