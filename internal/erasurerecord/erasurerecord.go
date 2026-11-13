// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package erasurerecord is the trace an erasure leaves where a restore cannot reach it.
//
// # Why it exists
//
// The receipt for an erasure is a row in the database. A restore to a point before the erasure
// brings back what was erased and takes the receipt away, so afterwards the system holds a person
// it was told to forget and no record that it was told. Something has to survive the restore, and
// it cannot be the database.
//
// # What this is allowed to decide, and what it must not
//
// It decides what the trace says and how a trace is recognised again: the digest, the line it is
// written as, and how lines are read back and put together. It does not decide what an erasure
// removes, when a trace is written, or what is done on finding one — it holds no connection and
// reads no row. The store writes a trace before an erasure commits; the replay command acts on
// one.
//
// # Why a keyed digest, and not the identifier
//
// The trace is written to the process log, which is the one place every deployment already keeps
// outside the database. A log that listed the identifiers of the people who asked to be forgotten
// would be the list nobody should hold, in the least protected place in the deployment. A plain
// hash would be little better: an identifier is often an address or a number somebody can guess,
// and a guess can be hashed. So the digest is keyed, and the key lives beside the deployment's
// other secrets and not in the database or the log.
//
// What somebody holding a trace can learn, then, depends on what else they hold:
//
//   - the log alone: that an erasure happened in a project, when, and of how many turns. Not whose.
//   - the log and the key: whether a particular identifier they already know was erased. They
//     cannot list who was.
//   - the key alone: nothing.
//
// # What it costs
//
// A key that is lost makes every trace written under it unreadable, and nothing can replay them.
// A trace the log pipeline dropped is an erasure nothing will repeat. Neither is detected here.
package erasurerecord

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	// KindSubject is an erasure of everything one person's turns produced.
	KindSubject = "subject"
	// KindSource is an erasure of a set of turns named by their own identifiers.
	KindSource = "source"

	// marker begins a trace wherever it is found in a line. Read back by searching for it, so a
	// trace survives whatever a log pipeline wraps around it: a timestamp, a JSON envelope, a
	// stream label.
	marker = "taisce-erasure-record:v1:"

	// perLine is how many digests one line carries. A source erasure may name ten thousand turns,
	// and container runtimes split a log line at sixteen kilobytes; fifty digests is under five.
	perLine = 50

	// MinKeyLength is the shortest key accepted. Shorter than a digest's own strength and the key
	// becomes the thing to guess.
	MinKeyLength = 32
)

// Key is the secret the digests are made with.
type Key struct{ secret []byte }

// ParseKey accepts the key as the deployment holds it. It is used as given, not decoded: a key is
// generated once by whatever generates the deployment's other secrets, and requiring an encoding
// would be one more way to end up with none.
func ParseKey(value string) (Key, error) {
	if len(value) < MinKeyLength {
		return Key{}, fmt.Errorf("the erasure record key is %d characters and must be at least %d", len(value), MinKeyLength)
	}
	return Key{secret: []byte(value)}, nil
}

// ID names the key without revealing it, so a replay given traces made under another key says so
// instead of matching nothing and reporting that nothing was held.
func (k Key) ID() string {
	return k.mac("key")[:16]
}

// Digest is what a trace holds in place of an identifier. The project and the kind are part of
// what is digested, so the same identifier in two projects, or a turn and a person who share one,
// do not produce the same value.
func (k Key) Digest(project, kind, identifier string) string {
	return k.mac("identifier", project, kind, identifier)
}

// mac is the keyed digest of its parts, each preceded by its length so that no two different
// lists of parts are the same bytes.
func (k Key) mac(parts ...string) string {
	m := hmac.New(sha256.New, k.secret)
	for _, part := range parts {
		fmt.Fprintf(m, "%d:%s", len(part), part)
	}
	return hex.EncodeToString(m.Sum(nil))
}

// Record is one line of a trace. An erasure of many turns is several, numbered, so that a replay
// can tell a whole trace from one the log lost part of.
type Record struct {
	RequestID string   `json:"request_id"`
	Project   string   `json:"project"`
	Kind      string   `json:"kind"`
	KeyID     string   `json:"key_id"`
	Part      int      `json:"part"`
	Parts     int      `json:"parts"`
	Digests   []string `json:"digests"`
}

