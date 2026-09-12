// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package domain

import "errors"

// These bound a single append and its downstream extraction work. Durable backlog admission is
// separate: a bounded turn alone cannot prevent an unlimited number of turns during an outage.
const (
	MaxObservationMessages = 64
	MaxMessageBytes        = 64 << 10
	MaxObservationBytes    = 256 << 10
)

var (
	ErrMessageCount    = errors.New("observation exceeds 64 messages")
	ErrMessageSize     = errors.New("message exceeds 65536 UTF-8 bytes")
	ErrObservationSize = errors.New("observation content exceeds 262144 UTF-8 bytes")
)

// ValidateMessageCount runs before allocating per-message validation or transport copies.
func ValidateMessageCount(count int) error {
	if count > MaxObservationMessages {
		return ErrMessageCount
	}
	return nil
}
