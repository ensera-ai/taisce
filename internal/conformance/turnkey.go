// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package conformance

import (
	"crypto/sha256"
	"strconv"

	"github.com/google/uuid"
)

// TurnKeyDerivation names the derivation TurnKey implements. It is the first field hashed, so a key
// from this derivation can never equal one from another, and conformance/turn-keys.json carries it
// beside the vectors so a file and a runner that disagree are seen to.
const TurnKeyDerivation = "taisce-turn-key/2"

// KeyMessage is one message as the key reads it: who said it and what they said. The group ordinal
// is not in the key; a turn is the same turn however an adapter groups it.
type KeyMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// TurnKey is the idempotency key every adapter derives for a turn: a version-5-shaped UUID over
// SHA-256 of the derivation name, the subject, the run and each message's role and content, every
// field written as a netstring — its length in UTF-8 bytes in decimal, a colon, the bytes, a comma.
//
// The length prefix is the point. With fields joined by a separator, content that contains the
// separator and a role name moves a boundary, and two different turns hash the same bytes; the
// deployment then refuses the second as a conflicting retry, on every retry, for good. A length
// cannot be forged by content, so the bytes hashed decode to exactly one sequence of fields.
//
// The length is of the UTF-8 encoding, never of a language's string: a UTF-16 count differs for any
// character outside the Basic Multilingual Plane, and the vectors hold one so that an adapter which
// counts the wrong unit fails its own test rather than disagreeing silently with the others.
func TurnKey(subject, run string, messages []KeyMessage) string {
	h := sha256.New()
	netstring := func(field string) {
		h.Write([]byte(strconv.Itoa(len(field))))
		h.Write([]byte{':'})
		h.Write([]byte(field))
		h.Write([]byte{','})
	}
	netstring(TurnKeyDerivation)
	netstring(subject)
	netstring(run)
	for _, m := range messages {
		netstring(m.Role)
		netstring(m.Content)
	}
	return uuidFromDigest(h.Sum(nil))
}

// uuidFromDigest shapes the first sixteen bytes of a digest as a version-5, RFC 4122 variant UUID.
// The shape is what the observe contract accepts as a key; the version nibble says it was derived,
// not drawn at random.
func uuidFromDigest(sum []byte) string {
	var id uuid.UUID
	copy(id[:], sum[:16])
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id.String()
}
