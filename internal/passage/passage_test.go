// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package passage_test

import (
	"errors"
	"github.com/ensera-ai/taisce/internal/passage"
	"strings"
	"testing"
)

// Input validation runs before database or provider access. Transport JSON validation cannot
// protect an in-process caller, so invalid Unicode and NUL are exercised at this boundary too.
func TestPassageQueryBoundsBeforeIO(t *testing.T) {
	valid := passage.Query{Question: "Where is PostgreSQL used?", Limit: 10}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := []passage.Query{
		{Question: "", Limit: 1}, {Question: " \t", Limit: 1}, {Question: string([]byte{0xff}), Limit: 1},
		{Question: "a\x00b", Limit: 1}, {Question: strings.Repeat("x", 8193), Limit: 1},
		{Question: "q", Limit: 0}, {Question: "q", Limit: 33}, {Question: "q", Limit: 1, Candidates: -1},
		{Question: "q", Limit: 1, Candidates: 1001}, {Question: "q", Limit: 10, Candidates: 9},
		{Question: "q", Limit: 1, Subject: strings.Repeat("x", 1025)},
		{Question: "q", Limit: 1, Subject: "\xff"}, {Question: "q", Limit: 1, Subject: "a\x00b"},
		{Question: "q", Limit: 1, Role: "developer"},
	}
	for i, q := range invalid {
		if !errors.Is(q.Validate(), passage.ErrInvalidQuery) {
			t.Fatalf("invalid query %d accepted", i)
		}
	}
	for _, role := range []string{"", "user", "assistant", "system", "tool"} {
		q := passage.Query{Question: strings.Repeat("x", 8192), Limit: 32, Candidates: 1000, Subject: strings.Repeat("x", 1024), Role: role}
		if err := q.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := passage.New(nil, nil, passage.Binding{}); !errors.Is(err, passage.ErrConfiguration) {
		t.Fatal("absent dependencies accepted")
	}
}
