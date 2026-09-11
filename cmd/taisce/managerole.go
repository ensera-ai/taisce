// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
)

// runManage serves the management surface (D133) and nothing else: no memory route, no formation.
// It holds the administrative connection, because creating a project is DDL, and that is the
// reason it is a process of its own rather than a route on the memory server.
func runManage(ctx context.Context, log *slog.Logger) error {
	admin, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	if err := admin.Ping(ctx); err != nil {
		return fmt.Errorf("database unreachable: %w", err)
	}
	server := api.NewManagementServer(credential.NewStore(admin, string(migrate.ControlSchema)), api.ManagementStores{
		Projects: pg.NewProjectStore(admin, schema),
		Provision: func(ctx context.Context, scope string) error {
			return migrate.ProvisionScope(ctx, admin, schema.String(), scope)
		},
		Observations: pg.NewObservationStore(admin),
		Audit:        pg.NewAuditStore(admin, schema),
		Refusals:     pg.NewRefusalStore(admin, schema),
		Eraser:       pg.NewEraser(admin),
	}, schema, log)
	addr := strings.TrimSpace(os.Getenv(envManageAddr))
	if addr == "" {
		addr = "127.0.0.1:8081"
	}
	ready := api.NewReadiness(func(checkCtx context.Context) error { return admin.Ping(checkCtx) })
	httpServer := &http.Server{Addr: addr, Handler: server.Handler(ready), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	errs := make(chan error, 1)
	go func() {
		log.Info("serving the management surface", "addr", listener.Addr().String(), "schema", schema.String(), "prefix", api.ManagementPrefix, "version", version)
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		log.Info("shutting down")
		return httpServer.Shutdown(shutdown)
	}
}
