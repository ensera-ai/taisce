// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
//
// Who may open a connection to this release's pods is a claim the rendering makes, so it is read
// back here. What a rendering cannot show is that a cluster enforces it: a NetworkPolicy is a
// request to the network plugin, and one that enforces nothing accepts the object and admits
// everybody. docs/37 records the run against a cluster that does enforce it.
package helm_test

import (
	"os/exec"
	"strings"
	"testing"
)

// policies returns the NetworkPolicy documents of a rendering, keyed by name.
func policies(t *testing.T, out string) map[string]string {
	t.Helper()
	found := map[string]string{}
	for _, doc := range strings.Split(out, "\n---") {
		if !strings.Contains(doc, "kind: NetworkPolicy") {
			continue
		}
		_, after, ok := strings.Cut(doc, "metadata:\n  name: ")
		if !ok {
			t.Fatalf("a NetworkPolicy without a name:\n%s", doc)
		}
		name, _, _ := strings.Cut(after, "\n")
		found[name] = doc
	}
	return found
}

func TestEveryPodOfAReleaseRefusesConnectionsNobodyNamed(t *testing.T) {
	got := policies(t, render(t))

	closed, ok := got["t-taisce-closed"]
	if !ok {
		t.Fatalf("the default rendering closes nothing: policies %v", keys(got))
	}
	// Selected by release, not by component: a pod added to this release later is closed before
	// anybody remembers to write a policy for it.
	if !strings.Contains(closed, "app.kubernetes.io/instance: t\n") || strings.Contains(closed, "app.kubernetes.io/component") {
		t.Errorf("the closing policy must select every pod of the release and name no component:\n%s", closed)
	}
	if strings.Contains(closed, "ingress:") || !strings.Contains(closed, "policyTypes: [Ingress]") {
		t.Errorf("the closing policy admits something, or does not say it is about ingress:\n%s", closed)
	}

	// The memory API is what applications reach: pods of this namespace, on its one port.
	api := got["t-taisce-api"]
	if !strings.Contains(api, "app.kubernetes.io/component: api") || !strings.Contains(api, "- podSelector: {}") || !strings.Contains(api, "port: 8080") {
		t.Errorf("the API must admit this namespace's pods on 8080 and nothing else:\n%s", api)
	}
	if strings.Contains(api, "namespaceSelector") || strings.Contains(api, "ipBlock") {
		t.Errorf("the default API policy reaches beyond its namespace:\n%s", api)
	}

	// The management surface holds the administrative connection. Nobody is admitted to it until
	// somebody is named; an operator reaches it through the cluster's own API, which no policy sees.
	if manage, ok := got["t-taisce-manage"]; ok {
		t.Errorf("the default rendering admits somebody to the management surface:\n%s", manage)
	}

	// The worker listens on its own loopback and the bootstrap Job listens on nothing.
	for _, name := range keys(got) {
		if strings.Contains(name, "worker") || strings.Contains(name, "bootstrap") {
			t.Errorf("policy %s admits traffic to a pod that serves none", name)
		}
	}
}

func TestTheDatabaseAdmitsItsOwnReleaseItsPeersAndItsOperator(t *testing.T) {
	db, ok := policies(t, render(t))["t-taisce-db"]
	if !ok {
		t.Fatal("the default rendering leaves the database open to every pod in the cluster")
	}
	if !strings.Contains(db, "cnpg.io/cluster: t-taisce-db") {
		t.Errorf("the database policy must select the instances of this release's cluster:\n%s", db)
	}
	rules := strings.Split(db, "    - from:")[1:]
	if len(rules) != 3 {
		t.Fatalf("three callers have a reason to reach an instance — the release, its peers, its operator — and the policy has %d rules:\n%s", len(rules), db)
	}
	release, peers, operator := rules[0], rules[1], rules[2]
	if !strings.Contains(release, "app.kubernetes.io/instance: t\n") || !strings.Contains(release, "port: 5432") || strings.Contains(release, "port: 8000") {
		t.Errorf("the release's pods speak PostgreSQL and nothing else:\n%s", release)
	}
	if !strings.Contains(peers, "cnpg.io/cluster: t-taisce-db") || !strings.Contains(peers, "port: 5432") {
		t.Errorf("an instance must be able to replicate from another:\n%s", peers)
	}
	if !strings.Contains(operator, `kubernetes.io/metadata.name: "cnpg-system"`) || !strings.Contains(operator, "port: 8000") {
		t.Errorf("the operator must reach the status port from its own namespace:\n%s", operator)
	}
	for _, rule := range rules {
		if strings.Contains(rule, "podSelector: {}") {
			t.Errorf("a database rule admits every pod of a namespace it did not name pods in:\n%s", rule)
		}
	}

	moved := policies(t, render(t, "--set", "networkPolicy.database.operatorNamespace=operators"))["t-taisce-db"]
	if !strings.Contains(moved, `kubernetes.io/metadata.name: "operators"`) || strings.Contains(moved, "cnpg-system") {
		t.Errorf("an operator installed elsewhere must be nameable:\n%s", moved)
	}

	// An external database is not this release's to fence: its pods, if it has any, are not here.
	if _, ok := policies(t, render(t, "--set", "postgresql.mode=external", "--set", "postgresql.external.existingSecret=dbsecret"))["t-taisce-db"]; ok {
		t.Error("an external database has no instances in this release, and a policy was rendered for them")
	}
}

