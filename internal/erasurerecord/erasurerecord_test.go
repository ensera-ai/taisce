// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package erasurerecord_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/erasurerecord"
)

func key(t *testing.T, value string) erasurerecord.Key {
	t.Helper()
	k, err := erasurerecord.ParseKey(value)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestATraceNamesNobodyToAReaderWithoutTheKey holds the reason the trace is a keyed digest: the
// log it is written to must not become the list of people who asked to be forgotten.
func TestATraceNamesNobodyToAReaderWithoutTheKey(t *testing.T) {
	held := key(t, strings.Repeat("k", 32))
	other := key(t, strings.Repeat("j", 32))
	const subject = "ada@example.test"
	line := held.Records("request-1", "p1", erasurerecord.KindSubject, []string{subject})[0].Line()

	if strings.Contains(line, subject) || strings.Contains(line, "ada") {
		t.Fatalf("the line written to the log holds the identifier: %s", line)
	}
	// Anybody can hash a guess. Only the key turns a guess into the value in the log.
	if held.Digest("p1", erasurerecord.KindSubject, subject) == other.Digest("p1", erasurerecord.KindSubject, subject) {
		t.Fatal("two keys give one digest, so the digest is not keyed")
	}
	// The same identifier elsewhere is a different value: a trace in one project says nothing of another.
	if held.Digest("p1", erasurerecord.KindSubject, subject) == held.Digest("p2", erasurerecord.KindSubject, subject) {
		t.Fatal("one identifier in two projects gives one digest")
	}
	if held.Digest("p1", erasurerecord.KindSubject, subject) == held.Digest("p1", erasurerecord.KindSource, subject) {
		t.Fatal("a person and a turn that share an identifier give one digest")
	}
	// Parts are length-prefixed, so moving a character between them changes the value.
	if held.Digest("p1", erasurerecord.KindSubject, "x") == held.Digest("p", erasurerecord.KindSubject, "1x") {
		t.Fatal("two different project and identifier pairs give one digest")
	}
	if held.ID() == other.ID() || len(held.ID()) != 16 {
		t.Fatalf("a key's name must tell it from another and reveal nothing: %q %q", held.ID(), other.ID())
	}
	if _, err := erasurerecord.ParseKey(strings.Repeat("k", 31)); err == nil {
		t.Fatal("a key shorter than the minimum was accepted")
	}
}

// TestATraceIsReadBackWhateverTheLogWrappedAroundIt holds the property the replay depends on: the
// lines come back out of a log export as the erasures that wrote them.
func TestATraceIsReadBackWhateverTheLogWrappedAroundIt(t *testing.T) {
	k := key(t, strings.Repeat("k", 40))
	turns := make([]string, 120)
	for i := range turns {
		turns[i] = fmt.Sprintf("turn-%d", i)
	}
	person := k.Records("request-person", "p1", erasurerecord.KindSubject, []string{"subject-1"})
	sources := k.Records("request-turns", "p1", erasurerecord.KindSource, turns)
	if len(person) != 1 || len(sources) != 3 {
		t.Fatalf("one person is one line and 120 turns are three, got %d and %d", len(person), len(sources))
	}
	for _, record := range sources {
		if len(record.Line()) > 8<<10 {
			t.Fatalf("a line of %d bytes will be split by a container runtime", len(record.Line()))
		}
	}

	// As a JSON log line, as a line with a timestamp in front, twice over, and out of order.
	export := strings.Join([]string{
		`{"time":"2026-11-13T10:00:00Z","level":"INFO","msg":"erasure recorded","record":"` + sources[2].Line() + `"}`,
		`2026-11-13T10:00:00.1Z stdout F ` + person[0].Line(),
		`unrelated line`,
		`{"record":"` + sources[0].Line() + `"}` + ` trailing ` + sources[1].Line(),
		`{"record":"` + sources[0].Line() + `"}`,
	}, "\n")
	records, err := erasurerecord.Read(strings.NewReader(export))
	if err != nil {
		t.Fatal(err)
	}
	erasures, err := erasurerecord.Gather(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(erasures) != 2 || erasures[0].RequestID != "request-turns" || erasures[1].RequestID != "request-person" {
		t.Fatalf("two erasures in the order first seen, got %+v", erasures)
	}
	if !erasures[0].Complete || len(erasures[0].Digests) != 120 || erasures[0].Kind != erasurerecord.KindSource || erasures[0].KeyID != k.ID() {
		t.Fatalf("the erasure of 120 turns did not come back whole: complete=%v digests=%d", erasures[0].Complete, len(erasures[0].Digests))
	}
	if !erasures[1].Digests[k.Digest("p1", erasurerecord.KindSubject, "subject-1")] {
		t.Fatal("the person's digest is not in the trace read back")
	}

	// A trace the log lost a line of is not whole, and says so.
	partial, err := erasurerecord.Read(strings.NewReader(sources[0].Line() + "\n" + sources[2].Line()))
	if err != nil {
		t.Fatal(err)
	}
	if lost, _ := erasurerecord.Gather(partial); len(lost) != 1 || lost[0].Complete {
		t.Fatalf("a trace missing its second line was reported whole: %+v", lost)
	}
}

// TestATraceThatCannotBeReadIsAnErrorAndNotASkippedLine holds the fail-closed half: a marker
// followed by something unreadable is an erasure that would otherwise silently not be repeated.
func TestATraceThatCannotBeReadIsAnErrorAndNotASkippedLine(t *testing.T) {
	k := key(t, strings.Repeat("k", 32))
	good := k.Records("request-1", "p1", erasurerecord.KindSubject, []string{"subject-1"})[0]
	for name, line := range map[string]string{
		"not an encoding": "taisce-erasure-record:v1:A",
		"not a record":    "taisce-erasure-record:v1:bm90IGpzb24",
		"a part missing":  erasurerecord.Record{RequestID: "r", Project: "p1", Kind: "subject", KeyID: "k", Part: 1, Parts: 1}.Line(),
		"an unknown kind": erasurerecord.Record{RequestID: "r", Project: "p1", Kind: "everything", KeyID: "k", Part: 1, Parts: 1, Digests: []string{"d"}}.Line(),
		"a part past end": erasurerecord.Record{RequestID: "r", Project: "p1", Kind: "subject", KeyID: "k", Part: 2, Parts: 1, Digests: []string{"d"}}.Line(),
	} {
		if _, err := erasurerecord.Read(strings.NewReader(good.Line() + "\n" + line)); err == nil || !strings.Contains(err.Error(), "line 2") {
			t.Errorf("%s: expected an error naming the line, got %v", name, err)
		}
	}
	// Two lines that claim one erasure and disagree about it are not put together.
	moved := good
	moved.Project = "p2"
	if _, err := erasurerecord.Gather([]erasurerecord.Record{good, moved}); err == nil {
		t.Error("two records of one erasure naming different projects were put together")
	}
}
