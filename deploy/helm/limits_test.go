// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// The limits a release declares, as opposed to the objects it creates.
//
// These tests exist because the chart's other tests assert what the rendered manifest says, and
// the defects they were written for were things it did not say. An absent `pool_max_conns` and an
// absent `tcp_keepalives_idle` are both valid YAML and both lint clean; what they leave behind is a
// ceiling set by the node the scheduler picked and a lock held for as long as an operating system's
// default. So the property under test is not "the value is right" but "a value was chosen at all",
// written so that a connection string added later without one fails here rather than on a cluster.
package helm_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every DSN the chart builds says how many connections it may open.
//
// pgx sizes a pool it is not told about at the number of CPUs the process can see. In a cluster that
// is a property of scheduling: the same release declared 32 connections on a 32-CPU node and 4 on a
// 4-CPU one. For the API it is not only a pool setting — admission bounds concurrent work at one
// less than the memory pool — so three replicas behind one Service admitted 31, 11 and 3 requests at
// once, and which replica the Service picked decided whether a request was served or shed.
//
// The regex finds every connection string in the rendering rather than the ones this test knew
// about, so a role added later with an unsized pool fails here.
func TestEveryConnectionTheChartBuildsStatesWhatItMayOpen(t *testing.T) {
	dsn := regexp.MustCompile(`postgres://[^"\s]+`)
	out := render(t)
	found := dsn.FindAllString(out, -1)
	if len(found) < 5 {
		t.Fatalf("expected a connection string for the API's memory and registry, the worker's memory, the ops centre and bootstrap; found %d: %v", len(found), found)
	}
	for _, d := range found {
		if !strings.Contains(d, "pool_max_conns=") {
			t.Errorf("a connection string opens an unstated number of connections, so its ceiling is the node's CPU count: %s", d)
		}
	}
	// The ceilings this deployment states, and which role carries which.
	for _, want := range []string{
		"taisce_data:$(TAISCE_DATA_PASSWORD)@t-taisce-db-rw:5432/taisce?sslmode=require&pool_max_conns=12",
		"taisce_control:$(TAISCE_CONTROL_PASSWORD)@t-taisce-db-rw:5432/taisce?sslmode=require&pool_max_conns=2",
		"taisce_data:$(TAISCE_DATA_PASSWORD)@t-taisce-db-rw:5432/taisce?sslmode=require&pool_max_conns=4",
		"taisce_admin:$(PGPASSWORD_ADMIN)@t-taisce-db-rw:5432/taisce?sslmode=require&pool_max_conns=3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendering lacks %q", want)
		}
	}
	// A worker resolves no credentials, so it is given no registry connection to size.
	worker := render(t, "--show-only", "templates/worker.yaml")
	if strings.Contains(worker, "TAISCE_REGISTRY_DSN") {
		t.Error("a worker must hold no registry connection: it resolves no credentials")
	}
	if !strings.Contains(worker, "pool_max_conns=4") {
		t.Error("the worker's memory pool must be stated")
	}
	// Each role's ceiling is its own value, so one of them moving does not move the others.
	raised := render(t, "--set", "worker.memoryPool=9")
	if !strings.Contains(raised, "pool_max_conns=9") || !strings.Contains(raised, "pool_max_conns=12") {
		t.Error("raising the worker's pool must leave the API's where the deployment stated it")
	}
	// An external database's DSNs are written by the operator and read from a secret. This chart
	// cannot append a ceiling to a value it never sees, so it renders none and claims none.
	external := render(t, "--set", "postgresql.mode=external", "--set", "postgresql.external.existingSecret=dbsecret")
	if dsn.MatchString(external) {
		t.Error("in external mode the chart must build no connection string of its own")
	}
	if !strings.Contains(external, "key: memoryDSN") {
		t.Error("in external mode the DSNs come from the named secret")
	}
}

// The declared database gives up on a peer that has stopped answering, so a lost node cannot hold a
// project's formation.
//
// Formation holds a session advisory lock for the whole drain of a scope, which spans a model call
// per turn. A session lock is released when its session ends and by nothing else, so "the holder has
// gone" is a question only the database answers. A process dying answers it in about three seconds,
// because the kernel on its node closes the sockets of a process that no longer exists — measured on
// a cluster. A node going away answers it only when the keepalive gives up, and left unset that is
// the operating system's default: on Linux, about two hours and eleven minutes. For all of it every
// other worker asking for that scope is refused, which is correct behaviour producing the wrong
// outcome, and on a single-project instance that is the whole deployment's formation.
//
// One idle bound is the whole fix. The interval and count stay the operating system's, and this
// closes the window for every session lock in the system rather than this one.
func TestTheDeclaredDatabaseGivesUpOnAPeerThatHasStoppedAnswering(t *testing.T) {
	out := render(t, "--show-only", "templates/postgresql.yaml")
	if !strings.Contains(out, `tcp_keepalives_idle: "60"`) {
		t.Error("the declared database must bound how long a dead peer holds a session lock")
	}
	for _, zero := range []string{`tcp_keepalives_idle: "0"`, "tcp_keepalives_idle: 0"} {
		if strings.Contains(out, zero) {
			t.Errorf("%s is the operating system's default, which is hours", zero)
		}
	}
	// A value, because a network where a healthy peer can be silent for longer needs a larger one.
	if !strings.Contains(render(t, "--set-string", `postgresql.cnpg.parameters.tcp_keepalives_idle=300`), `tcp_keepalives_idle: "300"`) {
		t.Error("the idle bound must be an operator's to raise")
	}
}

