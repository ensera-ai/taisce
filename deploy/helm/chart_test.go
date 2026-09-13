// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// The chart's defaults are the deployment's claims: highly available, nothing exposed
// that was not asked for, no credential that works because it was left unset. Rendered with the
// helm on the path and read back; no skip when helm is absent, because a build without the chart
// checked is a chart nobody has watched render.
package helm_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func render(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("helm", append([]string{"template", "t", "taisce"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestTheChartLintsAndItsDefaultsAreHighlyAvailableAndClosed(t *testing.T) {
	if out, err := exec.Command("helm", "lint", "taisce").CombinedOutput(); err != nil {
		t.Fatalf("helm lint: %v\n%s", err, out)
	}
	out := render(t)
	for _, want := range []string{
		"kind: Cluster", "instances: 3", "enableSuperuserAccess: false", "ALTER ROLE taisce_admin CREATEROLE", "CREATE EXTENSION IF NOT EXISTS vector", "taisce_admin:$(PGPASSWORD_ADMIN)",
		"name: t-taisce-api", "replicas: 2", "kind: PodDisruptionBudget", "minAvailable: 1",
		"name: t-taisce-worker", "TAISCE_ROLE\n              value: worker",
		"name: t-taisce-manage", "TAISCE_ROLE\n              value: manage", "value: \"off\"",
		"name: t-taisce-bootstrap-1", "runAsNonRoot: true", "readOnlyRootFilesystem: true",
		"sslmode=require", `["/taisce", "probe"]`, `["/taisce", "probe", "--worker"]`, `["/taisce", "probe", "--manage"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the default rendering lacks %q", want)
		}
	}
	for _, never := range []string{"kind: Ingress", "type: LoadBalancer", "type: NodePort", "change-me", "hostNetwork"} {
		if strings.Contains(out, never) {
			t.Errorf("the default rendering contains %q, which exposes or defaults something it must not", never)
		}
	}
	// The role passwords are generated, never a fixed value, and kept across upgrades.
	if !strings.Contains(out, "dataPassword: ") || strings.Contains(out, "dataPassword: \"\"") || !strings.Contains(out, "helm.sh/resource-policy: keep") {
		t.Error("the credentials secret must carry generated passwords and be kept")
	}
	if strings.Count(out, "replicas: 2") != 2 {
		t.Errorf("expected two replicas for the API and for the workers, got %d occurrences", strings.Count(out, "replicas: 2"))
	}
}

func TestTheChartShrinksAndPointsElsewhereOnlyWhenAsked(t *testing.T) {
	// A single-node evaluation shape is one deliberate line of values, and the rendering says what it is.
	small := render(t, "--set", "postgresql.cnpg.instances=1", "--set", "api.replicas=1", "--set", "worker.replicas=1")
	if !strings.Contains(small, "instances: 1") || strings.Contains(small, "replicas: 2") {
		t.Error("the shrunk rendering did not shrink")
	}
	// An external database: no Cluster, the DSNs come from the named secret, and no password is generated.
	external := render(t, "--set", "postgresql.mode=external", "--set", "postgresql.external.existingSecret=dbsecret")
	if strings.Contains(external, "kind: Cluster") || !strings.Contains(external, "key: memoryDSN") || !strings.Contains(external, "key: adminDSN") || strings.Contains(external, "dataPassword: ") {
		t.Error("the external rendering must reach the named secret and declare no cluster")
	}
	// The portal's ingress cannot be enabled while the portal is off: that would expose the surface's door.
	if out, err := exec.Command("helm", "template", "t", "taisce", "--set", "ingress.portal.enabled=true", "--set", "ingress.portal.host=ops.example").CombinedOutput(); err == nil || !strings.Contains(string(out), "manage.portal") {
		t.Errorf("the portal ingress must be refused while the portal is off, got %v\n%s", err, out)
	}
	exposed := render(t, "--set", "manage.portal=on", "--set", "ingress.portal.enabled=true", "--set", "ingress.portal.host=ops.example",
		"--set", "ingress.api.enabled=true", "--set", "ingress.api.host=memory.example")
	if strings.Count(exposed, "kind: Ingress") != 2 || !strings.Contains(exposed, "path: /portal") || !strings.Contains(exposed, `value: "on"`) {
		t.Error("enabling both ingresses renders both, the portal at its prefix")
	}
	if out, err := exec.Command("helm", "template", "t", "taisce", "--set", "ingress.api.enabled=true").CombinedOutput(); err == nil || !strings.Contains(string(out), "host is required") {
		t.Errorf("an ingress without a host is refused, got %v\n%s", err, out)
	}
}

// TestTheChartAsksForAnImageTheReleasePushed holds the chart and the image to one convention.
//
// The chart names its image by repository and a tag defaulted from its appVersion; the release pushes
// the image under the tags its workflow lists. v0.3.0 and v0.3.1 published a chart asking for
// `taisce:0.3.1` beside an image tagged only `v0.3.1`, and every install of that chart would have
// failed to pull (#21). Lint cannot see that, and neither can a rendering on its own, because each
// half is valid; only the pair is wrong.
//
// So the chart is packaged the way the release packages it and run through the same check the
// release runs before it pushes: refused against the tag list v0.3.1 actually pushed, accepted
// against the list the workflow now pushes. The workflow is read too, because a check that the
// workflow stopped calling would pass here and protect nothing.
func TestTheChartAsksForAnImageTheReleasePushed(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("helm", "package", "taisce", "--version", "9.8.7", "--app-version", "9.8.7", "-d", dir).CombinedOutput(); err != nil {
		t.Fatalf("helm package: %v\n%s", err, out)
	}
	chart := filepath.Join(dir, "taisce-9.8.7.tgz")
	check := func(pushed string) (string, error) {
		out, err := exec.Command("../../scripts/chart-image-check.sh", chart, "ghcr.io/ensera-ai/taisce", pushed).CombinedOutput()
		return string(out), err
	}
	const repo = "ghcr.io/ensera-ai/taisce"

	// What v0.3.1 pushed: the tag with its v, the commit, latest.
	old := repo + ":v9.8.7," + repo + ":0123abc," + repo + ":latest"
	if out, err := check(old); err == nil || !strings.Contains(out, repo+":9.8.7, which this release did not push") {
		t.Fatalf("a chart asking for a tag the release did not push was accepted: %v\n%s", err, out)
	}
	// What the workflow pushes now: the same, plus the tag without the v.
	current := repo + ":v9.8.7," + repo + ":9.8.7," + repo + ":0123abc," + repo + ":latest"
	if out, err := check(current); err != nil {
		t.Fatalf("a chart asking for a pushed tag was refused: %v\n%s", err, out)
	}

	workflow, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"${{ env.IMAGE }}:${{ steps.version.outputs.semver }}",
		"${{ env.SUBSTRATE }}:${{ steps.version.outputs.semver }}",
		`--app-version "$SEMVER"`,
		`scripts/chart-image-check.sh "dist/taisce-${SEMVER}.tgz" "$IMAGE" "$PUSHED"`,
	} {
		if !strings.Contains(string(workflow), want) {
			t.Errorf("the release workflow no longer contains %q", want)
		}
	}
	if strings.Index(string(workflow), "scripts/chart-image-check.sh") > strings.Index(string(workflow), `helm push "dist/taisce-`) {
		t.Error("the chart image check must run before the chart is pushed, not after")
	}
}
