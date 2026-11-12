// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// How either surface is published: as an Ingress or as a Gateway API route, and what each hostname
// answers. Three things here are the chart's to decide and are held as such — what is routed on the
// console's hostname, what its root answers, and which secret a gateway elsewhere may read.
package helm_test

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	theGateway   = `[{"name":"eg","namespace":"gateways","sectionName":"https"}]`
	portalOn     = "manage.portal=on"
	portalHost   = "ingress.portal.host=ops.example"
	apiHost      = "ingress.api.host=memory.example"
	portalRoute  = "ingress.portal.kind=HTTPRoute"
	apiRoute     = "ingress.api.kind=HTTPRoute"
	portalAdmits = "networkPolicy.manage.from=" + theController
	apiAdmits    = "networkPolicy.api.from=" + theController
)

// named returns the one rendered document of a kind whose name is given, or nothing.
func named(t *testing.T, out, kind, name string) string {
	t.Helper()
	for _, doc := range strings.Split(out, "\n---") {
		if strings.Contains(doc, "\nkind: "+kind+"\n") && strings.Contains(doc, "\n  name: "+name+"\n") {
			return doc
		}
	}
	return ""
}

func refused(t *testing.T, wantInMessage string, args ...string) {
	t.Helper()
	full := append([]string{"template", "t", "taisce", "--set", "postgresql.cnpg.backup.acknowledgedNone=true"}, args...)
	out, err := exec.Command("helm", full...).CombinedOutput()
	if err == nil || !strings.Contains(string(out), wantInMessage) {
		t.Errorf("expected a refusal mentioning %q for %v, got %v\n%.600s", wantInMessage, args, err, out)
	}
}

// TestTheConsolesHostnameAnswersAtItsRootAndTheMemoryHostnameDoesNot holds the address an operator
// is given to the console, and keeps the console off the root of the name agents hold a token for.
func TestTheConsolesHostnameAnswersAtItsRootAndTheMemoryHostnameDoesNot(t *testing.T) {
	both := render(t, "--set", portalOn, "--set", "ingress.portal.enabled=true", "--set", portalHost,
		"--set", "ingress.api.enabled=true", "--set", apiHost, "--set-json", apiAdmits, "--set-json", portalAdmits)

	console := named(t, both, "Ingress", "t-taisce-portal")
	// Exact, and to the management listener, which is what redirects it into the console.
	if !strings.Contains(console, "- path: /\n            pathType: Exact\n            backend:\n              service:\n                name: t-taisce-manage") {
		t.Errorf("the console's hostname does not route its bare root to the management listener, exactly:\n%s", console)
	}
	// A prefix at the root would hand every path on this hostname to the management listener — the
	// management API included, which this ingress exists to leave unpublished.
	if strings.Contains(console, "- path: /\n            pathType: Prefix") || strings.Contains(console, "path: /manage") {
		t.Errorf("the console's hostname routes more than /portal and its bare root:\n%s", console)
	}
	if !strings.Contains(console, "- path: /portal\n            pathType: Prefix") {
		t.Errorf("the console's hostname no longer routes /portal:\n%s", console)
	}

	memory := named(t, both, "Ingress", "t-taisce-api")
	if strings.Contains(memory, "t-taisce-manage") || strings.Contains(memory, "portal") {
		t.Errorf("the memory hostname routes something to the management listener:\n%s", memory)
	}

	// One hostname for both: its root is the memory API's, so the console adds no rule there. Two
	// rules for one path on one host would be resolved by the controller, by a precedence nobody
	// here chose, and the loser would be either the agents' endpoint or a redirect on it.
	shared := render(t, "--set", portalOn, "--set", "ingress.portal.enabled=true", "--set", "ingress.portal.host=Memory.Example",
		"--set", "ingress.api.enabled=true", "--set", apiHost, "--set-json", apiAdmits, "--set-json", portalAdmits)
	if c := named(t, shared, "Ingress", "t-taisce-portal"); strings.Contains(c, "pathType: Exact") || !strings.Contains(c, "- path: /portal") {
		t.Errorf("on a hostname shared with the memory API the console must route /portal and not the root:\n%s", c)
	}
	sharedRoute := render(t, "--set", portalOn, "--set", "ingress.portal.enabled=true", "--set", "ingress.portal.host=memory.example", "--set", portalRoute, "--set-json", "ingress.portal.parentRefs="+theGateway,
		"--set", "ingress.api.enabled=true", "--set", apiHost, "--set", apiRoute, "--set-json", "ingress.api.parentRefs="+theGateway, "--set-json", apiAdmits, "--set-json", portalAdmits)
	if c := named(t, sharedRoute, "HTTPRoute", "t-taisce-portal"); strings.Contains(c, "type: Exact") || !strings.Contains(c, "value: /portal}") {
		t.Errorf("on a hostname shared with the memory API the console's route must not claim the root:\n%s", c)
	}
}

