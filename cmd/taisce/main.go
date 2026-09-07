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

	if err := run(ctx, log); err != nil {
		log.Error("taisce did not start", "error", err)
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
	driverStopped := make(chan struct{})
	if inferenceConfig, cfgErr := inference.ConfigFromEnv(); cfgErr != nil {
		log.Warn("MEMORY WILL NOT FORM: no model endpoint is configured, so turns will be stored "+
			"and never extracted. Storage, recall of what is already formed, and erasure all work. "+
			"Set TAISCE_INFERENCE_ENDPOINT, TAISCE_INFERENCE_EXTRACTOR_MODEL and "+
			"TAISCE_INFERENCE_ALLOWLIST to start forming.", "reason", cfgErr.Error())
		close(driverStopped)
	} else {
		vocabulary, vocErr := pg.LoadOntology(ctx, memory, schema)
		if vocErr != nil {
			return fmt.Errorf("load the relation vocabulary: %w", vocErr)
		}
		observations := pg.NewObservationStore(memory)
		former := formation.NewFormer(observations, pg.NewFactStore(memory),
			extract.New(inference.NewModel(inferenceConfig), vocabulary))
		policy := formation.DefaultPolicy()
		driver := formation.NewDriver(
			formation.NewWorkerWithPolicy(memory, observations, former, policy),
			observations, schema, policy, log)

		go func() {
			defer close(driverStopped)
			// Run returns only when the context ends, and a scope busy with another replica is not
			// an error here — that is the normal case when the chart runs more than one.
			if err := driver.Run(ctx); err != nil {
				log.Error("formation driver stopped early", "error", err)
			}
		}()
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
