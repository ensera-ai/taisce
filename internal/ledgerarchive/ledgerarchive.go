// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package ledgerarchive is the file a sealed range of the audit ledger leaves in, and the check
// that such a file is what the ledger sealed.
//
// # Why it exists
//
// The ledger is bounded by letting sealed ranges leave it. An entry that leaves must stay as
// verifiable as one that stayed, or the bound is bought with the record. So a range leaves as a
// file that carries its seals, and the file can be checked by somebody who has it, a digest
// written down outside the database, and nothing else.
//
// # What this is allowed to decide, and what it must not
//
// It decides how a range is written down and whether a file verifies. It holds no connection: the
// store decides which range leaves and when, and reads the entries; this package is given them. The
// check here must not need the database, because the database is the thing whose account is being
// checked.
//
// # The file
//
// Lines of JSON. A header, then each seal followed by the entries it covers. An entry is one
// string: the line the seal's digest was computed over, exactly as the database produced it. That
// is deliberate. A file of separate fields would have to be put back together the way PostgreSQL
// writes a number and a time before it could be hashed, and a verifier that needs to reproduce a
// database's formatting is not independent of it.
//
// # What a file that verifies proves
//
// That its entries are the ones the seals were computed over, that the seals follow one another,
// and — when the reader supplies a digest — that the chain ends where an outside record says it
// did. It does not prove the file is complete beyond its own header, and it proves nothing if the
// only digest it is compared with came from the same database at the same time.
package ledgerarchive

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"strconv"
	"strings"
	"time"
)

// Version is the format's. A reader refuses one it does not know.
const Version = 1

// Header opens a file: where the chain stood before the range, and where the range ends.
type Header struct {
	Version int `json:"taisce_ledger_archive"`
	// PreviousDigest is the chain's digest before the first seal in the file: thirty-two zero
	// bytes for the first range ever archived, and the previous file's head afterwards.
	PreviousDigest string `json:"previous_digest"`
	ThroughSeal    int64  `json:"through_seal"`
	ThroughEntry   int64  `json:"through_entry"`
}

// Seal is one seal as the ledger held it.
type Seal struct {
	ID             int64     `json:"seal"`
	FromEntry      int64     `json:"from_entry"`
	ToEntry        int64     `json:"to_entry"`
	EntryCount     int       `json:"entry_count"`
	EntriesDigest  string    `json:"entries_digest"`
	PreviousDigest string    `json:"previous_digest"`
	Digest         string    `json:"digest"`
	SealedAt       time.Time `json:"sealed_at"`
}

type entry struct {
	Line *string `json:"entry"`
}

// Writer writes a file line by line, so a range of any size is never held in memory.
type Writer struct{ out *json.Encoder }

// NewWriter writes to out. The caller owns making it durable.
func NewWriter(out io.Writer) *Writer { return &Writer{out: json.NewEncoder(out)} }

// Header writes the opening line.
func (w *Writer) Header(previous []byte, throughSeal, throughEntry int64) error {
	return w.out.Encode(Header{Version: Version, PreviousDigest: hex.EncodeToString(previous), ThroughSeal: throughSeal, ThroughEntry: throughEntry})
}

// Seal writes a seal. Its entries follow it.
func (w *Writer) Seal(seal Seal) error { return w.out.Encode(seal) }

// Entry writes one entry: the line its seal digested.
func (w *Writer) Entry(line string) error { return w.out.Encode(entry{Line: &line}) }

// EntriesDigest is the digest a seal holds for its entries: SHA-256 over their lines joined by a
// newline, with none after the last. It is written incrementally so both the store and the
// verifier compute it without holding a range.
type EntriesDigest struct {
	sum hash.Hash
	n   int
}

// NewEntriesDigest starts the digest of one seal's entries.
func NewEntriesDigest() *EntriesDigest { return &EntriesDigest{sum: sha256.New()} }

// Add takes the next entry's line, in order.
func (d *EntriesDigest) Add(line string) {
	if d.n > 0 {
		d.sum.Write([]byte{'\n'})
	}
	d.sum.Write([]byte(line))
	d.n++
}

// Count is how many lines were added.
func (d *EntriesDigest) Count() int { return d.n }

// Sum is the digest of what was added.
func (d *EntriesDigest) Sum() []byte { return d.sum.Sum(nil) }

// Chain is a seal's own digest: the digest before it, then its entries' digest.
func Chain(previous, entries []byte) []byte {
	sum := sha256.Sum256(append(append([]byte{}, previous...), entries...))
	return sum[:]
}

// Verification is what checking a file found.
type Verification struct {
	Valid bool
	// Failure names the seal that disagreed and how, when Valid is false.
	Failure string
	Seals   int
	Entries int64
	// FirstEntry and LastEntry are the identifiers the file runs between.
	FirstEntry, LastEntry int64
	// PreviousDigest is where the file says the chain stood before it, and Head where it ends.
	PreviousDigest, Head []byte
}

