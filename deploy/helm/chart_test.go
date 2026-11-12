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

// theController names an ingress controller's namespace as a caller. An ingress whose controller is
// admitted nowhere is refused (TestAnIngressNobodyCouldArriveThroughIsRefused), so the tests that
// read an ingress have to say where its traffic comes from, as an operator would.
const theController = `[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"ingress"}}}]`

func render(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("helm", append([]string{"template", "t", "taisce"}, withoutABackup(args)...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// withoutABackup states that the release has no backup, unless the caller said something about
// backups itself.
//
// A release that says nothing is refused, so "the chart's defaults" no longer render at all, and
// every test here that reads a rendering needs one. This is the one setting they are given, and it
// is the answer that adds no object to what they read. The refusal itself is held by
// TestAReleaseStatesWhereItsBackupsGoOrSaysItHasNone, which does not come through here.
func withoutABackup(args []string) []string {
	for _, a := range args {
		if strings.Contains(a, "postgresql.cnpg.backup.") || a == "values-small.yaml" {
			return args
		}
	}
	return append([]string{"--set", "postgresql.cnpg.backup.acknowledgedNone=true"}, args...)
}

func TestTheChartLintsAndItsDefaultsAreHighlyAvailableAndClosed(t *testing.T) {
	if out, err := exec.Command("helm", "lint", "taisce", "--set", "postgresql.cnpg.backup.acknowledgedNone=true").CombinedOutput(); err != nil {
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
	if out, err := exec.Command("helm", "template", "t", "taisce", "--set", "postgresql.cnpg.backup.acknowledgedNone=true", "--set", "ingress.portal.enabled=true", "--set", "ingress.portal.host=ops.example").CombinedOutput(); err == nil || !strings.Contains(string(out), "manage.portal") {
		t.Errorf("the portal ingress must be refused while the portal is off, got %v\n%s", err, out)
	}
	exposed := render(t, "--set", "manage.portal=on", "--set", "ingress.portal.enabled=true", "--set", "ingress.portal.host=ops.example",
		"--set", "ingress.api.enabled=true", "--set", "ingress.api.host=memory.example",
		"--set-json", "networkPolicy.api.from="+theController, "--set-json", "networkPolicy.manage.from="+theController)
	if strings.Count(exposed, "kind: Ingress") != 2 || !strings.Contains(exposed, "path: /portal") || !strings.Contains(exposed, `value: "on"`) {
		t.Error("enabling both ingresses renders both, the portal at its prefix")
	}
	if out, err := exec.Command("helm", "template", "t", "taisce", "--set", "postgresql.cnpg.backup.acknowledgedNone=true", "--set", "ingress.api.enabled=true", "--set-json", "networkPolicy.api.from="+theController).CombinedOutput(); err == nil || !strings.Contains(string(out), "host is required") {
		t.Errorf("an ingress without a host is refused, got %v\n%s", err, out)
	}
}

// TestTheChartAsksForAnImageTheReleasePushed holds the chart and the image to one name per release.
//
// The chart names its image by repository and a tag defaulted from its appVersion; the release pushes
// the image under the tags its workflow lists. v0.3.0 and v0.3.1 published a chart whose appVersion
// had the release tag's v stripped, so it asked for `taisce:0.3.1` beside an image tagged `v0.3.1`,
// and every install of it would have failed to pull (#21). Lint cannot see that, and neither can a
// rendering on its own, because each half is valid; only the pair is wrong.
//
// So the chart is packaged the way the release packages it — plain SemVer for the chart version, the
// release tag verbatim for appVersion — and run through the same check the release runs before it
// pushes. It is accepted against the tags a release pushes, and refused against a list that lacks the
// v-prefixed tag, which is the shape of the original defect seen from the other side. The workflow is
// read too, because a check the workflow stopped calling would pass here and protect nothing.
// The portal's ingress follows the binary's rule for "on": any case, surrounding space ignored. The
// binary switches the portal on for "ON", so a chart that refused the ingress for it would disagree
// with the process it deploys (#55). Anything that is not "on" is still refused.
func TestThePortalIngressAcceptsOnTheWayTheBinaryDoes(t *testing.T) {
	for _, value := range []string{"on", "ON", "On", " on "} {
		out := render(t, "--set-string", "manage.portal="+value, "--set", "ingress.portal.enabled=true",
			"--set", "ingress.portal.host=ops.example", "--set-json", "networkPolicy.manage.from="+theController)
		if !strings.Contains(out, "path: /portal") {
			t.Errorf("manage.portal=%q switches the portal on, and its ingress was not rendered", value)
		}
	}
	for _, value := range []string{"off", "onn", "yes", ""} {
		out, err := exec.Command("helm", "template", "t", "taisce", "--set", "postgresql.cnpg.backup.acknowledgedNone=true", "--set-string", "manage.portal="+value,
			"--set", "ingress.portal.enabled=true", "--set", "ingress.portal.host=ops.example").CombinedOutput()
		if err == nil || !strings.Contains(string(out), "manage.portal") {
			t.Errorf("manage.portal=%q does not switch the portal on, and its ingress was not refused: %v\n%s", value, err, out)
		}
	}
}

func TestTheChartAsksForAnImageTheReleasePushed(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("helm", "package", "taisce", "--version", "9.8.7", "--app-version", "v9.8.7", "-d", dir).CombinedOutput(); err != nil {
		t.Fatalf("helm package: %v\n%s", err, out)
	}
	chart := filepath.Join(dir, "taisce-9.8.7.tgz")
	check := func(pushed string) (string, error) {
		out, err := exec.Command("../../scripts/chart-image-check.sh", chart, "ghcr.io/ensera-ai/taisce", pushed).CombinedOutput()
		return string(out), err
	}
	const repo = "ghcr.io/ensera-ai/taisce"

	// What a release pushes: the tag as written, the commit, latest.
	pushed := repo + ":v9.8.7," + repo + ":0123abc," + repo + ":latest"
	if out, err := check(pushed); err != nil {
		t.Fatalf("a chart asking for the tag the release pushed was refused: %v\n%s", err, out)
	}
	// A list without the v-prefixed tag: the chart must be refused, naming what it asked for.
	if out, err := check(repo + ":9.8.7," + repo + ":0123abc," + repo + ":latest"); err == nil || !strings.Contains(out, repo+":v9.8.7, which this release did not push") {
		t.Fatalf("a chart asking for a tag the release did not push was accepted: %v\n%s", err, out)
	}

	workflow, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	wf := string(workflow)
	for _, want := range []string{
		"${{ env.IMAGE }}:${{ github.ref_name }}",
		"${{ env.SUBSTRATE }}:${{ github.ref_name }}",
		`--app-version "$RELEASE_TAG"`,
		"RELEASE_TAG: ${{ github.ref_name }}",
		`scripts/chart-image-check.sh "dist/taisce-${SEMVER}.tgz" "$IMAGE" "$PUSHED"`,
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("the release workflow no longer contains %q", want)
		}
	}
	if strings.Contains(wf, `--app-version "$SEMVER"`) {
		t.Error("appVersion must be the release tag as written, not the stripped chart version")
	}
	if strings.Index(wf, "scripts/chart-image-check.sh") > strings.Index(wf, `helm push "dist/taisce-`) {
		t.Error("the chart image check must run before the chart is pushed, not after")
	}
}

// notes renders the chart's NOTES.txt, which `helm template` does not emit.
//
// A client-side dry-run install does, and it reaches no cluster: KUBECONFIG is pointed at nothing
// so that a machine which happens to have a context cannot make this pass for a reason a build
// runner would not share.
func notes(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("helm", append([]string{"install", "t", "taisce", "--dry-run=client", "-n", "taisce"}, withoutABackup(args)...)...)
	cmd.Env = append(os.Environ(), "KUBECONFIG=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm install --dry-run=client %v: %v\n%s", args, err, out)
	}
	_, after, found := strings.Cut(string(out), "\nNOTES:\n")
	if !found {
		t.Fatalf("the dry-run printed no NOTES:\n%s", out)
	}
	return after
}

// A token is shown once, to whoever minted it, and the notes say what to do about losing one.
//
// Only digests are stored, so a lost token is not recovered: another is minted, by the same command
// that minted the first, and the lost one is revoked by name. An operator who learns that at the
// moment they discover the loss has lost nothing they cannot replace — but only if the notes said
// so. This test is what a later reader re-runs to find out whether they still do.
func TestTheNotesSayATokenIsShownOnceAndHowToReplaceOne(t *testing.T) {
	out := notes(t)
	for _, want := range []string{
		"shown once",
		"only digests are stored",
		"mints nothing",
		"exec deploy/t-taisce-manage -- /taisce operator issue <name>",
		"exec deploy/t-taisce-manage -- /taisce credential issue <name> -project default",
		"Minting does not revoke what was lost",
		"/taisce credential revoke <id>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the notes do not say %q:\n%s", want, out)
		}
	}
}

