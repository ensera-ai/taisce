// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

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
	"io"
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
	"github.com/ensera-ai/taisce/internal/entitycandidate"
	"github.com/ensera-ai/taisce/internal/extract"
	"github.com/ensera-ai/taisce/internal/formation"
	"github.com/ensera-ai/taisce/internal/infra/inference"
	"github.com/ensera-ai/taisce/internal/infra/pg"
	"github.com/ensera-ai/taisce/internal/migrate"
	"github.com/ensera-ai/taisce/internal/notify"
	"github.com/ensera-ai/taisce/internal/passage"
	"github.com/ensera-ai/taisce/internal/recall"
	"github.com/ensera-ai/taisce/internal/report"
	"github.com/ensera-ai/taisce/internal/reportcandidate"
)

// Environment. Prefixed, because a process reading a bare DATABASE_URL picks up whatever the shell
// that started it happened to export.
const (
	envMemoryDSN   = "TAISCE_MEMORY_DSN"
	envRegistryDSN = "TAISCE_REGISTRY_DSN"
	envSchema      = "TAISCE_SCHEMA"
	envAddr        = "TAISCE_ADDR"
	// The bundle budget, in CHARACTERS. Not tokens: a token count is a property of the caller's
	// tokeniser, not ours, and one computed with the wrong tokeniser is wrong in a way they cannot
	// correct. Not rows: a row is not a size.
	envBundleBudget = "TAISCE_BUNDLE_CHARACTERS"
	// The identity that may change the schema. Only the operator commands use it; the server never
	// holds a connection that can run DDL.
	envAdminDSN = "TAISCE_ADMIN_DSN"
	// Which halves of the service this process runs: "all" (default), "api" or "worker".
	envRole             = "TAISCE_ROLE"
	envTurnBudget       = "TAISCE_FORMATION_TURN_BUDGET"
	envRequireFormation = "TAISCE_REQUIRE_FORMATION"
	envHealthAddr       = "TAISCE_HEALTH_ADDR"
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
// version is stamped at build time by the Makefile, from scripts/version.sh.
//
// "unknown" when built without it — an honest answer rather than a plausible wrong one. A binary
// reporting a version it does not have is worse than one admitting it does not know, because the
// first is believed.
var version = "unknown"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := dispatch(ctx, log, os.Args[1:]); err != nil {
		reportFailure(os.Args[1:], err, log, os.Stderr)
		os.Exit(1)
	}
}

// reportFailure says why the process is exiting. The server's failure goes to its log, beside
// everything else it logged. A command's failure goes to stderr, as the error panel in a terminal
// and as one JSON line otherwise, because stdout is the command's output: a script parsing it must
// not read the failure as data.
func reportFailure(args []string, err error, serverLog *slog.Logger, stderr io.Writer) {
	if len(args) == 0 || args[0] == "serve" {
		serverLog.Error("taisce failed", "error", err)
		return
	}
	if fancyOutput(stderr) {
		_, _ = fmt.Fprint(stderr, renderFailure(currentTheme(), err))
		return
	}
	slog.New(slog.NewJSONHandler(stderr, nil)).Error("taisce failed", "error", err)
}