// Verify checks a file against itself and, when head is given, against a digest from elsewhere.
//
// Without head it shows the file is internally a chain. That is worth little on its own: anybody
// who can write a file can write a consistent one. The check that matters is the last one — that
// the chain ends at a digest recorded where the database could not reach it, which is what the
// service writes to its log at every seal.
//
// A file that is malformed is an error. A file that is well formed and does not verify is a
// Verification with Valid false and the reason.
func Verify(file io.Reader, head []byte) (Verification, error) {
	lines := bufio.NewScanner(file)
	lines.Buffer(make([]byte, 0, 64<<10), 4<<20)
	if !lines.Scan() {
		return Verification{}, fmt.Errorf("the file is empty")
	}
	var header Header
	if err := json.Unmarshal(lines.Bytes(), &header); err != nil || header.Version == 0 {
		return Verification{}, fmt.Errorf("the file does not begin with a ledger archive header")
	}
	if header.Version != Version {
		return Verification{}, fmt.Errorf("the file is ledger archive version %d and this reads version %d", header.Version, Version)
	}
	previous, err := digest(header.PreviousDigest)
	if err != nil {
		return Verification{}, fmt.Errorf("the header's previous digest: %w", err)
	}
	out := Verification{PreviousDigest: previous, Head: previous, FirstEntry: -1}
	fail := func(format string, args ...any) (Verification, error) {
		out.Failure = fmt.Sprintf(format, args...)
		return out, nil
	}

	var (
		open     *Seal
		entries  *EntriesDigest
		lastSeal int64
	)
	// closeSeal checks the seal whose entries have all been read.
	closeSeal := func() (string, error) {
		if open == nil {
			return "", nil
		}
		wantEntries, err := digest(open.EntriesDigest)
		if err != nil {
			return "", fmt.Errorf("seal %d: %w", open.ID, err)
		}
		wantPrevious, err := digest(open.PreviousDigest)
		if err != nil {
			return "", fmt.Errorf("seal %d: %w", open.ID, err)
		}
		wantDigest, err := digest(open.Digest)
		if err != nil {
			return "", fmt.Errorf("seal %d: %w", open.ID, err)
		}
		switch {
		case entries.Count() != open.EntryCount:
			return fmt.Sprintf("seal %d covers %d entries and the file holds %d for it: an entry was removed or added", open.ID, open.EntryCount, entries.Count()), nil
		case !bytes.Equal(entries.Sum(), wantEntries):
			return fmt.Sprintf("seal %d covers entries %d-%d and they do not digest to what it recorded: an entry in that range was altered", open.ID, open.FromEntry, open.ToEntry), nil
		case !bytes.Equal(wantPrevious, out.Head):
			return fmt.Sprintf("seal %d does not follow what came before it: a seal was removed or the chain was re-linked", open.ID), nil
		case !bytes.Equal(Chain(wantPrevious, wantEntries), wantDigest):
			return fmt.Sprintf("seal %d records a digest that is not the one its own contents produce", open.ID), nil
		}
		out.Head = wantDigest
		out.Seals++
		out.Entries += int64(open.EntryCount)
		lastSeal = open.ID
		return "", nil
	}

	for number := 2; lines.Scan(); number++ {
		raw := lines.Bytes()
		var e entry
		if err := json.Unmarshal(raw, &e); err == nil && e.Line != nil {
			if open == nil {
				return Verification{}, fmt.Errorf("line %d is an entry before any seal", number)
			}
			id, _, _ := strings.Cut(*e.Line, "|")
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil {
				return Verification{}, fmt.Errorf("line %d is an entry with no identifier", number)
			}
			if n < open.FromEntry || n > open.ToEntry || n <= out.LastEntry && out.FirstEntry >= 0 {
				return fail("entry %d is outside the range of seal %d, or out of order", n, open.ID)
			}
			if out.FirstEntry < 0 {
				out.FirstEntry = n
			}
			out.LastEntry = n
			entries.Add(*e.Line)
			continue
		}
		var seal Seal
		if err := json.Unmarshal(raw, &seal); err != nil || seal.ID == 0 {
			return Verification{}, fmt.Errorf("line %d is neither a seal nor an entry", number)
		}
		failure, err := closeSeal()
		if err != nil {
			return Verification{}, err
		}
		if failure != "" {
			return fail("%s", failure)
		}
		if seal.ID <= lastSeal {
			return fail("seal %d does not come after seal %d", seal.ID, lastSeal)
		}
		open, entries = &seal, NewEntriesDigest()
	}
	if err := lines.Err(); err != nil {
		return Verification{}, fmt.Errorf("read the file: %w", err)
	}
	failure, err := closeSeal()
	if err != nil {
		return Verification{}, err
	}
	if failure != "" {
		return fail("%s", failure)
	}
	switch {
	case out.Seals == 0:
		return fail("the file holds no seal")
	case lastSeal != header.ThroughSeal || out.LastEntry != header.ThroughEntry:
		return fail("the file says it runs through seal %d and entry %d, and ends at seal %d and entry %d: its end is missing",
			header.ThroughSeal, header.ThroughEntry, lastSeal, out.LastEntry)
	case head != nil && !bytes.Equal(head, out.Head):
		return fail("the file ends at digest %x and the digest it was checked against is %x", out.Head, head)
	}
	out.Valid = true
	return out, nil
}

func digest(text string) ([]byte, error) {
	raw, err := hex.DecodeString(text)
	if err != nil || len(raw) != sha256.Size {
		return nil, fmt.Errorf("%q is not a SHA-256 digest", text)
	}
	return raw, nil
}
