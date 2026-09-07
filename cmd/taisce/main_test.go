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
// Re-executed as a subprocess, because what is being asserted is the exit status — which is the
// contract main has with whatever supervises it, and which cannot be observed from inside the
// process that would be exiting.
func TestTheBinaryExitsNonZeroWhenItCannotStart(t *testing.T) {
	if os.Getenv("TAISCE_MAIN_SUBPROCESS") == "1" {
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestTheBinaryExitsNonZeroWhenItCannotStart")
	cmd.Env = append(os.Environ(), "TAISCE_MAIN_SUBPROCESS=1", envMemoryDSN+"=")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the binary exited 0 with no configuration: %s", out)
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() == 0 {
		t.Fatalf("wanted a non-zero exit, got %v: %s", err, out)
	}
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