// A release that declares more connections than the server it declares beside them is refused
// before anything is applied.
//
// Each process already refuses to start when its own pool cannot fit the free slots, which catches a
// server that is full. It cannot catch a release that over-declares: pools fill lazily, so every pod
// sees a nearly empty database at install and starts cleanly — seven processes started here while
// reporting 185 to 196 slots free and actually using 11 — and the shortfall arrives later as
// connection refusals on a deployment that came up green. Nothing compared the sum against
// max_connections, so the arithmetic was a paragraph in a document rather than a rule.
func TestTheChartRefusesATopologyThatDeclaresMoreConnectionsThanTheServerHas(t *testing.T) {
	// The defaults fit: 2×(12+2) + 2×4 + 3 + 3 = 42 against 200.
	render(t)
	refuse := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("helm", append([]string{"template", "t", "taisce", "--set", "postgresql.cnpg.backup.acknowledgedNone=true"}, args...)...).CombinedOutput()
		if err == nil {
			t.Fatalf("helm template %v was accepted and should not have been:\n%s", args, out)
		}
		return string(out)
	}
	out := refuse("--set", "api.replicas=20", "--set", "worker.replicas=20")
	for _, want := range []string{"declares 366 client connections", "max_connections is 200", "at least 457"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal must name both numbers and what would satisfy it; it lacks %q:\n%s", want, out)
		}
	}
	// Raising the pools over-declares exactly as raising the replicas does.
	if out := refuse("--set", "api.memoryPool=90", "--set", "api.registryPool=20"); !strings.Contains(out, "max_connections is 200") {
		t.Errorf("a pool too large for the server must be refused:\n%s", out)
	}
	// And the operator may say the server is bigger, which is the other half of the arithmetic.
	big := render(t, "--set", "api.replicas=20", "--set", "worker.replicas=20",
		"--set-string", "postgresql.cnpg.parameters.max_connections=600")
	if !strings.Contains(big, "max_connections: \"600\"") || !strings.Contains(big, "replicas: 20") {
		t.Error("a server declared large enough for the topology must render it")
	}
	// An external database's limit is not this chart's to know, so the check does not apply and the
	// rendering is not refused for a number it cannot see.
	render(t, "--set", "postgresql.mode=external", "--set", "postgresql.external.existingSecret=dbsecret",
		"--set", "api.replicas=20", "--set", "worker.replicas=20")
}

