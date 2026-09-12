// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ensera-ai/taisce/internal/infra/pg"
)

type processFormationHealth struct{ last atomic.Int64 }

func (h *processFormationHealth) responsive() { h.last.Store(time.Now().UnixNano()) }
func (h *processFormationHealth) current() bool {
	last := h.last.Load()
	return last > 0 && time.Since(time.Unix(0, last)) <= pg.FormationHealthMaxAge
}

func formationRequired() (bool, error) {
	value := strings.TrimSpace(os.Getenv(envRequireFormation))
	if value == "" {
		return false, nil
	}
	required, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", envRequireFormation)
	}
	return required, nil
}

func workerHealthAddress() (string, error) {
	addr := strings.TrimSpace(os.Getenv(envHealthAddr))
	if addr == "" {
		// Not 8081: that is the management surface's, and a worker and manage on one host would
		// otherwise collide on their defaults.
		addr = "127.0.0.1:8082"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("%s must name a loopback host and port", envHealthAddr)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("%s must bind a loopback IP", envHealthAddr)
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return "", fmt.Errorf("invalid worker health port")
	}
	return addr, nil
}

func healthCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: taisce health")
	}
	// Diagnostics can include database-wide pressure, so require an explicit operator connection.
	if strings.TrimSpace(os.Getenv(envAdminDSN)) == "" {
		return fmt.Errorf("%s is required for operator health", envAdminDSN)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	admin, schema, err := adminPool(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	status, err := pg.ReadOperationalHealth(ctx, admin, schema)
	if err != nil {
		return err
	}
	if fancyOutput(out) {
		_, err = io.WriteString(out, renderHealth(currentTheme(), status, inferenceIsConfigured()))
		return err
	}
	return json.NewEncoder(out).Encode(status)
}

// The distroless image probes with its own binary; no curl or shell dependency is needed.
func probeCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	live := flags.Bool("live", false, "check liveness")
	worker := flags.Bool("worker", false, "check the worker loopback listener")
	manage := flags.Bool("manage", false, "check the management listener")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("probe accepts only --live, --worker and --manage")
	}
	addr := strings.TrimSpace(os.Getenv(envAddr))
	if addr == "" {
		addr = ":8080"
	}
	if *worker {
		var err error
		addr, err = workerHealthAddress()
		if err != nil {
			return err
		}
	}
	if *manage {
		// The management surface has its own listener; its probe reads the same health
		// endpoints there.
		addr = strings.TrimSpace(os.Getenv(envManageAddr))
		if addr == "" {
			addr = "127.0.0.1:8081"
		}
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid local probe address")
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("probe requires a loopback listening address")
	}
	path := "/ready"
	if *live {
		path = "/health"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+path, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("local probe failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("local probe returned status %d", resp.StatusCode)
	}
	return nil
}
