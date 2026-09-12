// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package compaction

import "time"

// Material is what one segment is written from: either the turns it covers, as they were said, or
// the segments of the level below it. Exactly one of the two is set.
type Material struct {
	Level int
	From  int64
	To    int64
	Turns []TurnText
	Units []UnitText
}

// TurnText is one turn as the summariser sees it: when it happened and who said what, in order.
type TurnText struct {
	Offset     int64
	OccurredAt time.Time
	Messages   []MessageText
}

// MessageText is one message with the role of whoever said it.
type MessageText struct {
	Role    string
	Content string
}

// UnitText is one lower-level segment's summary, with the range it covers.
type UnitText struct {
	From    int64
	To      int64
	Covered int
	Summary string
}

// Summary is what the summariser returns: the prose, and nothing else.
type Summary struct {
	Text string
}

// Size is what the material costs a context, in characters: the words a model reads.
func (m Material) Size() int {
	n := 0
	for _, t := range m.Turns {
		for _, msg := range t.Messages {
			n += len(msg.Role) + len(msg.Content)
		}
	}
	for _, u := range m.Units {
		n += len(u.Summary)
	}
	return n
}

// MaxMaterialCharacters bounds what one summary is written from. A level-1 segment covers Branch
// turns of at most the message ceiling each, which is more than a model attends to; material over
// the bound is cut from the oldest turn, and the cut is reported by the pass rather than hidden.
const MaxMaterialCharacters = 48000