// TestAGatewayClusterPublishesEitherSurface holds the route a Gateway API cluster is given.
func TestAGatewayClusterPublishesEitherSurface(t *testing.T) {
	out := render(t, "--set", portalOn,
		"--set", "ingress.portal.enabled=true", "--set", portalHost, "--set", portalRoute, "--set-json", "ingress.portal.parentRefs="+theGateway,
		"--set", "ingress.api.enabled=true", "--set", apiHost, "--set", apiRoute, "--set-json", "ingress.api.parentRefs="+theGateway,
		"--set-json", apiAdmits, "--set-json", portalAdmits)
	if strings.Contains(out, "kind: Ingress") {
		t.Error("a surface published as a route was also rendered as an Ingress")
	}

	console := named(t, out, "HTTPRoute", "t-taisce-portal")
	for _, want := range []string{
		"apiVersion: gateway.networking.k8s.io/v1\n", "name: eg", "namespace: gateways", "sectionName: https",
		`hostnames: ["ops.example"]`, "{type: PathPrefix, value: /portal}", "{type: Exact, value: /}", "{name: t-taisce-manage, port: 8081}",
	} {
		if !strings.Contains(console, want) {
			t.Errorf("the console's route lacks %q:\n%s", want, console)
		}
	}
	// The management API is on the same port as the console and is not what this publishes.
	if strings.Contains(console, "value: /manage") || strings.Contains(console, "{type: PathPrefix, value: /}") || strings.Count(console, "PathPrefix") != 1 {
		t.Errorf("the console's route publishes more than /portal and the bare root:\n%s", console)
	}

	memory := named(t, out, "HTTPRoute", "t-taisce-api")
	for _, want := range []string{`hostnames: ["memory.example"]`, "{type: PathPrefix, value: /}", "{name: t-taisce-api, port: 8080}", "name: eg"} {
		if !strings.Contains(memory, want) {
			t.Errorf("the memory route lacks %q:\n%s", want, memory)
		}
	}
	if strings.Contains(memory, "t-taisce-manage") {
		t.Errorf("the memory route reaches the management listener:\n%s", memory)
	}
	// No certificate was named, so nothing in this namespace is opened to the gateway's.
	if strings.Contains(out, "kind: ReferenceGrant") {
		t.Error("a grant was rendered though no secret was named")
	}
}

