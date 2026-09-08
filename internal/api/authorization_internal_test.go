// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"github.com/ensera-ai/taisce/internal/credential"
	"testing"
)

func TestUnknownAuthorityAndFutureOperationsFailClosedForReaders(t *testing.T) {
	for _, access := range []credential.Access{"", "admin", credential.ReadOnly} {
		if permitted(access, "future.mutation") {
			t.Fatalf("%q admitted unknown mutation", access)
		}
	}
}
