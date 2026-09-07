// What the binary refuses to start with, and what it does once it has.
//
// These are startup behaviours rather than request behaviours, and they matter for the same reason
// the request ones do: a process that starts when it should not is an instance that looks healthy
// and answers everything with an error, and whatever is supervising it — a compose file, a
// Kubernetes probe, a person watching a terminal — has no way to tell.
//
// The refusals are asserted one at a time from a complete configuration, so each test proves the one
// thing it names rather than a general unwillingness to start.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"

	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
	"time"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// complete returns a configuration that would start, so a test can remove exactly one thing.
func complete(t *testing.T) map[string]string {
	t.Helper()
	dsn := os.Getenv("TAISCE_TEST_DSN")
	if dsn == "" {
		t.Skip("TAISCE_TEST_DSN is not set")
	}
	return map[string]string{
		envMemoryDSN:   dsn,
		envRegistryDSN: dsn,
		envTenant:      "api_journey",
		envAddr:        "127.0.0.1:0",
	}
}

func withEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// Each of these is a way for a deployment to be wrong, and each has to stop the process rather than
// be defaulted into something that happens to run.
func TestStartingWithoutRequiredConfigurationIsRefused(t *testing.T) {
	for _, missing := range []string{envMemoryDSN, envTenant} {
		t.Run(missing, func(t *testing.T) {
			env := complete(t)
			env[missing] = ""
			withEnv(t, env)
			if err := run(context.Background(), quiet()); err == nil {
				t.Fatalf("started with %s unset", missing)
			} else if !strings.Contains(err.Error(), missing) {
				t.Fatalf("the error does not name what is missing: %v", err)
			}
		})
	}
}

// The registry connection is required separately, and this is the one worth its own test.
//
// Defaulting it to the memory connection would start, work, and quietly put every credential within
// reach of every memory statement — the boundary would still be drawn in the migration and would no
// longer be true in the process. So omission is a refusal rather than a fallback.
func TestTheRegistryConnectionCannotBeArrivedAtByOmission(t *testing.T) {
	env := complete(t)
	env[envRegistryDSN] = ""
	withEnv(t, env)

	err := run(context.Background(), quiet())
	if err == nil {
		t.Fatal("started with no registry connection, which would mean sharing the memory one")
	}
	if !strings.Contains(err.Error(), envRegistryDSN) {
		t.Fatalf("the error does not name the missing connection: %v", err)
	}
	// The reason is in the message, because the person who hits this is deciding whether to just
	// point both at the same place.
	if !strings.Contains(err.Error(), "credential") {
		t.Fatalf("the refusal does not say why the connection is separate: %v", err)
	}
}

// A schema name is interpolated rather than bound, so an unusable one is refused before it reaches a
// statement rather than after.
func TestAnUnusableSchemaNameIsRefusedAtStartup(t *testing.T) {
	env := complete(t)
	env[envTenant] = "not a valid identifier; DROP SCHEMA public"
	withEnv(t, env)

	if err := run(context.Background(), quiet()); err == nil {
		t.Fatal("started with a schema name that cannot be an identifier")
	}
}

// A database that cannot be reached is a startup failure, not a runtime surprise.
func TestAnUnreachableDatabaseStopsTheProcessStarting(t *testing.T) {
	env := complete(t)
	env[envMemoryDSN] = "postgres://nobody:nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=2"
	withEnv(t, env)

	if err := run(context.Background(), quiet()); err == nil {
		t.Fatal("started against a database it cannot reach")
	}
}

// The whole of run: it starts, serves, and stops when its context ends.
//
// Asserted by asking the running process a question over HTTP, because a server that starts and does
// not answer is the failure this is for.
func TestItServesAndShutsDownWhenItsContextEnds(t *testing.T) {
	port := freePort(t)
	env := complete(t)
	env[envAddr] = fmt.Sprintf("127.0.0.1:%d", port)
	withEnv(t, env)

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- run(ctx, quiet()) }()

	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	if !reachable(url, 5*time.Second) {
		cancel()
		t.Fatal("the process started and never answered")
	}

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("shutdown returned an error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the process did not stop when its context ended")
	}
}

// A port already in use is a startup failure with a reason, rather than a goroutine that dies while
// the process reports itself started.
func TestAPortAlreadyInUseStopsTheProcessStarting(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer held.Close()

	env := complete(t)
	env[envAddr] = held.Addr().String()
	withEnv(t, env)

	if err := run(context.Background(), quiet()); err == nil {
		t.Fatal("started on a port something else already holds")
	}
}

