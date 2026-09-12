// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/credential"
)

func TestUnconfiguredRecordInspectionFailsClosed(t *testing.T) {
	s := &Server{}
	for _, handler := range []grantedHandler{s.listRecords, s.recordHistory, s.retractRecords, s.correctRecords, s.assertRecords} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		handler(w, r, credential.Grant{})
		if w.Code != 500 {
			t.Fatalf("missing store returned %d", w.Code)
		}
	}
}
