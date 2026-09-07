// Command taisce serves one instance's memory over HTTP.
//
// One binary, two database identities. The registry connection resolves credentials and the memory
// connection serves memory, and they are separate because the grant between them is what stops a
// memory query reading a key. A single pool would work, silently, and would make that boundary a
// convention.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ensera-ai/taisce/internal/api"
	"github.com/ensera-ai/taisce/internal/credential"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/recall"
)

// Environment. Prefixed, because a process reading a bare DATABASE_URL picks up whatever the shell
// that started it happened to export.
const (
	envMemoryDSN   = "TAISCE_MEMORY_DSN"
	envRegistryDSN = "TAISCE_REGISTRY_DSN"
	envTenant      = "TAISCE_SCHEMA"
	envAddr        = "TAISCE_ADDR"
	envBundleLimit = "TAISCE_BUNDLE_LIMIT"
	// The identity that may change the schema. Only the operator commands use it; the server never
	// holds a connection that can run DDL.
	envAdminDSN = "TAISCE_ADMIN_DSN"
	// Which halves of the service this process runs: "all" (default), "api" or "worker".
	envRole = "TAISCE_ROLE"
)

// Roles.
//
// # Why one binary and not two
//
// Serving reads and forming the backlog have genuinely different shapes: the API is fast and
// database-bound, formation is slow and model-bound, and one of them can be paused during a provider
// incident while the other keeps answering. Those are real reasons to run them apart.
//
// They are not reasons to BUILD them apart. Two binaries is two images, two build pipelines, two
// version numbers that can disagree, and a schema migration that has to land in both — paid
// permanently, for a separation that is a runtime choice. So this is one image whose command decides
// what it does, and running them apart becomes a line in a chart rather than a change to the code.
//
// The default runs both, because that is what one machine wants and because nothing here has been
// measured needing otherwise. Splitting should follow a number, not a diagram.
const (
	roleAll    = "all"
	roleAPI    = "api"
	roleWorker = "worker"
)

// main owns the two things a process owns and a function should not: where the signals come from,
// and the exit status.
//
// A misconfigured start exits non-zero rather than serving. Anything supervising this — a compose
// file, a Kubernetes probe, an operator watching a terminal — decides whether the instance is alive
// from that status, and a process that binds a port it cannot serve from is an instance that looks
// healthy and answers everything with an error.
func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := dispatch(ctx, log, os.Args[1:]); err != nil {
		log.Error("taisce failed", "error", err)
		os.Exit(1)
	}
}

