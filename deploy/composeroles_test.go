package deploy_test

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// Compose, parsed as the file it is — anchors and merges resolved — gives the registry login only to
// the role that authenticates, passes the embedding revision the API needs for the semantic surfaces,
// and keeps the worker's health listener off the management surface's port.
func TestComposeGivesTheRegistryLoginOnlyToTheAPIAndKeepsPortsApart(t *testing.T) {
	raw, err := os.ReadFile("../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Services map[string]struct {
			Environment map[string]any `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	api, worker := file.Services["api"].Environment, file.Services["worker"].Environment
	if _, held := worker["TAISCE_REGISTRY_DSN"]; held {
		t.Fatal("the worker is given the registry login, which reads credentials it never resolves")
	}
	if _, held := api["TAISCE_REGISTRY_DSN"]; !held {
		t.Fatal("the API has no registry login, so it can authenticate nobody")
	}
	if _, passed := api["TAISCE_INFERENCE_EMBEDDING_REVISION"]; !passed {
		t.Fatal("the API is not given the embedding revision, so its semantic surfaces cannot be switched on")
	}
	if worker["TAISCE_HEALTH_ADDR"] == "127.0.0.1:8081" {
		t.Fatal("the worker's health listener is on the management surface's port")
	}
}
