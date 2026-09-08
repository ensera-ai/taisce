// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/domain"
)

func TestObservationResourceLimitsCountBytesAndPreserveTheBoundary(t *testing.T) {
	turn := domain.Turn{Scope: "p1"}
	for i := 0; i < domain.MaxObservationMessages; i++ {
		turn.Messages = append(turn.Messages, domain.Message{Ordinal: i, Role: domain.RoleUser, Content: strings.Repeat("é", domain.MaxObservationBytes/domain.MaxObservationMessages/2)})
	}
	if err := turn.Validate(); err != nil {
		t.Fatalf("exact count and aggregate boundary: %v", err)
	}
	turn.Messages[0].Content += "x"
	if err := turn.Validate(); !errors.Is(err, domain.ErrObservationSize) {
		t.Fatalf("aggregate overflow: %v", err)
	}
	turn.Messages = append(turn.Messages, domain.Message{})
	if err := turn.Validate(); !errors.Is(err, domain.ErrMessageCount) {
		t.Fatalf("count overflow: %v", err)
	}
	turn.Messages = []domain.Message{{Role: domain.RoleUser, Content: strings.Repeat("é", domain.MaxMessageBytes/2)}}
	if err := turn.Validate(); err != nil {
		t.Fatalf("exact message boundary: %v", err)
	}
	turn.Messages[0].Content += "é"
	if err := turn.Validate(); !errors.Is(err, domain.ErrMessageSize) {
		t.Fatalf("UTF-8 byte overflow: %v", err)
	}
}
