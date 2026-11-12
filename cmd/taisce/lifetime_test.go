// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
)

// An operator credential is permanent only when somebody says the word for it. Zero is what a flag
// holds when a script's variable was empty, so it is refused rather than read as "never".
func TestAnOperatorCredentialIsPermanentOnlyWhenAskedForByName(t *testing.T) {
	for _, c := range []struct {
		args []string
		want time.Duration
	}{
		{nil, credential.OperatorLifetime},
		{[]string{"-lifetime-days", "30"}, 30 * 24 * time.Hour},
		{[]string{"-no-expiry"}, credential.NoExpiry},
	} {
		got, err := operatorLifetime(c.args)
		if err != nil || got != c.want {
			t.Errorf("operator issue <name> %v: lifetime %s, %v; want %s", c.args, got, err, c.want)
		}
	}
	for _, args := range [][]string{
		{"-lifetime-days", "0"},
		{"-lifetime-days", "-3"},
		{"-lifetime-days", "40000"},
		{"-no-expiry", "-lifetime-days", "30"},
		{"-no-expiry", "-lifetime-days", "90"},
		{"stray"},
	} {
		if got, err := operatorLifetime(args); err == nil {
			t.Errorf("operator issue <name> %v was accepted with lifetime %s", args, got)
		}
	}
}

// The line under a new token says when it ends and how to replace it, because the day it ends the
// only thing the operator will be shown is a refusal that says nothing.
func TestANewTokenSaysWhenItEnds(t *testing.T) {
	at := time.Date(2027, 2, 10, 9, 30, 0, 0, time.UTC)
	said := expirySaid(&at, "taisce operator issue <name>")
	if !strings.Contains(said, "2027-02-10 09:30 UTC") || !strings.Contains(said, "taisce operator issue <name>") {
		t.Errorf("a dated credential's line names neither its date nor its replacement: %q", said)
	}
	if never := expirySaid(nil, "x"); !strings.Contains(never, "no expiry") || !strings.Contains(never, "revoked") {
		t.Errorf("a credential with no date must say that is what it is: %q", never)
	}
}
