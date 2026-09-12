// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/migrate"
)

func TestReservedMemoryNamespacesAreRefusedBeforeConnecting(t *testing.T) {
	for _, name := range []string{"control", "public", "information_schema", "pg_catalog", "pg_temp"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(envSchema, name)
			if _, err := configuredMemorySchema(); err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Fatalf("reserved namespace accepted: %v", err)
			}
		})
	}
}

func TestServingWithAdministrativeMemoryCredentialsIsRefused(t *testing.T) {
	env := runtimeConfiguration(t)
	env[envMemoryDSN] = complete(t)[envMemoryDSN]
	withEnv(t, env)
	if err := run(context.Background(), quiet()); !errors.Is(err, migrate.ErrUnsafeRuntimePrivileges) {
		t.Fatalf("administrative memory session reached serving: %v", err)
	}
}

func TestServingWithAdministrativeRegistryCredentialsIsRefused(t *testing.T) {
	env := runtimeConfiguration(t)
	env[envRegistryDSN] = complete(t)[envRegistryDSN]
	withEnv(t, env)
	if err := run(context.Background(), quiet()); !errors.Is(err, migrate.ErrUnsafeRuntimePrivileges) {
		t.Fatalf("administrative registry session reached serving: %v", err)
	}
}

func TestNonOwnerRuntimeRolesCanAuthenticateStoreExportAndErase(t *testing.T) {
	env := runtimeConfiguration(t)
	ctx := context.Background()
	owner, err := pgxpool.New(ctx, complete(t)[envMemoryDSN])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	project := "priv_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := migrate.ProvisionScope(ctx, owner, env[envSchema], project); err != nil {
		t.Fatal(err)
	}
	credentials := credential.NewStore(owner, "control")
	token, grant, err := credentials.Issue(ctx, "privilege journey", project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := credentials.Revoke(ctx, grant.CredentialID); err != nil {
			t.Error(err)
		}
	})
	env[envAddr] = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	env[envRole] = "api"
	withEnv(t, env)
	running, cancel := context.WithCancel(ctx)
	stopped := make(chan error, 1)
	go func() { stopped <- run(running, quiet()) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-stopped:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server did not stop")
		}
	})
	base := "http://" + env[envAddr]
	if !reachable(base+"/health", 5*time.Second) {
		t.Fatal("non-owner runtime did not start")
	}
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/v1/observations", `{"data_subject_id":"fixture","messages":[{"role":"user","content":"A memory stored with runtime privileges."}]}`, http.StatusCreated},
		{"/v1/exports", `{"data_subject_id":"fixture"}`, http.StatusOK},
		{"/v1/erasures", `{"data_subject_id":"fixture","reason":"fixture complete"}`, http.StatusOK},
	} {
		request, err := http.NewRequest(http.MethodPost, base+tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("%s returned %d, expected %d", tc.path, response.StatusCode, tc.status)
		}
	}
}