// The bundle cap falls back rather than failing, because a malformed number is a deployment typo and
// refusing to start over one would be a worse outcome than a default the operator can see in the log.
func TestTheBundleCapFallsBackRatherThanFailing(t *testing.T) {
	for _, value := range []string{"", "not-a-number", "0", "-5"} {
		t.Setenv(envBundleLimit, value)
		if n := bundleLimit(); n <= 0 {
			t.Fatalf("%q produced a cap of %d, which would return nothing", value, n)
		}
	}
	t.Setenv(envBundleLimit, "7")
	if n := bundleLimit(); n != 7 {
		t.Fatalf("a valid cap was ignored: %d", n)
	}
}

// ── main itself ───────────────────────────────────────────────────────────────────────────────
//
// Re-executed as a subprocess, because what is asserted is the EXIT STATUS — the contract main has
// with whatever supervises it, and the thing that cannot be observed from inside the process that
// would be exiting. A container that exits zero on a failure is one an orchestrator restarts
// forever without ever reporting a problem.
func TestTheBinaryExitsNonZeroWhenItFails(t *testing.T) {
	if os.Getenv("TAISCE_MAIN_SUBPROCESS") == "1" {
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestTheBinaryExitsNonZeroWhenItFails")
	cmd.Env = append(os.Environ(), "TAISCE_MAIN_SUBPROCESS=1", envMemoryDSN+"=")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the binary exited 0 on a failure: %s", out)
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() == 0 {
		t.Fatalf("wanted a non-zero exit, got %v: %s", err, out)
	}
}

// ── The commands ──────────────────────────────────────────────────────────────────────────────

// No subcommand means serve, because the container's command is the common case and a deployment
// that has to remember the word is one that fails at start with a usage message.
func TestNoSubcommandMeansServe(t *testing.T) {
	env := complete(t)
	env[envMemoryDSN] = ""
	withEnv(t, env)

	err := dispatch(context.Background(), quiet(), nil)
	if err == nil {
		t.Fatal("dispatch with no arguments did not reach serve")
	}
	if !strings.Contains(err.Error(), envMemoryDSN) {
		t.Fatalf("it routed somewhere other than serve: %v", err)
	}
}

func TestAnUnknownCommandIsAnError(t *testing.T) {
	if err := dispatch(context.Background(), quiet(), []string{"frobnicate"}); err == nil {
		t.Fatal("an unknown command succeeded")
	}
	if err := dispatch(context.Background(), quiet(), []string{"help"}); err != nil {
		t.Fatalf("help failed: %v", err)
	}
}

// Bootstrap brings an empty database to a state that can serve, and it does so twice.
//
// Twice matters: the compose file and the chart both run it on every start, so a bootstrap that
// fails the second time is a deployment that cannot restart. And it must not mint a second
// credential on the second run, or every restart leaves a key nobody can account for.
func TestBootstrapIsIdempotentAndMintsOneCredential(t *testing.T) {
	env := complete(t)
	env[envTenant] = "cmd_bootstrap"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv("TAISCE_CONTROL_PASSWORD", "taisce-test-control")
	t.Setenv("TAISCE_DATA_PASSWORD", "taisce-test-data")

	ctx := context.Background()
	dropSchema(t, "cmd_bootstrap")

	if err := dispatch(ctx, quiet(), []string{"bootstrap", "-project", "cmd_bootstrap_p1"}); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	first := credentialsGranting(t, "cmd_bootstrap_p1")
	if first != 1 {
		t.Fatalf("bootstrap left %d credentials able to reach the project, wanted exactly one", first)
	}

	if err := dispatch(ctx, quiet(), []string{"bootstrap", "-project", "cmd_bootstrap_p1"}); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if second := credentialsGranting(t, "cmd_bootstrap_p1"); second != first {
		t.Fatalf("a restart minted another credential: %d then %d", first, second)
	}
}

// The passwords for the two database identities have no default, because a default is a credential
// that works because nobody chose one.
func TestBootstrapRefusesWithoutTheIdentityPasswords(t *testing.T) {
	env := complete(t)
	env[envTenant] = "cmd_nopass"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv("TAISCE_CONTROL_PASSWORD", "")
	t.Setenv("TAISCE_DATA_PASSWORD", "")

	err := dispatch(context.Background(), quiet(), []string{"bootstrap"})
	if err == nil {
		t.Fatal("bootstrapped with no passwords for the two identities")
	}
	if !strings.Contains(err.Error(), "TAISCE_CONTROL_PASSWORD") {
		t.Fatalf("the refusal does not say what is missing: %v", err)
	}
}

// Creating a project is a controlled operation, and creating one twice is not a failure — the chart
// and the compose file both run it on every start.
func TestCreatingAProjectIsRepeatable(t *testing.T) {
	env := complete(t)
	env[envTenant] = "cmd_project"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv("TAISCE_CONTROL_PASSWORD", "taisce-test-control")
	t.Setenv("TAISCE_DATA_PASSWORD", "taisce-test-data")

	ctx := context.Background()
	dropSchema(t, "cmd_project")
	if err := dispatch(ctx, quiet(), []string{"bootstrap", "-project", "p1"}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := dispatch(ctx, quiet(), []string{"project", "create", "p2"}); err != nil {
			t.Fatalf("create p2 (attempt %d): %v", i, err)
		}
	}
	if err := dispatch(ctx, quiet(), []string{"project", "list"}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := dispatch(ctx, quiet(), []string{"project", "nonsense"}); err == nil {
		t.Fatal("an unknown project command succeeded")
	}
}

// A credential can be minted and revoked from the command line, which is how an operator recovers
// from a lost token without a portal.
func TestACredentialCanBeMintedAndRevokedFromTheCommandLine(t *testing.T) {
	env := complete(t)
	env[envTenant] = "cmd_cred"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv("TAISCE_CONTROL_PASSWORD", "taisce-test-control")
	t.Setenv("TAISCE_DATA_PASSWORD", "taisce-test-data")

	ctx := context.Background()
	dropSchema(t, "cmd_cred")
	if err := dispatch(ctx, quiet(), []string{"bootstrap", "-project", "cmd_cred_p1"}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	before := credentialsGranting(t, "cmd_cred_p1")
	if err := dispatch(ctx, quiet(), []string{"credential", "issue", "second", "-project", "cmd_cred_p1"}); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if after := credentialsGranting(t, "cmd_cred_p1"); after != before+1 {
		t.Fatalf("issuing produced %d credentials for the project, wanted %d", after, before+1)
	}
	if err := dispatch(ctx, quiet(), []string{"credential", "revoke", "not-a-uuid"}); err == nil {
		t.Fatal("revoking an unknown credential succeeded")
	}
	if err := dispatch(ctx, quiet(), []string{"credential", "nonsense"}); err == nil {
		t.Fatal("an unknown credential command succeeded")
	}
}

func dropSchema(t *testing.T, name string) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+name+` CASCADE`); err != nil {
		t.Fatalf("drop %s: %v", name, err)
	}
}

// credentialsGranting counts the live credentials that can reach one project.
//
// Counted per project rather than instance-wide, because every test in this repository shares one
// database and a global count would make this test depend on what else ran first.
func credentialsGranting(t *testing.T, project string) int {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM control.credential WHERE revoked_at IS NULL AND project = $1`,
		project).Scan(&n); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	return n
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}
	return port
}

func reachable(url string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// With a model configured, the process starts the formation driver alongside the server.
//
// The endpoint is unreachable on purpose: what is under test is that the driver is WIRED, not that
// extraction works. A deployment where the driver silently never starts is one that stores turns,
// never forms them, and answers every recall with nothing — which looks like a memory that does not
// work rather than a process that was misconfigured.
func TestAConfiguredModelStartsTheFormationDriver(t *testing.T) {
	port := freePort(t)
	env := complete(t)
	env[envAddr] = fmt.Sprintf("127.0.0.1:%d", port)
	withEnv(t, env)

	// Allowlisted, because the allowlist is closed when unset and this would otherwise be refused
	// before it ever reached a socket — which is the fail-closed default doing its job.
	t.Setenv("TAISCE_INFERENCE_ENDPOINT", "http://127.0.0.1:1/v1")
	t.Setenv("TAISCE_INFERENCE_EXTRACTOR_MODEL", "unreachable-on-purpose")
	t.Setenv("TAISCE_INFERENCE_ALLOWLIST", "http://127.0.0.1:1")

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- run(ctx, quiet()) }()

	if !reachable(fmt.Sprintf("http://127.0.0.1:%d/health", port), 5*time.Second) {
		cancel()
		t.Fatal("the process did not serve with a model configured")
	}
	cancel()

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("shutdown returned an error: %v", err)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("the process did not stop; the driver may not be honouring its context")
	}
}

// The operator commands refuse the same configuration mistakes the server does, and say which.
//
// They are a separate entry point into the same database, so a refusal the server makes and these do
// not is a way to half-create a schema with a connection that was never checked.
func TestTheOperatorCommandsRefuseMissingConfiguration(t *testing.T) {
	base := complete(t)

	t.Run("no admin or memory connection", func(t *testing.T) {
		env := map[string]string{envAdminDSN: "", envMemoryDSN: "", envTenant: base[envTenant]}
		withEnv(t, env)
		if err := dispatch(context.Background(), quiet(), []string{"project", "list"}); err == nil {
			t.Fatal("ran with no connection at all")
		}
	})

	t.Run("no schema", func(t *testing.T) {
		env := map[string]string{envAdminDSN: base[envMemoryDSN], envMemoryDSN: base[envMemoryDSN], envTenant: ""}
		withEnv(t, env)
		err := dispatch(context.Background(), quiet(), []string{"project", "list"})
		if err == nil {
			t.Fatal("ran with no schema named")
		}
		if !strings.Contains(err.Error(), envTenant) {
			t.Fatalf("the refusal does not name what is missing: %v", err)
		}
	})

	t.Run("unusable schema name", func(t *testing.T) {
		env := map[string]string{envAdminDSN: base[envMemoryDSN], envTenant: "not a; identifier"}
		withEnv(t, env)
		if err := dispatch(context.Background(), quiet(), []string{"project", "list"}); err == nil {
			t.Fatal("ran with a schema name that cannot be an identifier")
		}
	})

	t.Run("unreachable database", func(t *testing.T) {
		env := map[string]string{
			envAdminDSN: "postgres://nobody:nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=2",
			envTenant:   base[envTenant],
		}
		withEnv(t, env)
		if err := dispatch(context.Background(), quiet(), []string{"project", "list"}); err == nil {
			t.Fatal("ran against a database it cannot reach")
		}
	})
}

// Each command states its own usage when called with nothing, because a bare `taisce project` that
// silently does nothing is worse than one that says what it wanted.
func TestEachCommandSaysWhatItWantedWhenCalledBare(t *testing.T) {
	env := complete(t)
	env[envAdminDSN] = env[envMemoryDSN]
	env[envTenant] = "cmd_usage"
	withEnv(t, env)

	for name, args := range map[string][]string{
		"project":           {"project"},
		"project create":    {"project", "create"},
		"credential":        {"credential"},
		"credential issue":  {"credential", "issue"},
		"credential revoke": {"credential", "revoke"},
	} {
		t.Run(name, func(t *testing.T) {
			err := dispatch(context.Background(), quiet(), args)
			if err == nil {
				t.Fatal("ran with no arguments and reported success")
			}
			if !strings.Contains(err.Error(), "usage") {
				t.Fatalf("the error does not say what was wanted: %v", err)
			}
		})
	}
}

// A credential cannot be minted for a project that does not exist.
//
// The check a foreign key would make, in the only place that can make it: the credential is in the
// registry schema and the project is in the tenant schema, and no constraint crosses that boundary
// because crossing it is what the boundary prevents. Without it a typo mints a token that
// authenticates successfully and reaches no memory — which looks like a permissions bug to whoever
// debugs it.
func TestACredentialCannotBeMintedForAProjectThatDoesNotExist(t *testing.T) {
	env := complete(t)
	env[envTenant] = "cmd_nosuchproject"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv("TAISCE_CONTROL_PASSWORD", "taisce-test-control")
	t.Setenv("TAISCE_DATA_PASSWORD", "taisce-test-data")

	ctx := context.Background()
	dropSchema(t, "cmd_nosuchproject")
	if err := dispatch(ctx, quiet(), []string{"bootstrap", "-project", "real"}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	err := dispatch(ctx, quiet(), []string{"credential", "issue", "typo", "-project", "raal"})
	if err == nil {
		t.Fatal("minted a credential for a project that does not exist")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}

	// And the real one works, so the check is not simply refusing everything.
	if err := dispatch(ctx, quiet(), []string{"credential", "issue", "good", "-project", "real"}); err != nil {
		t.Fatalf("minting for a real project failed: %v", err)
	}
}

// ── Roles ─────────────────────────────────────────────────────────────────────────────────────
//
// One image, and its command decides what it does. Two binaries would be two build pipelines and two
// version numbers that can disagree, paid permanently for a separation that is a runtime choice.

// The api role serves and does not form. A deployment runs this to scale reads separately, or to
// pause formation while a provider is unwell — turns still arrive and are stored, because the backlog
// is in the database rather than in a process.
func TestTheAPIRoleServesWithoutForming(t *testing.T) {
	port := freePort(t)
	env := complete(t)
	env[envAddr] = fmt.Sprintf("127.0.0.1:%d", port)
	env[envRole] = "api"
	withEnv(t, env)
	// Configured, so that what stops formation is the role rather than a missing endpoint.
	t.Setenv("TAISCE_INFERENCE_ENDPOINT", "http://127.0.0.1:1/v1")
	t.Setenv("TAISCE_INFERENCE_EXTRACTOR_MODEL", "unreachable-on-purpose")
	t.Setenv("TAISCE_INFERENCE_ALLOWLIST", "127.0.0.1:1")

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- run(ctx, quiet()) }()

	if !reachable(fmt.Sprintf("http://127.0.0.1:%d/health", port), 5*time.Second) {
		cancel()
		t.Fatal("the api role did not serve")
	}
	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the api role did not stop when its context ended")
	}
}

// The worker role forms and binds nothing.
//
// No listener at all, deliberately: a worker holding a port is one an orchestrator can route traffic
// to by accident, and a health check that passed would say the wrong thing about a process that
// serves no reads.
func TestTheWorkerRoleFormsAndBindsNothing(t *testing.T) {
	port := freePort(t)
	env := complete(t)
	env[envAddr] = fmt.Sprintf("127.0.0.1:%d", port)
	env[envRole] = "worker"
	withEnv(t, env)
	t.Setenv("TAISCE_INFERENCE_ENDPOINT", "http://127.0.0.1:1/v1")
	t.Setenv("TAISCE_INFERENCE_EXTRACTOR_MODEL", "unreachable-on-purpose")
	t.Setenv("TAISCE_INFERENCE_ALLOWLIST", "127.0.0.1:1")

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- run(ctx, quiet()) }()

	// It must NOT answer. Given a moment to bind if it were going to.
	if reachable(fmt.Sprintf("http://127.0.0.1:%d/health", port), 2*time.Second) {
		cancel()
		t.Fatal("the worker role bound a port, which an orchestrator could route traffic to")
	}
	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the worker role did not stop when its context ended")
	}
}

// A role nobody defined is refused rather than defaulted.
//
// Defaulting a typo to "all" would give a deployment that quietly runs both halves in every pod —
// which is the state the split existed to avoid, arrived at by a spelling mistake.
func TestAnUnknownRoleIsRefused(t *testing.T) {
	env := complete(t)
	env[envRole] = "aip"
	withEnv(t, env)

	err := run(context.Background(), quiet())
	if err == nil {
		t.Fatal("started with a role nobody defined")
	}
	if !strings.Contains(err.Error(), envRole) {
		t.Fatalf("the refusal does not name the setting: %v", err)
	}
}

// A project name that cannot be an identifier is refused.
//
// It is interpolated into a partition name and an index name, because DDL cannot take a bind value —
// so validation here is not tidiness, it is the thing standing between a project name and a
// statement.
func TestAProjectNameThatCannotBeAnIdentifierIsRefused(t *testing.T) {
	env := complete(t)
	env[envTenant] = "cmd_badname"
	env[envAdminDSN] = env[envMemoryDSN]
	withEnv(t, env)
	t.Setenv("TAISCE_CONTROL_PASSWORD", "taisce-test-control")
	t.Setenv("TAISCE_DATA_PASSWORD", "taisce-test-data")

	ctx := context.Background()
	dropSchema(t, "cmd_badname")
	if err := dispatch(ctx, quiet(), []string{"bootstrap", "-project", "ok"}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	for _, name := range []string{
		"has spaces",
		"Uppercase",
		"1leading-digit",
		"quote'; DROP SCHEMA public; --",
		"",
	} {
		if err := dispatch(ctx, quiet(), []string{"project", "create", name}); err == nil {
			t.Fatalf("created a project named %q", name)
		}
	}

	// And the schema survived every one of them, which is the assertion that matters most.
	var alive bool
	pool, err := pgxpool.New(ctx, os.Getenv("TAISCE_TEST_DSN"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'public')`).
		Scan(&alive); err != nil {
		t.Fatalf("check: %v", err)
	}
	if !alive {
		t.Fatal("a project name reached a statement")
	}
}
