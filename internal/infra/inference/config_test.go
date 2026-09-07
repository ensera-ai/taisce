package inference_test

import (
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/inference"
)

// ── The egress guard ──────────────────────────────────────────────────────────────────────────
//
// Extraction sends a customer's message text to a model. The endpoint is a string in the
// environment, so a typo or a copied deployment file is all it takes for that content to go
// somewhere nobody chose — and unlike most misconfigurations, this one cannot be undone once it has
// happened.
//
// So the permitted hosts are named separately from the endpoint and the two must agree, and the
// disagreement is fatal at startup rather than logged after the fact.
func TestNoMessageContentLeavesForAnUnlistedHost(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoint  string
		allowlist string
		model     string
		permitted bool
		because   string
	}{
		{
			name:      "a listed host is permitted",
			endpoint:  "https://inference.example/v1",
			allowlist: "inference.example",
			model:     "a-model",
			permitted: true,
		},
		{
			name:      "an unlisted host is refused",
			endpoint:  "https://somewhere-else.example/v1",
			allowlist: "inference.example",
			model:     "a-model",
			because:   "does not list",
		},
		{
			name: "an empty allowlist permits nothing",
			// The default has to be closed. An allowlist that permits everything when unset is one
			// forgotten variable away from being no allowlist at all, and nothing would report it.
			endpoint:  "https://inference.example/v1",
			allowlist: "",
			model:     "a-model",
			because:   "is empty",
		},
		{
			name: "a host that merely starts with a listed one is refused",
			// The failure an allowlist exists for. A prefix match on the URL would accept this,
			// and the content would go to a host somebody else controls.
			endpoint:  "https://inference.example.attacker.test/v1",
			allowlist: "inference.example",
			model:     "a-model",
			because:   "does not list",
		},
		{
			name:      "a different port on a listed host is refused",
			endpoint:  "https://inference.example:8443/v1",
			allowlist: "inference.example",
			model:     "a-model",
			because:   "does not list",
		},
		{
			name: "plaintext to a remote host is refused even when listed",
			// Listing a host says where the content may go, not that it may go there in the clear.
			endpoint:  "http://inference.example/v1",
			allowlist: "inference.example",
			model:     "a-model",
			because:   "not https",
		},
		{
			name: "plaintext to the local machine is permitted",
			// A model on the same machine is the one case where there is no wire to protect, and
			// refusing it would push people towards a remote endpoint to get started.
			endpoint:  "http://localhost:11434/v1",
			allowlist: "localhost:11434",
			model:     "a-model",
			permitted: true,
		},
		{
			name:      "no model is refused",
			endpoint:  "https://inference.example/v1",
			allowlist: "inference.example",
			because:   "TAISCE_INFERENCE_EXTRACTOR_MODEL",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(inference.EnvEndpoint, tc.endpoint)
			t.Setenv(inference.EnvAllowlist, tc.allowlist)
			t.Setenv(inference.EnvModel, tc.model)
			t.Setenv(inference.EnvAPIKey, "not-a-real-key")

			cfg, err := inference.ConfigFromEnv()
			if tc.permitted {
				if err != nil {
					t.Fatalf("expected %q to be permitted: %v", tc.endpoint, err)
				}
				if cfg.Endpoint == "" {
					t.Fatal("permitted, but no endpoint came back")
				}
				return
			}
			if err == nil {
				t.Fatalf("%q was permitted with allowlist %q", tc.endpoint, tc.allowlist)
			}
			if !strings.Contains(err.Error(), tc.because) {
				t.Fatalf("refused for the wrong reason: %v", err)
			}
		})
	}
}

// A trailing slash on the endpoint must not produce a doubled one in the path. Cosmetic against most
// providers and a 404 against some, which is a confusing way to discover a configuration typo.
func TestATrailingSlashOnTheEndpointIsTrimmed(t *testing.T) {
	t.Setenv(inference.EnvEndpoint, "https://inference.example/v1/")
	t.Setenv(inference.EnvAllowlist, "inference.example")
	t.Setenv(inference.EnvModel, "a-model")

	cfg, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.Endpoint != "https://inference.example/v1" {
		t.Fatalf("endpoint is %q", cfg.Endpoint)
	}
}

// Plaintext is permitted to the machine the process is on, and to nothing else.
//
// The exception exists so that a container can reach a model on the operator's own laptop without the
// quickstart beginning with a certificate. It is narrow on purpose: everything that leaves the
// machine still needs TLS, because the failure being prevented is a customer's conversations crossing
// a network in the clear.
func TestPlaintextIsPermittedOnlyToThisMachine(t *testing.T) {
	for _, endpoint := range []string{
		"http://localhost:11434/v1",
		"http://127.0.0.1:11434/v1",
		"http://host.docker.internal:11434/v1",
	} {
		t.Setenv(inference.EnvEndpoint, endpoint)
		t.Setenv(inference.EnvModel, "extractor")
		host := strings.TrimPrefix(strings.Split(endpoint, "/v1")[0], "http://")
		t.Setenv(inference.EnvAllowlist, host)
		if _, err := inference.ConfigFromEnv(); err != nil {
			t.Fatalf("%s was refused: %v", endpoint, err)
		}
	}

	// Anything else in the clear is refused even when it is allowlisted, because the allowlist says
	// WHERE content may go and this rule says HOW it may travel.
	for _, endpoint := range []string{
		"http://api.example.com/v1",
		"http://192.168.1.50:11434/v1",
	} {
		t.Setenv(inference.EnvEndpoint, endpoint)
		t.Setenv(inference.EnvModel, "extractor")
		t.Setenv(inference.EnvAllowlist, strings.TrimPrefix(strings.Split(endpoint, "/v1")[0], "http://"))
		if _, err := inference.ConfigFromEnv(); err == nil {
			t.Fatalf("%s was accepted in the clear", endpoint)
		}
	}
}

// The allowlist is matched on host and port, so an entry carrying a scheme matches nothing.
//
// Worth its own test because it is the mistake an operator makes first: the endpoint is a URL, so the
// allowlist looks like it should be one too, and the failure is a silent refusal to form memory
// rather than an obvious error.
func TestTheAllowlistIsHostAndPortRatherThanAURL(t *testing.T) {
	t.Setenv(inference.EnvEndpoint, "http://localhost:11434/v1")
	t.Setenv(inference.EnvModel, "extractor")

	t.Setenv(inference.EnvAllowlist, "http://localhost:11434")
	if _, err := inference.ConfigFromEnv(); err == nil {
		t.Fatal("an allowlist entry with a scheme was accepted, which makes the match a prefix")
	} else if !strings.Contains(err.Error(), "localhost:11434") {
		t.Fatalf("the error does not show what the entry should have been: %v", err)
	}

	t.Setenv(inference.EnvAllowlist, "localhost:11434")
	if _, err := inference.ConfigFromEnv(); err != nil {
		t.Fatalf("host:port was refused: %v", err)
	}
}