// The README's install can be followed literally, and the small shape it points at renders.
//
// Three things in it did not survive being followed. The provider key is a Secret the operator
// creates and a Secret lives in a namespace, while the one install command created the namespace
// itself — so there was no point in the sequence at which the key could exist, and a reader who
// followed it watched the workers start and fail to reach a model. The published-chart example named
// a version two releases old, with no way to tell whether it was current, an example, or the last
// one known to work. And nothing said how to reach what was installed, on a chart that deliberately
// exposes nothing.
//
// Asserted here rather than reviewed, because each of the three is the kind of defect that returns:
// a command copied from elsewhere, a version pasted in, a surface nobody documents because whoever
// wrote it already knew how to reach it.
func TestTheReadmesInstallCanBeFollowedAndTheSmallShapeItNamesRenders(t *testing.T) {
	readme := readFile(t, "README.md")

	// The namespace is created before the key, and the key before the install.
	namespace := strings.Index(readme, "kubectl create namespace taisce")
	secret := strings.Index(readme, "create secret generic model-key")
	install := strings.Index(readme, "helm install memory deploy/helm/taisce -n taisce \\")
	if namespace < 0 || secret < 0 || install < 0 {
		t.Fatalf("the install must create the namespace, then the key, then the release; found them at %d, %d, %d", namespace, secret, install)
	}
	if !(namespace < secret && secret < install) {
		t.Error("the key is a Secret in a namespace, so the namespace must be created before it and both before the install")
	}
	// No install example creates the namespace as part of itself: that order cannot be followed.
	for _, line := range strings.Split(readme, "\n") {
		if strings.Contains(line, "helm install") && strings.Contains(line, "--create-namespace") {
			t.Errorf("an install that creates the namespace leaves nowhere for the key to exist: %s", strings.TrimSpace(line))
		}
	}
	// The published-chart example pins no version, because a version written here goes stale.
	pinned := regexp.MustCompile(`--version [0-9]`)
	if pinned.MatchString(readme) {
		t.Errorf("the published-chart example names a version, which will be wrong by the next release: %q", pinned.FindString(readme))
	}
	// And it says how to reach either surface, for a cluster with no ingress controller.
	for _, want := range []string{
		"port-forward svc/memory-taisce-api 8080:8080",
		"port-forward svc/memory-taisce-manage 8081:8081",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("the README must say how to reach what was installed; it lacks %q", want)
		}
	}

	// The small shape is a file that renders, not a paragraph. One of everything, and it states no
	// PostgreSQL parameter that the memory limit now decides.
	if !strings.Contains(readme, "-f deploy/helm/values-small.yaml") {
		t.Error("the README must point at the small values file rather than describing the shape")
	}
	small := render(t, "-f", "values-small.yaml")
	for _, want := range []string{"instances: 1", `shm: "256Mi"`, `shared_buffers: "512MB"`, `maintenance_work_mem: "128MB"`, `max_connections: "60"`} {
		if !strings.Contains(small, want) {
			t.Errorf("the small shape lacks %q", want)
		}
	}
	if strings.Contains(small, "replicas: 2") {
		t.Error("the small shape runs one of everything")
	}
	values := readFile(t, "values-small.yaml")
	for _, derived := range []string{"shared_buffers", "effective_cache_size", "maintenance_work_mem", "max_wal_size", "sharedMemory"} {
		if strings.Contains(values, derived+":") {
			t.Errorf("%s follows the memory limit, so the small shape must not have to state it — which is the whole point of the file", derived)
		}
	}
}