// run is everything main does that can be tested: configuration, connection, serving and shutdown.
//
// The context arrives as an argument rather than being built here, so a test can end the server the
// same way a signal does. Signal handling stays in main, where the process boundary is.
func run(ctx context.Context, log *slog.Logger) error {
	ctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	if strings.TrimSpace(os.Getenv(envRole)) == roleManage {
		return runManage(ctx, log)
	}
	role := strings.TrimSpace(os.Getenv(envRole))
	if role == "" {
		role = roleAll
	}
	if role != roleAll && role != roleAPI && role != roleWorker {
		return fmt.Errorf("%s is %q; it is one of %q, %q, %q or %q", envRole, role, roleAll, roleAPI, roleWorker, roleManage)
	}
	// Only a role that authenticates holds the registry connection. A worker forms turns and resolves
	// no credentials, so it is never given the login that can read them: least privilege is what the
	// process holds, not what it happens to use.
	serves := role != roleWorker
	memoryDSN := strings.TrimSpace(os.Getenv(envMemoryDSN))
	if memoryDSN == "" {
		return fmt.Errorf("%s is not set", envMemoryDSN)
	}
	// Defaulting the registry connection to the memory one would start, work, and quietly put
	// credentials within reach of every memory statement. It is required separately so that the
	// separation cannot be arrived at by omission.
	var registryDSN string
	if serves {
		registryDSN = strings.TrimSpace(os.Getenv(envRegistryDSN))
		if registryDSN == "" {
			return fmt.Errorf("%s is not set; the registry connection is separate from the memory one "+
				"so that a memory query cannot read a credential", envRegistryDSN)
		}
	}
	schema, err := configuredMemorySchema()
	if err != nil {
		return fmt.Errorf("schema: %w", err)
	}

	memory, err := migrate.NewRuntimePool(ctx, memoryDSN, schema, migrate.MemoryPlane)
	if err != nil {
		return fmt.Errorf("connect memory: %w", err)
	}
	defer memory.Close()
	var registry *pgxpool.Pool
	if serves {
		registry, err = migrate.NewRuntimePool(ctx, registryDSN, schema, migrate.RegistryPlane)
		if err != nil {
			return fmt.Errorf("connect registry: %w", err)
		}
		defer registry.Close()
	}

	// Fail at startup rather than on the first request. A process that binds a port and then cannot
	// reach its database is a healthy-looking instance that answers everything with an error.
	if err := memory.Ping(ctx); err != nil {
		return fmt.Errorf("memory database unreachable: %w", err)
	}
	if registry != nil {
		if err := registry.Ping(ctx); err != nil {
			return fmt.Errorf("registry database unreachable: %w", err)
		}
	}

	// And fail at startup rather than on somebody's write. Both pools are counted, because the
	// server does not care which of this process's two pools took a slot — a deployment that can
	// open its memory pool and not its registry pool authenticates nobody.
	type namedPool struct {
		name string
		pool *pgxpool.Pool
	}
	pools := []namedPool{{"memory", memory}}
	if registry != nil {
		pools = append(pools, namedPool{"registry", registry})
	}
	for _, p := range pools {
		budget, err := migrate.CheckConnectionBudget(ctx, p.pool)
		if err != nil {
			return fmt.Errorf("%s pool: %w", p.name, err)
		}
		log.Info("connection budget", "pool", p.name, "wanted", budget.Wanted,
			"available", budget.Available(), "max_connections", budget.MaxConnections)
	}

	requireFormation, err := formationRequired()
	if err != nil {
		return err
	}
	var workerAddr string
	if role == roleWorker {
		workerAddr, err = workerHealthAddress()
		if err != nil {
			return err
		}
	}

	admission, _ := api.NewAdmission(4, 4)
	if role != roleWorker {
		admission, err = api.NewAdmission(memory.Config().MaxConns, registry.Config().MaxConns)
		if err != nil {
			return err
		}
	}
	defer func() {
		stats := admission.Stats()
		log.Info("HTTP admission totals", "accepted", stats.Accepted, "refused", stats.Refused, "anonymous_audit_suppressed", stats.AuditSuppressed)
	}()
	var passages *passage.Retriever
	var entityCandidates *entitycandidate.Retriever
	var reportCandidates *reportcandidate.Retriever
	if role != roleWorker {
		passages, err = configuredPassages(memory, schema)
		if err != nil {
			return err
		}
		entityCandidates, err = configuredEntityCandidates(memory, schema)
		if err != nil {
			return err
		}
		reportCandidates, err = configuredReportCandidates(memory, schema)
		if err != nil {
			return err
		}
	}
	// One record store, shared: promoting feedback runs the correction and the retraction through
	// this instance, in the promotion's own transaction, so both surfaces cannot drift apart.
	records := pg.NewRecordStore(memory, schema)
	// The memory surface, for the roles that serve it. A worker serves only its health probes.
	var server *api.Server
	if serves {
		server = api.NewServer(
			credential.NewStore(registry, string(migrate.ControlSchema)), api.Stores{
				Audit:        pg.NewAuditStore(memory, schema),
				Exporter:     pg.NewExporter(memory, schema),
				Projects:     pg.NewProjectStore(memory, schema),
				Observations: pg.NewObservationStore(memory),
				Eraser:       pg.NewEraser(memory),
				Recaller: recall.NewWithBudget(pg.NewRecallStore(memory, schema), bundleBudget(log)).
					WithSemantic(api.NewSemanticSurfaces(entityCandidates, reportCandidates, passages, pg.NewCommunityStore(memory, schema))),
				Citations:        pg.NewCitationStore(memory, schema),
				Records:          records,
				Feedback:         pg.NewFeedbackStore(memory, schema, records),
				Artifacts:        pg.NewArtifactStore(memory, schema),
				Subjects:         pg.NewSubjectStore(memory, schema),
				Contexts:         pg.NewSegmentStore(memory, schema),
				Passages:         passages,
				EntityCandidates: entityCandidates,
				ReportCandidates: reportCandidates,
				Notifications:    pg.NewNotificationStore(memory, schema),
				// The operator's policy about where this deployment may send. Unconfigured, the routes
				// refuse and nothing outbound is ever attempted.
				Notifier: notify.NewSender(notify.FromEnv(), log),
			}, schema,
			log,
			admission,
		)
	}

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
	// Cancel a started driver before deferred pool closure, including listener-start failures.
	defer cancelRun()
	processHealth := &processFormationHealth{}
	switch {
	case role == roleAPI:
		// Reads only. A deployment runs this when formation is being scaled separately, or paused
		// while a provider is unwell — turns still arrive and are stored, and they form when a
		// worker returns, because the backlog is in the database rather than in a process.
		log.Info("formation is not running in this process", "role", role)
		close(driverStopped)
	default:
		if err := startDriver(ctx, log, memory, schema, driverStopped, processHealth.responsive); err != nil {
			return err
		}
	}

	addr := os.Getenv(envAddr)
	if addr == "" {
		// Loopback would be wrong in a container and right on a laptop; a container is where this
		// runs, and the boundary in front of it is the deployment's job rather than a default here.
		addr = ":8080"
	}
	ready := api.NewReadiness(func(checkCtx context.Context) error {
		if role == roleWorker {
			if !processHealth.current() {
				return fmt.Errorf("local formation is not responsive")
			}
			return pg.CheckServingHealth(checkCtx, memory, nil, schema, false)
		}
		return pg.CheckServingHealth(checkCtx, memory, registry, schema, requireFormation)
	})
	var handler http.Handler
	if role == roleWorker {
		// Only probes on loopback; no memory routes and no externally reachable worker service.
		addr = workerAddr
		handler = api.HealthHandler(ready)
	} else {
		handler = server.Handler(ready)
	}
	httpServer := &http.Server{
		Addr:    addr,
		Handler: handler,
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
			"contract", api.Version, "version", version)
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

func bundleBudget(log *slog.Logger) recall.Budget {
	b := recall.DefaultBudget()
	raw := strings.TrimSpace(os.Getenv(envBundleBudget))
	if raw == "" {
		return b
	}
	var n int
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil || n <= 0 {
		// A typo falls back rather than refusing to start. A deployment that cannot serve because a
		// number is misspelled is a worse outcome than one serving a default it can see in the log —
		// which is only true if the log says so.
		log.Warn("the recall budget is not a positive whole number, so the default is used",
			"variable", envBundleBudget, "value", raw, "characters", b.Characters)
		return b
	}
	b.Characters = n
	return b
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
	stopped chan struct{}, responsive ...func()) error {

	inferenceConfig, err := inference.ConfigFromEnv()
	if err != nil {
		log.Warn("MEMORY WILL NOT FORM: no model endpoint is configured, so turns will be stored "+
			"and never extracted. Storage, recall of what is already formed, and erasure all work. "+
			"Set TAISCE_INFERENCE_ENDPOINT, TAISCE_INFERENCE_EXTRACTOR_MODEL and "+
			"TAISCE_INFERENCE_ALLOWLIST to start forming.", "reason", err.Error())
		close(stopped)
		return nil
	}

	vocabulary, err := pg.LoadVocabulary(ctx, memory, schema)
	if err != nil {
		return fmt.Errorf("load the vocabulary: %w", err)
	}
	policy := formation.DefaultPolicy()
	if policy.TurnBudget, err = configuredTurnBudget(policy.TurnBudget); err != nil {
		return err
	}
	inferenceConfig.Timeout = policy.TurnBudget
	observations := pg.NewObservationStore(memory)
	former := formation.NewFormer(observations, pg.NewFactStore(memory),
		extract.NewWith(inference.NewModel(inferenceConfig), vocabulary))
	driver := formation.NewDriver(
		formation.NewWorkerWithPolicy(memory, observations, former, policy),
		observations, pg.NewRetentionStore(memory, schema),
		pg.NewAuditStore(memory, schema), schema, policy, log, responsive...).
		WithPasses(
			formation.NewSubjects(pg.NewCommunityStore(memory, schema), report.New(inference.NewReporter(inferenceConfig))),
			formation.NewCompaction(pg.NewSegmentStore(memory, schema), inference.NewSummariser(inferenceConfig))).
		WithNotifications(formation.NewNotifications(
			pg.NewNotificationStore(memory, schema), notify.NewSender(notify.FromEnv(), log)))

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

// configuredTurnBudget reads how long one formation attempt may take, or keeps the default.
//
// A conversational turn never approaches the default, but a document-sized turn under a shared
// model server can: measured on a news corpus, articles over ten kilobytes were cut off by the
// client while the model was still writing their claims. Until documents are segmented on the way
// in, this is the operator's only knob, so it is a setting rather than a constant. A value that is
// not a positive duration is refused at startup: a budget of zero would fail every turn, and a
// silently ignored typo would leave the operator believing they had raised it.
func configuredTurnBudget(fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(envTurnBudget))
	if raw == "" {
		return fallback, nil
	}
	budget, err := time.ParseDuration(raw)
	if err != nil || budget <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 5m or 900s, got %q", envTurnBudget, raw)
	}
	return budget, nil
}

// One namespace is selected for the entire instance at startup, never by a request or project.
// The optional setting preserves existing installations and isolated database test fixtures.
func configuredMemorySchema() (pg.Schema, error) {
	name := strings.TrimSpace(os.Getenv(envSchema))
	if name == "" {
		name = "memory"
	}
	if name == "control" || name == "public" || name == "information_schema" || strings.HasPrefix(name, "pg_") {
		return "", fmt.Errorf("%s selects a reserved namespace", envSchema)
	}
	return pg.NewSchema(name)
}
