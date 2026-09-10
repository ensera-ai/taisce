// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package ingest_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/ingest"
)

func write(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The segments are the document: their concatenation is the text, none is empty, none exceeds
// the ceiling, and every cut is on a code-point boundary. A quote found in the document is found
// whole in exactly one segment at the position the segment's offset predicts, in Latin and in
// Arabic, which is what makes a citation into a segment a citation into the file.
func TestSegmentsAreTheDocumentAndQuotePositionsRoundTrip(t *testing.T) {
	paragraphs := []string{
		"Marta works at Ensera. She lives in Dublin and prefers written documentation to calls.",
		"قالت مرتا إنها تعمل في إنسيرا. وهي تعيش في دبلن وتفضّل التوثيق المكتوب على المكالمات.",
		"The office moves in March! Will the lease be signed? Nobody has said.",
		"第三段落。办公室三月搬迁！租约签了吗？还没有人说。",
	}
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString(paragraphs[i%len(paragraphs)])
		b.WriteString("\n\n")
	}
	text := strings.TrimRight(b.String(), "\n")
	segments, err := ingest.Split(text, ingest.MinSegmentBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) < 10 {
		t.Fatalf("expected many segments at the smallest ceiling, got %d", len(segments))
	}
	var joined strings.Builder
	for i, s := range segments {
		if s.Ordinal != i || s.Text != text[s.ByteStart:s.ByteEnd] || s.ByteEnd-s.ByteStart > ingest.MinSegmentBytes || strings.TrimSpace(s.Text) == "" {
			t.Fatalf("segment %d violates its invariants: %+v", i, s)
		}
		if !utf8.ValidString(s.Text) {
			t.Fatalf("segment %d is cut inside a code point", i)
		}
		if i > 0 && segments[i-1].ByteEnd != s.ByteStart {
			t.Fatalf("segment %d does not start where %d ended", i, i-1)
		}
		joined.WriteString(s.Text)
	}
	if joined.String() != text {
		t.Fatal("the segments are not the document")
	}
	for _, quote := range []string{"prefers written documentation", "تعيش في دبلن", "租约签了吗？"} {
		found := 0
		for _, s := range segments {
			at := strings.Index(s.Text, quote)
			if at < 0 {
				continue
			}
			found++
			if text[s.ByteStart+at:s.ByteStart+at+len(quote)] != quote {
				t.Fatalf("quote %q found in segment %d but its document position does not hold it", quote, s.Ordinal)
			}
		}
		if found == 0 {
			t.Fatalf("quote %q was cut across segments and can be cited nowhere", quote)
		}
	}
}

// The cut prefers a paragraph, then a sentence, then whitespace, then the last whole code point;
// none of them may fall inside a multibyte space or code point.
func TestTheCutPrefersParagraphThenSentenceThenSpaceThenCodePoint(t *testing.T) {
	ceiling := ingest.MinSegmentBytes
	word := strings.Repeat("x", 40)
	// Paragraph: two paragraphs whose sum overflows the ceiling; the cut is after the blank line.
	paragraph := strings.Repeat(word+" ", 3) + "\n\n" + strings.Repeat(word+" ", 4) + "end"
	s, err := ingest.Split(paragraph, ceiling)
	if err != nil || len(s) != 2 || !strings.HasSuffix(s[0].Text, "\n\n") {
		t.Fatalf("expected the cut after the blank line, got %+v %v", s, err)
	}
	// Sentence: no blank line, a sentence ends before the ceiling; the cut is after its space.
	sentence := strings.Repeat(word+" ", 3) + "done. " + strings.Repeat(word+" ", 4) + "end"
	s, err = ingest.Split(sentence, ceiling)
	if err != nil || len(s) != 2 || !strings.HasSuffix(s[0].Text, "done. ") {
		t.Fatalf("expected the cut after the sentence, got %+v %v", s, err)
	}
	// Arabic sentence terminator followed by a multibyte space.
	arabic := strings.Repeat("كلمة ", 20) + "انتهى؟ " + strings.Repeat("كلمة ", 40)
	s, err = ingest.Split(arabic, ceiling)
	if err != nil || len(s) < 2 || !strings.HasSuffix(s[0].Text, "؟ ") {
		t.Fatalf("expected the cut after the Arabic terminator and its space, got %d segments %v", len(s), err)
	}
	// Whitespace only: no terminator anywhere; the cut is after a space, and words stay whole.
	words := strings.Repeat(word+" ", 10)
	s, err = ingest.Split(words, ceiling)
	if err != nil || len(s) < 2 {
		t.Fatalf("expected several segments, got %d %v", len(s), err)
	}
	for _, seg := range s[:len(s)-1] {
		if !strings.HasSuffix(seg.Text, " ") {
			t.Fatalf("a segment cut inside a word: %q", seg.Text)
		}
	}
	// No boundary at all, in a four-byte script: the cut lands on a code point, never inside one.
	glyphs := strings.Repeat("𝔘", 300)
	s, err = ingest.Split(glyphs, ceiling)
	if err != nil {
		t.Fatal(err)
	}
	for _, seg := range s {
		if !utf8.ValidString(seg.Text) || len(seg.Text) > ceiling {
			t.Fatalf("segment cut inside a code point or over the ceiling: %d bytes", len(seg.Text))
		}
	}
}