// Lowering the database's memory limit lowers everything that is a fraction of it.
//
// These four were written beside the 32Gi limit they were sized for. Lowering the limit on its own
// left a database configured to reserve more shared memory than its container allowed, so it did not
// start — and the README's instructions for shrinking the deployment changed the replica counts and
// nothing else, giving a reader who followed them no reason to expect it. The smallest working change
// was five values across two blocks, derived by hand from a limit the operator picked, none of it
// documented and all of it needed before the first pod started.
//
// The fractions reproduce the previous defaults exactly at 32Gi, so this changed no deployment that
// had not already been broken by shrinking one.
func TestTheDatabasesMemoryParametersFollowItsMemoryLimit(t *testing.T) {
	// At the default limit, exactly what the chart declared before the arithmetic was moved here.
	out := render(t, "--show-only", "templates/postgresql.yaml")
	for _, want := range []string{
		`shared_buffers: "8192MB"`, `effective_cache_size: "22528MB"`,
		`maintenance_work_mem: "2048MB"`, `max_wal_size: "16384MB"`, `shm: "4Gi"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("at a 32Gi limit the derivation must reproduce the chart's own defaults; it lacks %q", want)
		}
	}
	// A quarter of the limit for shared_buffers, and an eighth for the shared-memory volume, which
	// is always twice maintenance_work_mem: a parallel index build puts its work there, so a volume
	// below that bound is one the build fails in.
	for _, c := range []struct{ limit, buffers, maintenance, shm string }{
		{"2Gi", `"512MB"`, `"128MB"`, `"256Mi"`},
		{"4096Mi", `"1024MB"`, `"256MB"`, `"512Mi"`},
		{"64Gi", `"16384MB"`, `"4096MB"`, `"8Gi"`},
	} {
		out := render(t, "--set-string", "postgresql.cnpg.resources.limits.memory="+c.limit, "--show-only", "templates/postgresql.yaml")
		if !strings.Contains(out, "shared_buffers: "+c.buffers) || !strings.Contains(out, "maintenance_work_mem: "+c.maintenance) || !strings.Contains(out, "shm: "+c.shm) {
			t.Errorf("at a %s limit expected shared_buffers %s, maintenance_work_mem %s and shm %s:\n%s", c.limit, c.buffers, c.maintenance, c.shm, out)
		}
	}
	// An operator who states one of them still wins: these are the defaults underneath.
	stated := render(t, "--set-string", "postgresql.cnpg.parameters.shared_buffers=99MB",
		"--set-string", "postgresql.cnpg.sharedMemory=1Gi", "--show-only", "templates/postgresql.yaml")
	if !strings.Contains(stated, `shared_buffers: "99MB"`) || !strings.Contains(stated, `shm: "1Gi"`) {
		t.Error("a stated parameter must override the derivation")
	}
	if !strings.Contains(stated, `max_wal_size: "16384MB"`) {
		t.Error("overriding one parameter must leave the others derived")
	}
	// A unit the chart cannot read is refused, not guessed at: misreading it by a factor of 1024
	// configures a database that will not start, for a reason nothing would point at.
	refused, err := exec.Command("helm", "template", "t", "taisce", "--set", "postgresql.cnpg.backup.acknowledgedNone=true", "--set-string", "postgresql.cnpg.resources.limits.memory=32G").CombinedOutput()
	if err == nil || !strings.Contains(string(refused), "write it in Gi or Mi") {
		t.Errorf("a memory limit in an unreadable unit must be refused: %v\n%s", err, refused)
	}
}

// The shared-memory volume's size is written the way the API server stores it.
//
// A quantity has many spellings and Kubernetes keeps one: the largest binary suffix that leaves a
// whole number, so 1024Mi is stored as 1Gi. The database operator decides whether a pod needs
// replacing by comparing the pod it finds with the pod it would build from the cluster's spec. With
// the chart writing 1024Mi it built one, found the other, and replaced the replica — whose
// replacement was stored as 1Gi again. Measured on a three-instance cluster with an 8Gi limit: one
// replica replaced every fourteen seconds, 329 times in 77 minutes of operator log, for as long as
// the release existed. The primary is rolled last, so it was never reached and the service answered
// throughout; the only sign was a replica whose age was always a few seconds.
//
// It happened at every limit whose eighth is a whole number of gibibytes, which is 8Gi, 16Gi, 24Gi
// and the chart's own default of 32Gi.
//
// The check does not ask a Kubernetes library what canonical means, because the property is small
// enough to state: a binary quantity is in stored form when its number is not a multiple of 1024.
//
// What this does not cover: a quantity an operator writes anywhere else in the values file. Only the
// shared-memory volume is known to be compared by spelling, and only it is derived by this chart.
func TestTheSharedMemoryVolumeIsSizedInTheFormTheAPIServerStores(t *testing.T) {
	shm := regexp.MustCompile(`shm: "(\d+)(Mi|Gi|Ti)"`)
	stored := func(t *testing.T, out, why string) string {
		t.Helper()
		m := shm.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("%s: no shared-memory size in the rendering:\n%s", why, out)
		}
		n := 0
		for _, d := range m[1] {
			n = n*10 + int(d-'0')
		}
		if n == 0 || n%1024 == 0 {
			t.Errorf("%s: shm is %s%s, which the API server stores under a different spelling, so the operator replaces the pod without end", why, m[1], m[2])
		}
		return m[1] + m[2]
	}
	// Derived, across the limits an operator is likely to pick and two whose eighth is not a whole
	// gibibyte. The amount is asserted as well as the form: canonical and wrong is still wrong.
	for _, c := range []struct{ limit, want string }{
		{"2Gi", "256Mi"}, {"4096Mi", "512Mi"}, {"12Gi", "1536Mi"},
		{"8Gi", "1Gi"}, {"16Gi", "2Gi"}, {"24Gi", "3Gi"}, {"32Gi", "4Gi"}, {"64Gi", "8Gi"},
	} {
		out := render(t, "--set-string", "postgresql.cnpg.resources.limits.memory="+c.limit, "--show-only", "templates/postgresql.yaml")
		if got := stored(t, out, "at a "+c.limit+" limit"); got != c.want {
			t.Errorf("at a %s limit the volume is an eighth of it, %s; rendered %s", c.limit, c.want, got)
		}
	}
	// Stated by the operator in a spelling that is not the stored one: the same amount, respelled.
	for _, c := range []struct{ stated, want string }{{"1024Mi", "1Gi"}, {"3072Mi", "3Gi"}, {"1Gi", "1Gi"}, {"768Mi", "768Mi"}} {
		out := render(t, "--set-string", "postgresql.cnpg.sharedMemory="+c.stated, "--show-only", "templates/postgresql.yaml")
		if got := stored(t, out, "stated as "+c.stated); got != c.want {
			t.Errorf("sharedMemory stated as %s must render as %s; rendered %s", c.stated, c.want, got)
		}
	}
	// A stated size this chart cannot respell is refused. Passing it through unread is how the loop
	// comes back: 1G, 1000M and 1.5Gi are all valid quantities and none is stored as written.
	for _, bad := range []string{"1G", "1000M", "1.5Gi", "0Gi", "Gi", "1073741824"} {
		out, err := exec.Command("helm", "template", "t", "taisce", "--set", "postgresql.cnpg.backup.acknowledgedNone=true", "--set-string", "postgresql.cnpg.sharedMemory="+bad).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "postgresql.cnpg.sharedMemory") || !strings.Contains(string(out), "whole number of Mi or Gi") {
			t.Errorf("sharedMemory=%q must be refused with the reason: %v\n%s", bad, err, out)
		}
	}
}
