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

// managementHandler is the manage role's handler: the surface, and the portal only when
// TAISCE_PORTAL is "on". Off, the portal's routes do not exist, which is the only way a browser
// surface is not exposed by default.
func managementHandler(server *api.ManagementServer, ready http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", server.Handler(ready))
	if strings.EqualFold(strings.TrimSpace(os.Getenv(envPortal)), "on") {
		api.NewPortal(server).Mount(mux)
	}
	return mux
}

// runManage serves the management surface and nothing else: no memory route, no formation.
// It holds the administrative connection, because creating a project is DDL, and that is the
// reason it is a process of its own rather than a route on the memory server.
func runManage(ctx context.Context, log *slog.Logger) error {
	// The administrative connection, named, or nothing. adminPool falls back to the memory
	// connection for one-shot commands on a laptop; a long-running management surface that did that
	// would start under the memory identity and fail on every operation instead of once, here.
	if strings.TrimSpace(os.Getenv(envAdminDSN)) == "" {
		return fmt.Errorf("%s is not set; the management surface holds the administrative connection "+
			"and does not start under the memory identity", envAdminDSN)
	}
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
		Health: func(ctx context.Context) (pg.OperationalHealth, error) {
			return pg.ReadOperationalHealth(ctx, admin, schema)
		},
	}, schema, log)
	addr := strings.TrimSpace(os.Getenv(envManageAddr))
	if addr == "" {
		addr = "127.0.0.1:8081"
	}
	ready := api.NewReadiness(func(checkCtx context.Context) error { return admin.Ping(checkCtx) })
	handler := managementHandler(server, ready)
	httpServer := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
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
