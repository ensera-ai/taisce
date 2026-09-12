// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package ingest turns a document on disk into the observations the write contract already
// accepts, and nothing else. It decides three things: which files are documents and how one is
// refused, where a long text is cut so that every piece fits one message, and how a piece names
// itself so that sending it twice stores it once. It does not talk to the network, the database or
// a model; the command in cmd/taisce does the sending, and the API applies every rule about roles,
// subjects and sizes exactly as it does for a conversation. A document is not a new kind of
// memory. It is a sequence of turns whose speaker is a third party.
//
// # Why segments are separate observations rather than one long message
//
// Formation extracts one message per model call, so a message is the unit of extraction cost and
// of failure: a message that exceeds the turn budget is parked whole, and its neighbours with it
// if they share a turn. A document cut into segments that are each their own observation is formed
// piece by piece, retried piece by piece, and cited piece by piece. Evidence is a byte span against
// the chunk it indexes, so a segment is also the unit of citation: the span is exact within
// the segment, and the segment's own offset within the document, recorded in the manifest, maps it
// back to the file. Splitting inside a code point or without recording the offset would make every
// citation into a document point at the wrong bytes, which is the corruption #91 names.
//
// # Why the cut prefers a paragraph, then a sentence, then a space
//
// The extractor locates a quote inside the message it was given; a claim whose sentence is cut in
// half is a quote it cannot locate, and the claim is refused as unlocatable. Cutting on a paragraph
// keeps claims with their context; on a sentence keeps each quote whole; on whitespace keeps words
// whole; and the last resort, a code-point boundary, keeps the text valid UTF-8 and nothing more.
// The ceiling is measured, not guessed: on a news corpus the articles that overran a two-minute
// model budget under a shared server averaged 10.7 KB, and the rest, averaging 4.5 KB, did not
// (docs/31), so the default ceiling sits at 8 KiB and an operator who has measured their own model
// moves it.
//
// # What is refused, and why before anything is sent
//
// A file over the size ceiling, one that is not valid UTF-8, one carrying a NUL byte, one with no
// text once trimmed, an unsupported extension, malformed JSON or JSON with a field this contract
// does not define: each is refused with no segment sent, because a document half stored is a
// document whose citations point at bytes the operator cannot reproduce. Nothing here decompresses
// anything, so an archive is an unsupported format rather than a bomb.
package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ensera-ai/taisce/internal/domain"
	"github.com/google/uuid"
)

const (
	// DefaultSegmentBytes is the measured ceiling explained above.
	DefaultSegmentBytes = 8 << 10
	// MinSegmentBytes keeps a segment large enough to hold a sentence of any language with its
	// context; below it the cut falls inside sentences constantly and every quote is unlocatable.
	MinSegmentBytes = 256
	// MaxSegmentBytes is the write contract's own message ceiling.
	MaxSegmentBytes = domain.MaxMessageBytes
	// DefaultMaxFileBytes bounds one file before it is read. A document larger than this is not a
	// document an agent's memory should hold as turns; it is a corpus, and it is split by its owner.
	DefaultMaxFileBytes = 16 << 20
)

var (
	ErrUnsupportedFormat = errors.New("unsupported document format: .txt, .md and .json are read")
	ErrTooLarge          = errors.New("document exceeds the file size ceiling")
	ErrEmpty             = errors.New("document carries no text")
	ErrInvalidText       = errors.New("document is not valid UTF-8 text without NUL bytes")
	ErrMalformed         = errors.New("document is not the JSON shape this contract defines")
	ErrSegmentCeiling    = errors.New("segment ceiling must be between 256 bytes and the message ceiling")
	ErrUnsegmentable     = errors.New("document has a run of whitespace longer than a segment")
)

// Document is what was read: the text every segment is cut from, and the identity it will carry.
type Document struct {
	// Name is how the operator named the file; it goes into the manifest, never onto the wire.
	Name string
	// Text is the document as the segments see it: for a text file, the file trimmed of trailing
	// whitespace; for a JSON document, the title, a blank line and the text. Offsets are into this.
	Text string
	// Digest is SHA-256 of Text, so two files with the same words are one document and an edited
	// file is a different one.
	Digest [32]byte
	// OccurredAt is when the document says it happened, or zero when it does not say, in which case
	// the API stamps ingestion time as it does for a conversation that gives none.
	OccurredAt time.Time
}

// jsonDocument is the one JSON shape this contract defines. Unknown fields are refused rather than
// ignored, because a caller who wrote `occurredAt` and was silently ignored has lost a date without
// being told.
type jsonDocument struct {
	Title      string     `json:"title"`
	Text       string     `json:"text"`
	OccurredAt *time.Time `json:"occurred_at"`
}

