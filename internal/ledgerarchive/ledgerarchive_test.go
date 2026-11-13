// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package ledgerarchive_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/ledgerarchive"
)

// archived builds a file of two seals the way the store does, and returns its lines and its head.
func archived(t *testing.T) ([]string, []byte) {
	t.Helper()
	var out bytes.Buffer
	w := ledgerarchive.NewWriter(&out)
	previous := make([]byte, 32)
	if err := w.Header(previous, 2, 5); err != nil {
		t.Fatal(err)
	}
	next := int64(1)
	for seal := int64(1); seal <= 2; seal++ {
		var lines []string
		digest := ledgerarchive.NewEntriesDigest()
		from := next
		for ; next < from+int64(seal)+1; next++ {
			line := fmt.Sprintf("%d|recall|credential-1|credential|p1|%d|allowed|1731495600.000000", next, next)
			lines = append(lines, line)
			digest.Add(line)
		}
		chained := ledgerarchive.Chain(previous, digest.Sum())
		if err := w.Seal(ledgerarchive.Seal{ID: seal, FromEntry: from, ToEntry: next - 1, EntryCount: len(lines),
			EntriesDigest: hex.EncodeToString(digest.Sum()), PreviousDigest: hex.EncodeToString(previous),
			Digest: hex.EncodeToString(chained), SealedAt: time.Unix(1731495600, 0).UTC()}); err != nil {
			t.Fatal(err)
		}
		for _, line := range lines {
			if err := w.Entry(line); err != nil {
				t.Fatal(err)
			}
		}
		previous = chained
	}
	return strings.Split(strings.TrimSpace(out.String()), "\n"), previous
}

func verify(t *testing.T, lines []string, head []byte) (ledgerarchive.Verification, error) {
	t.Helper()
	return ledgerarchive.Verify(strings.NewReader(strings.Join(lines, "\n")+"\n"), head)
}

// TestAnArchivedRangeVerifiesAgainstADigestFromOutsideAndNothingElse holds what a file is for: a
// reader with the file and a digest written down elsewhere learns whether the file is what was
// sealed, with no database.
func TestAnArchivedRangeVerifiesAgainstADigestFromOutsideAndNothingElse(t *testing.T) {
	lines, head := archived(t)
	v, err := verify(t, lines, head)
	if err != nil || !v.Valid || v.Seals != 2 || v.Entries != 5 || v.FirstEntry != 1 || v.LastEntry != 5 || !bytes.Equal(v.Head, head) {
		t.Fatalf("a file as the store writes it: %+v %v", v, err)
	}
	// Checked against itself alone it still holds together, and says where it ends.
	if v, err := verify(t, lines, nil); err != nil || !v.Valid || !bytes.Equal(v.Head, head) {
		t.Fatalf("with no outside digest: %+v %v", v, err)
	}
	// A digest from outside that it does not end at is the failure that matters most: a file
	// rebuilt to be consistent with itself is caught here and nowhere else.
	other := bytes.Repeat([]byte{7}, 32)
	if v, err := verify(t, lines, other); err != nil || v.Valid || !strings.Contains(v.Failure, "checked against") {
		t.Fatalf("against a digest it does not end at: %+v %v", v, err)
	}
}

// TestEveryWayAnArchivedRangeCanBeAlteredIsNamed makes each alteration happen and requires the
// verifier to say which seal disagreed and how, because "invalid" tells nobody what happened.
func TestEveryWayAnArchivedRangeCanBeAlteredIsNamed(t *testing.T) {
	lines, head := archived(t)
	// lines: 0 header, 1 seal 1, 2-3 its entries, 4 seal 2, 5-7 its entries.
	without := func(i int) []string {
		return append(append([]string{}, lines[:i]...), lines[i+1:]...)
	}
	replaced := func(i int, old, new string) []string {
		out := append([]string{}, lines...)
		if !strings.Contains(out[i], old) {
			t.Fatalf("line %d does not contain %q: %s", i, old, out[i])
		}
		out[i] = strings.Replace(out[i], old, new, 1)
		return out
	}
	swapped := append([]string{}, lines...)
	swapped[5], swapped[6] = swapped[6], swapped[5]
	for name, tc := range map[string]struct {
		lines []string
		says  string
	}{
		"an entry altered":            {replaced(2, "|1|allowed", "|9|allowed"), "seal 1 covers entries 1-2 and they do not digest"},
		"an entry removed":            {without(6), "seal 2 covers 3 entries and the file holds 2"},
		"an entry from another range": {replaced(2, `"1|recall`, `"4|recall`), "outside the range of seal 1"},
		"entries out of order":        {swapped, "out of order"},
		"a seal removed":              {append(append([]string{}, lines[:1]...), lines[4:]...), "seal 2 does not follow what came before it"},
		"a seal's digest replaced":    {replaced(4, `"digest":"`, `"digest":"00`), ""},
		"the end cut off":             {lines[:4], "its end is missing"},
		"a later seal first":          {append(append(append([]string{}, lines[:1]...), lines[4:]...), lines[1:4]...), "does not follow"},
	} {
		v, err := verify(t, tc.lines, head)
		if tc.says == "" {
			// A digest one character too long is not a digest: malformed, not merely wrong.
			if err == nil {
				t.Errorf("%s: accepted (%+v)", name, v)
			}
			continue
		}
		if err != nil || v.Valid || !strings.Contains(v.Failure, tc.says) {
			t.Errorf("%s: want a failure saying %q, got %+v %v", name, tc.says, v, err)
		}
	}
	// A seal whose recorded digest is well formed and is not what its contents produce.
	wrong := replaced(4, `"digest":"`+hex.EncodeToString(head), `"digest":"`+strings.Repeat("ab", 32))
	if v, err := verify(t, wrong, nil); err != nil || v.Valid || !strings.Contains(v.Failure, "not the one its own contents produce") {
		t.Errorf("a seal with another digest: %+v %v", v, err)
	}
}

// TestAFileThatIsNotAnArchiveIsAnErrorAndNotAVerdict keeps "malformed" apart from "does not
// verify": the first is a wrong file, the second is an accusation.
func TestAFileThatIsNotAnArchiveIsAnErrorAndNotAVerdict(t *testing.T) {
	lines, _ := archived(t)
	for name, file := range map[string]string{
		"an empty file":           "",
		"no header":               strings.Join(lines[1:], "\n"),
		"another version":         `{"taisce_ledger_archive":2,"previous_digest":"` + strings.Repeat("00", 32) + `"}`,
		"a header with no digest": `{"taisce_ledger_archive":1,"previous_digest":"zz"}`,
		"an entry before a seal":  lines[0] + "\n" + lines[2],
		"an entry with no id":     lines[0] + "\n" + lines[1] + "\n" + `{"entry":"recall|x"}`,
		"a line that is neither":  lines[0] + "\n" + `{"something":"else"}`,
		"a seal with no digests":  lines[0] + "\n" + `{"seal":1,"from_entry":1,"to_entry":1,"entry_count":0}`,
	} {
		if v, err := ledgerarchive.Verify(strings.NewReader(file), nil); err == nil {
			t.Errorf("%s: no error (%+v)", name, v)
		}
	}
	// A header alone is well formed and holds nothing, which is a verdict.
	if v, err := ledgerarchive.Verify(strings.NewReader(lines[0]), nil); err != nil || v.Valid || !strings.Contains(v.Failure, "no seal") {
		t.Errorf("a header alone: %+v %v", v, err)
	}
}
