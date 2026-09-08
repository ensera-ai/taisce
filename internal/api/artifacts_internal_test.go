// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/credential"
)

func TestArtifactStorageMustBeConfiguredBeforeServingItsRoutes(t *testing.T) {
	server := &Server{}
	for _, handler := range []grantedHandler{server.putArtifact, server.getArtifact, server.listArtifacts, server.deleteArtifact} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		handler(w, r, credential.Grant{})
		if w.Code != 500 {
			t.Fatal("unconfigured artifact storage appeared ready")
		}
	}
}