// TestAnEmptyListOfCallersAdmitsNobody holds the one place this API reads absence as permission.
//
// A NetworkPolicy rule whose `from` is empty or missing matches every source. So a list an operator
// emptied to close a door would, rendered as written, open it to the whole cluster — and nothing
// would fail, because the object is valid. An empty list renders no rule at all, and the closing
// policy is then the only one that selects the pod.
func TestAnEmptyListOfCallersAdmitsNobody(t *testing.T) {
	got := policies(t, render(t, "--set-json", "networkPolicy.api.from=[]"))
	if api, ok := got["t-taisce-api"]; ok {
		t.Errorf("an API with nobody named still has a policy admitting somebody:\n%s", api)
	}
	if _, ok := got["t-taisce-closed"]; !ok {
		t.Error("with nobody named, the closing policy must still be there")
	}
	for name, doc := range policies(t, render(t)) {
		if strings.Contains(doc, "from: []") || strings.Contains(doc, "- from:\n      ports") || strings.Contains(doc, "ingress:\n    - ports") {
			t.Errorf("policy %s has a rule that names no caller, which admits every caller:\n%s", name, doc)
		}
	}
}

func TestCallersAreAdmittedOnlyWhereTheyAreNamed(t *testing.T) {
	gateway := `[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"gateways"}}}]`
	got := policies(t, render(t, "--set-json", "networkPolicy.manage.from="+gateway))
	manage, ok := got["t-taisce-manage"]
	if !ok || !strings.Contains(manage, "kubernetes.io/metadata.name: gateways") || !strings.Contains(manage, "port: 8081") || !strings.Contains(manage, "app.kubernetes.io/component: manage") {
		t.Errorf("a named caller must be admitted to the management surface on its port:\n%s", manage)
	}
	// Naming a caller for one surface names it for no other.
	if strings.Contains(got["t-taisce-api"], "gateways") || strings.Contains(got["t-taisce-db"], "gateways") {
		t.Error("a caller named for the management surface was admitted somewhere else")
	}

	// A management surface that is not deployed has no door to describe.
	if _, ok := policies(t, render(t, "--set", "manage.enabled=false", "--set-json", "networkPolicy.manage.from="+gateway))["t-taisce-manage"]; ok {
		t.Error("a policy was rendered for a management surface that is not deployed")
	}
}

// TestAnIngressNobodyCouldArriveThroughIsRefused turns a silent timeout into a rendering error.
//
// An ingress controller is a pod, almost always in a namespace of its own. With the default policy
// it is a stranger, so an enabled ingress would be accepted, would resolve, and would time out on
// every request — a failure whose cause is two files away from its symptom.
func TestAnIngressNobodyCouldArriveThroughIsRefused(t *testing.T) {
	base := []string{"template", "t", "taisce", "--set", "postgresql.cnpg.backup.acknowledgedNone=true"}
	controller := `[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"ingress"}}}]`

	api := append(append([]string{}, base...), "--set", "ingress.api.enabled=true", "--set", "ingress.api.host=memory.example")
	if out, err := exec.Command("helm", api...).CombinedOutput(); err == nil || !strings.Contains(string(out), "networkPolicy.api.from") {
		t.Errorf("an API ingress whose controller is admitted nowhere must be refused, got %v\n%s", err, out)
	}
	if out, err := exec.Command("helm", append(api, "--set-json", "networkPolicy.api.from="+controller)...).CombinedOutput(); err != nil {
		t.Errorf("an API ingress whose controller is named must render: %v\n%s", err, out)
	}

	portal := append(append([]string{}, base...), "--set", "manage.portal=on", "--set", "ingress.portal.enabled=true", "--set", "ingress.portal.host=ops.example")
	if out, err := exec.Command("helm", portal...).CombinedOutput(); err == nil || !strings.Contains(string(out), "networkPolicy.manage.from") {
		t.Errorf("a portal ingress whose controller is admitted nowhere must be refused, got %v\n%s", err, out)
	}
	if out, err := exec.Command("helm", append(portal, "--set-json", "networkPolicy.manage.from="+controller)...).CombinedOutput(); err != nil {
		t.Errorf("a portal ingress whose controller is named must render: %v\n%s", err, out)
	}

	// Switched off, the chart fences nothing and has nothing to refuse.
	off := append(append([]string{}, api...), "--set", "networkPolicy.enabled=false")
	out, err := exec.Command("helm", off...).CombinedOutput()
	if err != nil || strings.Contains(string(out), "kind: NetworkPolicy") {
		t.Errorf("with the policy switched off, an ingress renders and no policy does: %v\n%s", err, out)
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