// A segment that would carry no words is refused with the whole document, and so is a ceiling
// outside the range a message can hold.
func TestUnsegmentableTextAndBadCeilingsAreRefused(t *testing.T) {
	blank := "a" + strings.Repeat(" ", ingest.MinSegmentBytes*2) + "b"
	if _, err := ingest.Split(blank, ingest.MinSegmentBytes); !errors.Is(err, ingest.ErrUnsegmentable) {
		t.Fatalf("expected the whitespace run refused, got %v", err)
	}
	if _, err := ingest.Split("some text", ingest.MinSegmentBytes-1); !errors.Is(err, ingest.ErrSegmentCeiling) {
		t.Fatal("a ceiling below the minimum must be refused")
	}
	if _, err := ingest.Split("some text", ingest.MaxSegmentBytes+1); !errors.Is(err, ingest.ErrSegmentCeiling) {
		t.Fatal("a ceiling above the message limit must be refused")
	}
	if _, err := ingest.Split("   \n  ", ingest.MinSegmentBytes); !errors.Is(err, ingest.ErrEmpty) {
		t.Fatal("blank text must be refused")
	}
}

// Every refusal happens before anything could be sent: the size ceiling from the file's size, not
// its content; text that is not UTF-8 or carries NUL; an empty file; an extension this contract does
// not read; JSON that is malformed, has trailing content, or names a field the shape does not have.
func TestADocumentIsRefusedWholeBeforeAnythingIsSent(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content []byte
		max     int
		want    error
	}{
		{"big.txt", []byte(strings.Repeat("x", 1025)), 1024, ingest.ErrTooLarge},
		{"empty.txt", nil, 0, ingest.ErrEmpty},
		{"blank.md", []byte(" \n\t\n"), 0, ingest.ErrEmpty},
		{"bad.txt", []byte{'a', 0xff, 'b'}, 0, ingest.ErrInvalidText},
		{"nul.txt", []byte("a\x00b"), 0, ingest.ErrInvalidText},
		{"archive.zip", []byte("PK\x03\x04"), 0, ingest.ErrUnsupportedFormat},
		{"noext", []byte("plain"), 0, ingest.ErrUnsupportedFormat},
		{"broken.json", []byte(`{"title": "x", "text": `), 0, ingest.ErrMalformed},
		{"trailing.json", []byte(`{"text": "x"} {"text": "y"}`), 0, ingest.ErrMalformed},
		{"unknown.json", []byte(`{"text": "x", "occurredAt": "2024-01-01T00:00:00Z"}`), 0, ingest.ErrMalformed},
		{"notext.json", []byte(`{"title": "only a title"}`), 0, nil},
	}
	for _, tc := range cases {
		path := write(t, dir, tc.name, tc.content)
		_, err := ingest.Read(path, tc.max)
		if tc.want == nil {
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: expected %v, got %v", tc.name, tc.want, err)
		}
	}
	if _, err := ingest.Read(filepath.Join(dir, "missing.txt"), 0); err == nil {
		t.Fatal("a missing file must be an error")
	}
	if err := os.Mkdir(filepath.Join(dir, "folder.txt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Read(filepath.Join(dir, "folder.txt"), 0); err == nil {
		t.Fatal("a directory with a document's extension must be an error")
	}
}

// A JSON document is its title, a blank line and its text, with the time it says it happened; a
// text file is its bytes trimmed of trailing whitespace. The digest names the text the segments
// are cut from, so the same words in two files are one document and an edit is another.
func TestAReadDocumentCarriesItsTextTimeAndDigest(t *testing.T) {
	dir := t.TempDir()
	j := write(t, dir, "note.json", []byte(`{"title": " The lease ", "text": "It was signed in March.", "occurred_at": "2024-03-04T05:06:07Z"}`))
	doc, err := ingest.Read(j, 0)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Text != "The lease\n\nIt was signed in March." || doc.OccurredAt.Year() != 2024 || doc.Name != "note.json" {
		t.Fatalf("unexpected document %+v", doc)
	}
	same := write(t, dir, "same.txt", []byte("The lease\n\nIt was signed in March.\n\n"))
	twin, err := ingest.Read(same, 0)
	if err != nil {
		t.Fatal(err)
	}
	if twin.Digest != doc.Digest || !twin.OccurredAt.IsZero() {
		t.Fatal("the same words must be the same document, and a text file says no time")
	}
	edited := write(t, dir, "edited.txt", []byte("The lease\n\nIt was signed in April."))
	other, err := ingest.Read(edited, 0)
	if err != nil {
		t.Fatal(err)
	}
	if other.Digest == doc.Digest {
		t.Fatal("an edit must be a different document")
	}
}

// A segment's key is a function of the document, its position, the speaker and the subject: the
// same document sent twice replays, and the same words as another speaker or under another
// subject are another observation.
func TestASegmentKeyIsStableAndSensitiveToSpeakerAndSubject(t *testing.T) {
	var digest [32]byte
	digest[0] = 1
	a := ingest.Key(digest, 0, "tool", "")
	if a != ingest.Key(digest, 0, "tool", "") || a.Version() != 5 {
		t.Fatal("a key must be deterministic and a version-5 UUID")
	}
	for _, other := range []struct {
		ordinal int
		role    string
		subject string
	}{{1, "tool", ""}, {0, "user", ""}, {0, "tool", "subject-1"}} {
		if ingest.Key(digest, other.ordinal, other.role, other.subject) == a {
			t.Fatalf("key must differ for %+v", other)
		}
	}
	var another [32]byte
	another[0] = 2
	if ingest.Key(another, 0, "tool", "") == a {
		t.Fatal("key must differ for another document")
	}
}