// With no management surface there is no workload holding the administrative connection, so the
// recipe above would name a deployment that does not exist. The notes have to say that instead of
// printing a command that cannot work — an instruction that fails is worse than none, because it is
// read as evidence that the step was tried. It matters more now that the recipe is the only way to a
// first credential and not only to a replacement.
func TestTheNotesDoNotOfferAnExecRecipeWhenNoWorkloadHoldsTheAdminConnection(t *testing.T) {
	out := notes(t, "--set", "manage.enabled=false")
	if strings.Contains(out, "exec deploy/t-taisce-manage") {
		t.Errorf("the notes offer an exec into a management surface that is disabled:\n%s", out)
	}
	for _, want := range []string{"No credential exists yet", "it is disabled", "--set manage.enabled=true"} {
		if !strings.Contains(out, want) {
			t.Errorf("the notes do not say %q with the management surface off:\n%s", want, out)
		}
	}
}

// No container this chart runs writes a credential to its log.
//
// A cluster ships container output to a log store, where it is kept under retention and access
// rules this deployment did not choose. So the Job is told to mint nothing, and the notes send the
// operator to a command whose output reaches their terminal and no log.
func TestNoContainerInTheChartWritesACredentialToItsLog(t *testing.T) {
	job := render(t, "--show-only", "templates/bootstrap-job.yaml")
	if !strings.Contains(job, `"-mint-credentials=false"`) {
		t.Errorf("the bootstrap Job is not told to mint nothing, so it prints the first tokens to a log:\n%s", job)
	}
	out := notes(t)
	for _, gone := range []string{"logs job/", "grep token", "printed once"} {
		if strings.Contains(out, gone) {
			t.Errorf("the notes still send an operator to a log for a token (%q):\n%s", gone, out)
		}
	}
	for _, want := range []string{
		"No credential exists yet",
		"exec deploy/t-taisce-manage -- /taisce operator issue",
		"exec deploy/t-taisce-manage -- /taisce credential issue",
		"-project default",
		"to your terminal and to no log",
		"/taisce credential revoke <id>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the notes do not say %q:\n%s", want, out)
		}
	}
}