// run is everything main does that can be tested: configuration, connection, serving and shutdown.
//
// The context arrives as an argument rather than being built here, so a test can end the server the
// same way a signal does. Signal handling stays in main, where the process boundary is.
func run(ctx context.Context, log *slog.Logger) error {
	memoryDSN := strings.TrimSpace(os.Getenv(envMemoryDSN))
	if memoryDSN == "" {
		return fmt.Errorf("%s is not set", envMemoryDSN)
	}
	// Defaulting the registry connection to the memory one would start, work, and quietly put
	// credentials within reach of every memory statement. It is required separately so that the
	// separation cannot be arrived at by omission.
	registryDSN := strings.TrimSpace(os.Getenv(envRegistryDSN))
	if registryDSN == "" {
		return fmt.Errorf("%s is not set; the registry connection is separate from the memory one "+
			"so that a memory query cannot read a credential", envRegistryDSN)
	}
	tenant := strings.TrimSpace(os.Getenv(envTenant))
	if tenant == "" {
		return fmt.Errorf("%s is not set", envTenant)
	}
	schema, err := pg.NewSchema(tenant)
	if err != nil {
		return fmt.Errorf("schema: %w", err)
	}

	memory, err := pgxpool.New(ctx, memoryDSN)
	if err != nil {
		return fmt.Errorf("connect memory: %w", err)
	}
	defer memory.Close()
	registry, err := pgxpool.New(ctx, registryDSN)
	if err != nil {
		return fmt.Errorf("connect registry: %w", err)
	}
	defer registry.Close()

	// Fail at startup rather than on the first request. A process that binds a port and then cannot
	// reach its database is a healthy-looking instance that answers everything with an error.
	if err := memory.Ping(ctx); err != nil {
		return fmt.Errorf("memory database unreachable: %w", err)
	}
	if err := registry.Ping(ctx); err != nil {
		return fmt.Errorf("registry database unreachable: %w", err)
	}

	server := api.NewServer(
		credential.NewStore(registry, string(migrate.ControlSchema)),
		pg.NewAuditStore(memory, schema),
		pg.NewExporter(memory, schema),
		pg.NewProjectStore(memory, schema),
		pg.NewObservationStore(memory),
		pg.NewEraser(memory),
		recall.New(pg.NewRecallStore(memory, schema), bundleLimit()),
		schema,
		log,
	)

	// The driver forms the backlog behind the append. Without it, turns are stored, freshness never
	// advances, and a recall returns nothing — a deployment that looks like it works and holds no
	// memory.
	//
	// Extraction needs a model, and there is no model that can be shipped. So a missing endpoint
	// does not stop the process: storage, recall of what is already formed, erasure and export all
	// work without one, and refusing to start would turn a five-minute install into a failure in
	// minute one. What it must not do is look fine, so it says so at every level an operator might
	// be reading.
	role := strings.TrimSpace(os.Getenv(envRole))
	if role == "" {
		role = roleAll
	}
	if role != roleAll && role != roleAPI && role != roleWorker {
		return fmt.Errorf("%s is %q; it is one of %q, %q or %q", envRole, role, roleAll, roleAPI, roleWorker)
	}

	driverStopped := make(chan struct{})
	switch {
	case role == roleAPI:
		// Reads only. A deployment runs this when formation is being scaled separately, or paused
		// while a provider is unwell — turns still arrive and are stored, and they form when a
		// worker returns, because the backlog is in the database rather than in a process.
		log.Info("formation is not running in this process", "role", role)
		close(driverStopped)
	default:
		if err := startDriver(ctx, log, memory, schema, driverStopped); err != nil {
			return err
		}
	}

	if role == roleWorker {
		// No listener at all. A worker that also bound a port would be one an orchestrator could
		// route traffic to by accident, and a health check that passed would say the wrong thing.
		log.Info("forming only; no HTTP listener", "role", role)
		<-ctx.Done()
		<-driverStopped
		return nil
	}

	addr := os.Getenv(envAddr)
	if addr == "" {
		// Loopback would be wrong in a container and right on a laptop; a container is where this
		// runs, and the boundary in front of it is the deployment's job rather than a default here.
		addr = ":8080"
	}
	httpServer := &http.Server{
		Addr:    addr,
		Handler: server.Handler(),
		// A request that never finishes holds a connection forever. These are deliberately generous
		// rather than tuned: no latency budget has been agreed, so a tight number here would be a
		// guess enforced on every caller.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	// The listener is opened before serving so that a port already in use is a startup failure with
	// a reason, rather than a goroutine that dies while the process reports itself started.
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	errs := make(chan error, 1)
	go func() {
		log.Info("serving", "addr", listener.Addr().String(), "schema", schema.String(),
			"contract", api.Version)
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		// In-flight requests finish. A recall cut off mid-answer is a caller who cannot tell a
		// restart from a failure.
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		log.Info("shutting down")
		err := httpServer.Shutdown(shutdown)

		// Wait for the driver, so a turn mid-extraction finishes or is abandoned deliberately rather
		// than the process disappearing underneath it. Its attempt is not counted against the turn
		// when the cause is a cancelled context, so a restart costs nothing.
		select {
		case <-driverStopped:
		case <-shutdown.Done():
			log.Warn("the formation driver did not stop within the shutdown budget")
		}
		return err
	}
}

func bundleLimit() int {
	const fallback = 50
	raw := strings.TrimSpace(os.Getenv(envBundleLimit))
	if raw == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

// startDriver brings up formation behind the append.
//
// Without it, turns are stored, freshness never advances, and a recall returns nothing — a deployment
// that looks like it works and holds no memory.
//
// # Why a missing model does not stop the process
//
// Extraction needs one and none can be shipped: no key belongs in an open repository, and a hosted
// provider would put a signup inside the first five minutes. Storage, recall of what is already
// formed, and erasure all work without a model, so refusing to start would turn a five-minute install
// into a failure in minute one.
//
// What it must not do is look fine. So it says so, loudly, at the level an operator reads.
func startDriver(ctx context.Context, log *slog.Logger, memory *pgxpool.Pool, schema pg.Schema,
	stopped chan struct{}) error {

	inferenceConfig, err := inference.ConfigFromEnv()
	if err != nil {
		log.Warn("MEMORY WILL NOT FORM: no model endpoint is configured, so turns will be stored "+
			"and never extracted. Storage, recall of what is already formed, and erasure all work. "+
			"Set TAISCE_INFERENCE_ENDPOINT, TAISCE_INFERENCE_EXTRACTOR_MODEL and "+
			"TAISCE_INFERENCE_ALLOWLIST to start forming.", "reason", err.Error())
		close(stopped)
		return nil
	}

	vocabulary, err := pg.LoadOntology(ctx, memory, schema)
	if err != nil {
		return fmt.Errorf("load the relation vocabulary: %w", err)
	}
	observations := pg.NewObservationStore(memory)
	former := formation.NewFormer(observations, pg.NewFactStore(memory),
		extract.New(inference.NewModel(inferenceConfig), vocabulary))
	policy := formation.DefaultPolicy()
	driver := formation.NewDriver(
		formation.NewWorkerWithPolicy(memory, observations, former, policy),
		observations, pg.NewRetentionStore(memory, schema), schema, policy, log)

	go func() {
		defer close(stopped)
		// Run returns only when the context ends, and a scope busy with another replica is not an
		// error here — that is the normal case when more than one of these is running.
		if err := driver.Run(ctx); err != nil {
			log.Error("formation driver stopped early", "error", err)
		}
	}()
	return nil
}
