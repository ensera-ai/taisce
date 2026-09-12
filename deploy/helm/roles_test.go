package helm_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/inference"
)

// One rendered document, found by kind and component.
func document(t *testing.T, rendered, kind, component string) string {
	t.Helper()
	for _, doc := range strings.Split(rendered, "\n---") {
		if strings.Contains(doc, "kind: "+kind) && strings.Contains(doc, "app.kubernetes.io/component: "+component) {
			return doc
		}
	}
	t.Fatalf("no %s for %s in the rendered chart", kind, component)
	return ""
}

// The chart gives the registry login only to the API, in both database modes, and keeps the
// worker's health listener off the management surface's port.
func TestOnlyTheRolesThatAuthenticateHoldTheRegistryLogin(t *testing.T) {
	for _, mode := range [][]string{nil, {"--set", "postgresql.mode=external", "--set", "postgresql.external.existingSecret=db"}} {
		rendered := render(t, mode...)
		worker, api := document(t, rendered, "Deployment", "worker"), document(t, rendered, "Deployment", "api")
		if strings.Contains(worker, "TAISCE_REGISTRY_DSN") || strings.Contains(worker, "TAISCE_CONTROL_PASSWORD") {
			t.Fatalf("%v: the worker is given the registry login", mode)
		}
		if !strings.Contains(api, "TAISCE_REGISTRY_DSN") {
			t.Fatalf("%v: the API has no registry login", mode)
		}
		if !strings.Contains(worker, `"127.0.0.1:8082"`) {
			t.Fatalf("%v: the worker's health listener is not on its own port", mode)
		}
	}
}

// The README's own install example is accepted by the allowlist check it configures. It was not:
// an entry of models.example:443 does not match https://models.example/v1, because an entry names
// the host as the URL writes it.
func TestTheReadmesInstallExamplePassesTheAllowlistItConfigures(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	value := func(key string) string {
		t.Helper()
		m := regexp.MustCompile(`--set ` + regexp.QuoteMeta(key) + `=(\S+)`).FindStringSubmatch(string(raw))
		if m == nil {
			t.Fatalf("the README sets no %s", key)
		}
		return m[1]
	}
	for _, name := range []string{inference.EnvAPIKey, inference.EnvEmbeddingModel} {
		t.Setenv(name, "")
	}
	t.Setenv(inference.EnvEndpoint, value("inference.endpoint"))
	t.Setenv(inference.EnvAllowlist, value("inference.allowlist"))
	t.Setenv(inference.EnvModel, "the-readmes-model")
	if _, err := inference.ConfigFromEnv(); err != nil {
		t.Fatalf("the README's install example is refused by its own allowlist: %v", err)
	}
}
