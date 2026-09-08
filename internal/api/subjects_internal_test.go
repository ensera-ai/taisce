// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/credential"
)

func TestSubjectStorageMustBeConfiguredBeforeServingItsRoutes(t *testing.T) {
	s := &Server{}
	for _, handler := range []grantedHandler{s.registerSubject, s.getSubject, s.listSubjects, s.updateSubject} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
		handler(w, r, credential.Grant{})
		if w.Code != 500 {
			t.Fatal("unconfigured registry appeared available")
		}
	}
}
