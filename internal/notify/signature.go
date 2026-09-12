// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// SignatureHeader carries the version, the time and the signature, in one header.
//
// One header rather than three, because three can disagree: a receiver that reads the timestamp
// from one header and verifies a signature computed over another has verified nothing. Here the
// material is unambiguous — the version, the timestamp and the body — and the header is the whole
// claim.
const SignatureHeader = "Taisce-Signature"

// SignatureVersion is the first field of the header and of the signed material. It exists so that a
// change to what is signed cannot be read by an old receiver as the same claim.
const SignatureVersion = "v1"

// Sign returns the header value for a body at a moment.
//
// # Why the timestamp is inside the signed material
//
// A signature over the body alone is valid forever. Anybody who captures one delivery can replay it
// a week later and the receiver cannot tell. Signing the timestamp with the body means a replay is
// either stale — which the receiver rejects by looking at the clock — or a forgery, which requires
// the secret.
func Sign(secret []byte, at time.Time, body []byte) string {
	stamp := strconv.FormatInt(at.UTC().Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	// The fields are joined by a byte that cannot appear in any of them, so no arrangement of one
	// field's contents can imitate the boundary between two.
	mac.Write([]byte(SignatureVersion))
	mac.Write([]byte{'\n'})
	mac.Write([]byte(stamp))
	mac.Write([]byte{'\n'})
	mac.Write(body)
	return fmt.Sprintf("%s t=%s s=%s", SignatureVersion, stamp, hex.EncodeToString(mac.Sum(nil)))
}

// Verify is what a receiver does, written here so the suite can hold the signature to it and so an
// adapter has one correct implementation to copy rather than three guesses.
//
// Tolerance bounds how old a delivery may be and still be believed. It is the receiver's choice and
// there is no right answer; what matters is that there is one, because without it the timestamp is
// decoration.
func Verify(secret []byte, header string, body []byte, now time.Time, tolerance time.Duration) error {
	var version, stamp, signature string
	if _, err := fmt.Sscanf(header, "%s t=%s s=%s", &version, &stamp, &signature); err != nil {
		return fmt.Errorf("the signature header is not readable")
	}
	if version != SignatureVersion {
		return fmt.Errorf("the signature is version %q and this reader knows %q", version, SignatureVersion)
	}
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return fmt.Errorf("the signature has no readable time")
	}
	age := now.UTC().Sub(time.Unix(seconds, 0).UTC())
	if age > tolerance || age < -tolerance {
		return fmt.Errorf("the signature is %s old, outside the tolerance of %s", age.Truncate(time.Second), tolerance)
	}
	expected := Sign(secret, time.Unix(seconds, 0), body)
	// Constant time, because a comparison that returns early tells an attacker how much of their
	// guess was right, and a signature is guessed one byte at a time or not at all.
	if !hmac.Equal([]byte(expected), []byte(header)) {
		return fmt.Errorf("the signature does not match the body")
	}
	return nil
}