// Read loads one document, refusing before anything is sent. maxBytes bounds the file and is
// checked before the file is read, so a file that would not fit is never in memory.
func Read(path string, maxBytes int) (Document, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFileBytes
	}
	info, err := os.Stat(path)
	if err != nil {
		return Document{}, err
	}
	if info.Size() > int64(maxBytes) {
		return Document{}, fmt.Errorf("%s: %w (%d bytes over %d)", path, ErrTooLarge, info.Size(), maxBytes)
	}
	if info.Size() == 0 {
		return Document{}, fmt.Errorf("%s: %w", path, ErrEmpty)
	}
	f, err := os.Open(path)
	if err != nil {
		return Document{}, err
	}
	defer f.Close()
	// One byte more than the ceiling, so a file that grew between the stat and the read is caught
	// rather than truncated into a document whose digest names bytes that were never all read.
	raw, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil {
		return Document{}, err
	}
	if len(raw) > maxBytes {
		return Document{}, fmt.Errorf("%s: %w", path, ErrTooLarge)
	}
	doc := Document{Name: filepath.Base(path)}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md":
		doc.Text = string(raw)
	case ".json":
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var j jsonDocument
		if err := decoder.Decode(&j); err != nil {
			return Document{}, fmt.Errorf("%s: %w: %v", path, ErrMalformed, err)
		}
		if _, err := decoder.Token(); err != io.EOF {
			return Document{}, fmt.Errorf("%s: %w: trailing content after the document", path, ErrMalformed)
		}
		doc.Text = j.Text
		if title := strings.TrimSpace(j.Title); title != "" {
			doc.Text = title + "\n\n" + j.Text
		}
		if j.OccurredAt != nil {
			doc.OccurredAt = *j.OccurredAt
		}
	default:
		return Document{}, fmt.Errorf("%s: %w", path, ErrUnsupportedFormat)
	}
	if !utf8.ValidString(doc.Text) || strings.ContainsRune(doc.Text, 0) {
		return Document{}, fmt.Errorf("%s: %w", path, ErrInvalidText)
	}
	doc.Text = strings.TrimRightFunc(doc.Text, unicode.IsSpace)
	if strings.TrimSpace(doc.Text) == "" {
		return Document{}, fmt.Errorf("%s: %w", path, ErrEmpty)
	}
	doc.Digest = sha256.Sum256([]byte(doc.Text))
	return doc, nil
}

// Segment is one piece of a document: the bytes [ByteStart, ByteEnd) of Document.Text, whole.
type Segment struct {
	Ordinal   int
	ByteStart int
	ByteEnd   int
	Text      string
}

// Split cuts text into segments of at most ceiling bytes whose concatenation is the text. Every
// cut lies on a code-point boundary and prefers, in order, the end of a paragraph, the end of a
// sentence, whitespace, and finally the last code point that fits.
func Split(text string, ceiling int) ([]Segment, error) {
	if ceiling < MinSegmentBytes || ceiling > MaxSegmentBytes {
		return nil, ErrSegmentCeiling
	}
	if strings.TrimSpace(text) == "" {
		return nil, ErrEmpty
	}
	var out []Segment
	for pos := 0; pos < len(text); {
		end := len(text)
		if end-pos > ceiling {
			end = pos + cut(text[pos:pos+ceiling])
		}
		piece := text[pos:end]
		if strings.TrimSpace(piece) == "" {
			// A segment with no words would be refused by the API as a message with no content,
			// and the segments after it would then be a document with a hole. Refuse the whole.
			return nil, ErrUnsegmentable
		}
		out = append(out, Segment{Ordinal: len(out), ByteStart: pos, ByteEnd: end, Text: piece})
		pos = end
	}
	return out, nil
}

// cut returns where to end the first segment of a window that is exactly one ceiling long and is
// known not to be the end of the text. The choice is the latest boundary of the best kind that
// leaves at least one word in the segment.
func cut(window string) int {
	firstWord := strings.IndexFunc(window, func(r rune) bool { return !unicode.IsSpace(r) })
	if firstWord < 0 {
		return len(window) // whitespace only; Split refuses it
	}
	// A paragraph ends at a blank line; the blank line stays with the paragraph it closes.
	if i := strings.LastIndex(window, "\n\n"); i > firstWord {
		return i + 2
	}
	// A sentence ends at terminal punctuation followed by whitespace, in the scripts this has met:
	// Latin, Arabic (؟ ۔) and CJK (。！？) terminators.
	best := -1
	for i, r := range window {
		if i <= firstWord {
			continue
		}
		if !isTerminator(r) {
			continue
		}
		next := i + utf8.RuneLen(r)
		if next < len(window) {
			// The space after the terminator stays with the sentence it closes, so the next
			// segment begins on a word.
			if after, width := utf8.DecodeRuneInString(window[next:]); unicode.IsSpace(after) {
				best = next + width
			}
		}
	}
	if best > 0 {
		return best
	}
	if i := strings.LastIndexFunc(window, unicode.IsSpace); i > firstWord {
		_, width := utf8.DecodeRuneInString(window[i:])
		return i + width
	}
	// No boundary at all: the longest prefix that is whole UTF-8, which is the window itself unless
	// the ceiling fell inside a code point.
	for i := len(window); i > 0; i-- {
		if utf8.ValidString(window[:i]) {
			return i
		}
	}
	return len(window)
}

func isTerminator(r rune) bool {
	switch r {
	case '.', '!', '?', '؟', '۔', '。', '！', '？':
		return true
	}
	return false
}

// keyNamespace is the fixed UUID namespace under which a segment names itself. It is a constant so
// that the same document sent from two machines produces the same keys, which is what makes the
// second send a replay rather than a duplicate.
var keyNamespace = uuid.MustParse("6f1d8a1e-3b7c-4d2a-9f0e-5c4b3a2d1e0f")

// Key derives the idempotency key of one segment. It covers the document's digest, the segment's
// ordinal, the role and the data subject, so the same words observed as a different speaker or
// under a different subject are a different observation, as the API would store them.
func Key(digest [32]byte, ordinal int, role, dataSubjectID string) uuid.UUID {
	data := fmt.Sprintf("%x\n%d\n%s\n%s", digest, ordinal, role, dataSubjectID)
	return uuid.NewHash(sha256.New(), keyNamespace, []byte(data), 5)
}