// Records is the trace of one erasure: its identifiers digested, in lines of bounded size.
func (k Key) Records(requestID, project, kind string, identifiers []string) []Record {
	parts := (len(identifiers) + perLine - 1) / perLine
	out := make([]Record, 0, parts)
	for part := 0; part < parts; part++ {
		chunk := identifiers[part*perLine : min((part+1)*perLine, len(identifiers))]
		record := Record{RequestID: requestID, Project: project, Kind: kind, KeyID: k.ID(), Part: part + 1, Parts: parts}
		for _, identifier := range chunk {
			record.Digests = append(record.Digests, k.Digest(project, kind, identifier))
		}
		out = append(out, record)
	}
	return out
}

// Line is the record as it is written to the log.
func (r Record) Line() string {
	raw, err := json.Marshal(r)
	if err != nil {
		// A struct of strings, integers and a slice of strings cannot fail to marshal.
		panic(err)
	}
	return marker + base64.RawURLEncoding.EncodeToString(raw)
}

var traced = regexp.MustCompile(regexp.QuoteMeta(marker) + `([A-Za-z0-9_-]+)`)

// Read finds every record in a stream of log lines, whatever surrounds them.
//
// A marker followed by something that is not a record is an error and not a line to skip: a trace
// that cannot be read is an erasure that would silently not be repeated.
func Read(lines io.Reader) ([]Record, error) {
	var out []Record
	scanner := bufio.NewScanner(lines)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for number := 1; scanner.Scan(); number++ {
		for _, found := range traced.FindAllStringSubmatch(scanner.Text(), -1) {
			raw, err := base64.RawURLEncoding.DecodeString(found[1])
			if err != nil {
				return nil, fmt.Errorf("line %d holds an erasure record that cannot be decoded: %w", number, err)
			}
			var record Record
			if err := json.Unmarshal(raw, &record); err != nil {
				return nil, fmt.Errorf("line %d holds an erasure record that cannot be read: %w", number, err)
			}
			if record.RequestID == "" || record.Project == "" || record.KeyID == "" || record.Part < 1 || record.Part > record.Parts ||
				(record.Kind != KindSubject && record.Kind != KindSource) || len(record.Digests) == 0 {
				return nil, fmt.Errorf("line %d holds an erasure record with a part missing", number)
			}
			out = append(out, record)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read erasure records: %w", err)
	}
	return out, nil
}

// Erasure is the trace of one erasure, put back together from its lines.
type Erasure struct {
	RequestID string
	Project   string
	Kind      string
	KeyID     string
	Digests   map[string]bool
	// Complete is false when a numbered line of the trace was not among those read.
	Complete bool
}

// Gather puts lines back together by the erasure they belong to, in the order each erasure was
// first seen. A line read twice counts once: a log is commonly exported with overlap, and a replay
// writes the trace again.
func Gather(records []Record) ([]Erasure, error) {
	type gathering struct {
		erasure Erasure
		parts   map[int]bool
		of      int
	}
	var order []string
	byRequest := map[string]*gathering{}
	for _, record := range records {
		g, seen := byRequest[record.RequestID]
		if !seen {
			g = &gathering{erasure: Erasure{RequestID: record.RequestID, Project: record.Project, Kind: record.Kind,
				KeyID: record.KeyID, Digests: map[string]bool{}}, parts: map[int]bool{}, of: record.Parts}
			byRequest[record.RequestID] = g
			order = append(order, record.RequestID)
		}
		if g.erasure.Project != record.Project || g.erasure.Kind != record.Kind || g.erasure.KeyID != record.KeyID || g.of != record.Parts {
			return nil, fmt.Errorf("the records of erasure %s disagree with each other", strings.ToValidUTF8(record.RequestID, "?"))
		}
		g.parts[record.Part] = true
		for _, digest := range record.Digests {
			g.erasure.Digests[digest] = true
		}
	}
	out := make([]Erasure, 0, len(order))
	for _, id := range order {
		g := byRequest[id]
		g.erasure.Complete = len(g.parts) == g.of
		out = append(out, g.erasure)
	}
	return out, nil
}
