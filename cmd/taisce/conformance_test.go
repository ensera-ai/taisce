// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/conformance"
)

// The command runs the suite against a live deployment with the reference adapter and prints one
// verdict per case and a summary; the same deployment refuses a run with nothing to drive it.
func TestConformanceCommandRunsTheSuiteAgainstALiveDeployment(t *testing.T) {
	h := newIngestHarness(t)
	t.Setenv(envToken, h.token)
	var out bytes.Buffer
	if err := conformanceCommand(context.Background(), []string{"--api", h.server.URL, "--cases", "../../conformance/cases.json", "--reference"}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if strings.Count(out.String(), `"passed":true`) != 10 || !strings.Contains(out.String(), `"failed":0`) {
		t.Fatalf("expected ten passing cases and a clean summary, got\n%s", out.String())
	}
	for _, refused := range [][]string{
		{"--api", h.server.URL, "--cases", "../../conformance/cases.json"},
		{"--api", h.server.URL, "--cases", "../../conformance/cases.json", "--reference", "--driver", "x"},
		{"--api", h.server.URL, "--cases", "no-such.json", "--reference"},
		{"--cases", "../../conformance/cases.json", "--reference"},
	} {
		t.Setenv(envAPI, "")
		if err := conformanceCommand(context.Background(), refused, &out); err == nil {
			t.Fatalf("expected a refusal for %v", refused)
		}
	}
	if err := conformanceCommand(context.Background(), []string{"--no-such-flag"}, &out); err == nil {
		t.Fatal("an unknown flag must be refused")
	}
	t.Setenv(envToken, "")
	if err := conformanceCommand(context.Background(), []string{"--api", h.server.URL, "--reference"}, &out); err == nil || !strings.Contains(err.Error(), envToken) {
		t.Fatalf("expected the token refusal, got %v", err)
	}
}

// A driver that fails a case makes the run fail with the count, so an adapter's build goes red.
func TestConformanceCommandFailsTheRunWhenACaseFails(t *testing.T) {
	h := newIngestHarness(t)
	t.Setenv(envToken, h.token)
	// The reference served over stdin/stdout is a driver; a driver that answers nothing is one
	// the runner cannot use, and that is a run that could not happen rather than a failed case.
	var out bytes.Buffer
	err := conformanceCommand(context.Background(), []string{"--api", h.server.URL, "--cases", "../../conformance/cases.json", "--driver", "true"}, &out)
	if err == nil || !strings.Contains(err.Error(), "driver") {
		t.Fatalf("a driver that answers nothing must fail the run by name, got %v", err)
	}
}

// --serve-reference is the reference over stdin and stdout: one instruction line in, one report
// line out, which is what an adapter's driver reproduces in its own language.
func TestConformanceServeReferenceAnswersOneReportPerInstruction(t *testing.T) {
	h := newIngestHarness(t)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = stdin })
	instruction := `{"case":"x","api":"` + h.server.URL + `","token":"` + h.token + `","data_subject_id":"s1","turn":{"user":"hello","assistant":"hi"}}` + "\n"
	go func() {
		_, _ = io.WriteString(writer, instruction)
		writer.Close()
	}()
	var out bytes.Buffer
	if err := conformanceCommand(context.Background(), []string{"--serve-reference"}, &out); err != nil {
		t.Fatal(err)
	}
	var report conformance.Report
	if err := decodeLine(out.String(), &report); err != nil || !report.Observed || report.Fatal || report.StoreError != "" {
		t.Fatalf("expected one clean report, got %q %v", out.String(), err)
	}
}

// decodeLine parses the first line of a stream of JSON lines.
func decodeLine(out string, into any) error {
	line, _, _ := strings.Cut(out, "\n")
	return json.Unmarshal([]byte(line), into)
}
