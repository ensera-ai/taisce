// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestServingRefusesAPoolTooSmallToReserveAnotherProjectsCapacity(t *testing.T) {
	env := runtimeConfiguration(t)
	dsn, err := url.Parse(env[envMemoryDSN])
	if err != nil {
		t.Fatal(err)
	}
	params := dsn.Query()
	params.Set("pool_max_conns", "2")
	dsn.RawQuery = params.Encode()
	env[envMemoryDSN] = dsn.String()
	env[envRole] = roleAPI
	withEnv(t, env)
	if err := run(context.Background(), quiet()); err == nil || !strings.Contains(err.Error(), "at least 3 memory") {
		t.Fatalf("unsafe pool capacity accepted: %v", err)
	}
}