// TestTheGatewayIsGrantedOneNamedSecretAndNeverTheKind holds the grant to the certificate.
//
// A ReferenceGrant whose `to` names a kind and no object lets the gateway's namespace read every
// secret this one will ever hold — the database's role passwords among them. That is the grant a
// first example produces, and nothing tells its author. Here the name is the only way to get one.
func TestTheGatewayIsGrantedOneNamedSecretAndNeverTheKind(t *testing.T) {
	args := []string{"--set", portalOn, "--set", "ingress.portal.enabled=true", "--set", portalHost, "--set", portalRoute,
		"--set-json", "ingress.portal.parentRefs=" + theGateway, "--set-json", portalAdmits}
	grant := named(t, render(t, append(args, "--set", "ingress.portal.certificateSecret=console-tls")...), "ReferenceGrant", "t-taisce-portal-certificate")
	for _, want := range []string{
		`{group: gateway.networking.k8s.io, kind: Gateway, namespace: "gateways"}`,
		`{group: "", kind: Secret, name: "console-tls"}`,
	} {
		if !strings.Contains(grant, want) {
			t.Errorf("the grant lacks %q:\n%s", want, grant)
		}
	}
	if strings.Count(grant, "kind: Secret") != 1 || strings.Count(grant, "name: \"") != 1 || strings.Contains(grant, "t-taisce-credentials") {
		t.Errorf("the grant reaches something other than the one certificate:\n%s", grant)
	}
	for _, line := range strings.Split(grant, "\n") {
		if strings.Contains(line, "kind: Secret") && !strings.Contains(line, "name: ") {
			t.Errorf("a grant to the Secret kind with no name opens every secret in the namespace: %s", line)
		}
	}

	// Two listeners in one namespace are one grantor, not two entries; a second namespace is a second.
	two := `[{"name":"eg","namespace":"gateways","sectionName":"a"},{"name":"eg","namespace":"gateways","sectionName":"b"},{"name":"edge","namespace":"edge"}]`
	several := named(t, render(t, "--set", portalOn, "--set", "ingress.portal.enabled=true", "--set", portalHost, "--set", portalRoute,
		"--set-json", "ingress.portal.parentRefs="+two, "--set-json", portalAdmits, "--set", "ingress.portal.certificateSecret=console-tls"), "ReferenceGrant", "t-taisce-portal-certificate")
	if strings.Count(several, "kind: Gateway") != 2 || !strings.Contains(several, `namespace: "edge"`) {
		t.Errorf("the grant must name each gateway namespace once:\n%s", several)
	}

	// A gateway in the release's own namespace needs no permission to read a secret beside it.
	local := `[{"name":"eg"},{"name":"eg","namespace":"default"}]`
	if out := render(t, "--namespace", "default", "--set", portalOn, "--set", "ingress.portal.enabled=true", "--set", portalHost, "--set", portalRoute,
		"--set-json", "ingress.portal.parentRefs="+local, "--set-json", portalAdmits, "--set", "ingress.portal.certificateSecret=console-tls"); strings.Contains(out, "kind: ReferenceGrant") {
		t.Error("a grant was rendered for a gateway in the release's own namespace")
	}
}

// TestARouteThatCouldNotServeIsRefused turns three accepted-and-useless renderings into errors.
func TestARouteThatCouldNotServeIsRefused(t *testing.T) {
	portal := []string{"--set", portalOn, "--set", "ingress.portal.enabled=true", "--set", portalHost, "--set-json", portalAdmits}
	// Attached to nothing: the cluster accepts it and it serves nothing.
	refused(t, "ingress.portal.parentRefs", append(portal, "--set", portalRoute)...)
	refused(t, "ingress.api.parentRefs", "--set", "ingress.api.enabled=true", "--set", apiHost, "--set", apiRoute, "--set-json", apiAdmits)
	// A kind this chart does not render must not fall through to one it does.
	refused(t, "ingress.portal.kind", append(portal, "--set", "ingress.portal.kind=Route")...)
	refused(t, "ingress.portal.kind", append(portal, "--set", "ingress.portal.kind=httproute")...)
	// An Ingress's certificate setting on a route would be dropped without a word.
	refused(t, "ingress.portal.certificateSecret", append(portal, "--set", portalRoute, "--set-json", "ingress.portal.parentRefs="+theGateway,
		"--set-json", `ingress.portal.tls=[{"hosts":["ops.example"],"secretName":"console-tls"}]`)...)
	// The existing refusals hold for a route as they do for an Ingress.
	refused(t, "manage.portal", "--set", "ingress.portal.enabled=true", "--set", portalHost, "--set", portalRoute, "--set-json", "ingress.portal.parentRefs="+theGateway, "--set-json", portalAdmits)
	refused(t, "networkPolicy.manage.from", "--set", portalOn, "--set", "ingress.portal.enabled=true", "--set", portalHost, "--set", portalRoute, "--set-json", "ingress.portal.parentRefs="+theGateway)
	refused(t, "host is required", "--set", portalOn, "--set", "ingress.portal.enabled=true", "--set", portalRoute, "--set-json", "ingress.portal.parentRefs="+theGateway, "--set-json", portalAdmits)
}
