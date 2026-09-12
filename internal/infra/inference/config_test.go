package inference_test

import (
	"strings"
	"testing"

	"github.com/ensera-ai/taisce/internal/infra/inference"
)

// hermetic clears every inference variable, so a test's result depends on what the test sets and on
// nothing else.
//
// Not hypothetical. These tests passed under `make test` and failed under `make test-inference`,
// because a profile had exported an embedding endpoint that no test's allowlist named — so a config
// the test believed it had built entirely was half inherited from the shell. A test that only sets
// what it uses is a test whose result depends on who ran it.
//
// The live measurements behind the `inference` tag deliberately read the environment, which is why
// this is a helper the hermetic tests call rather than a clear applied to the whole package.
func hermetic(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		inference.EnvEndpoint, inference.EnvAPIKey, inference.EnvModel, inference.EnvAllowlist,
		inference.EnvEmbeddingEndpoint, inference.EnvEmbeddingAPIKey, inference.EnvEmbeddingModel,
	} {
		t.Setenv(name, "")
	}
}

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
	hermetic(t)
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
	hermetic(t)
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
	hermetic(t)
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
	hermetic(t)
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

// ── Two providers, one allowlist ──────────────────────────────────────────────────────────────
//
// Generation is slow, is the expensive part, and runs on a background pass where nobody is waiting.
// Embedding is a cheap forward pass an operator may want on their own hardware. So an operator may
// send extraction to a hosted provider and keep embedding local — and every host that receives
// somebody's words still has to be listed.

func TestEmbeddingCanGoSomewhereElseAndStillHasToBeListed(t *testing.T) {
	hermetic(t)
	t.Setenv(inference.EnvEndpoint, "https://openrouter.ai/api/v1")
	t.Setenv(inference.EnvModel, "qwen/qwen3.8-flash")
	t.Setenv(inference.EnvAPIKey, "sk-generation")
	t.Setenv(inference.EnvEmbeddingEndpoint, "http://localhost:11434/v1")
	t.Setenv(inference.EnvEmbeddingModel, "qwen3-embedding:4b-q8_0")

	// Only the generation host listed: the second host receives report text and nobody permitted it.
	t.Setenv(inference.EnvAllowlist, "openrouter.ai")
	if _, err := inference.ConfigFromEnv(); err == nil {
		t.Fatal("a second endpoint inherited the first one's permission")
	}

	t.Setenv(inference.EnvAllowlist, "openrouter.ai,localhost:11434")
	cfg, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("both hosts listed and it was refused: %v", err)
	}

	endpoint, key := cfg.EmbeddingsAt()
	if endpoint != "http://localhost:11434/v1" {
		t.Fatalf("embedding goes to %q", endpoint)
	}
	// The key travels with the endpoint. Carrying the generation provider's key to a different host
	// would send a credential somewhere it does not belong, which is worse than the request failing.
	if key != "" {
		t.Fatalf("the generation provider's key was sent to another host: %q", key)
	}

	t.Setenv(inference.EnvEmbeddingAPIKey, "sk-embedding")
	cfg, err = inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if _, key := cfg.EmbeddingsAt(); key != "sk-embedding" {
		t.Fatalf("the embedding provider's own key was not used: %q", key)
	}
}

// One provider is the ordinary case and needs one set of variables.
func TestOneProviderNeedsOneEndpoint(t *testing.T) {
	hermetic(t)
	t.Setenv(inference.EnvEndpoint, "http://localhost:11434/v1")
	t.Setenv(inference.EnvModel, "qwen3.6:35b-a3b-mxfp8")
	t.Setenv(inference.EnvAPIKey, "sk-local")
	t.Setenv(inference.EnvEmbeddingModel, "qwen3-embedding:4b-q8_0")
	t.Setenv(inference.EnvEmbeddingEndpoint, "")
	t.Setenv(inference.EnvEmbeddingAPIKey, "")
	t.Setenv(inference.EnvAllowlist, "localhost:11434")

	cfg, err := inference.ConfigFromEnv()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	endpoint, key := cfg.EmbeddingsAt()
	if endpoint != cfg.Endpoint || key != cfg.APIKey {
		t.Fatalf("embedding fell back to %q with key %q", endpoint, key)
	}
}

// A second endpoint on a host nobody listed is refused for the same reason the first one is: it is an
// egress of exactly the data this product exists to hold, and the list is what makes permitting it a
// deliberate act.
func TestASecondEndpointObeysEveryRuleTheFirstOneDoes(t *testing.T) {
	hermetic(t)
	t.Setenv(inference.EnvEndpoint, "https://openrouter.ai/api/v1")
	t.Setenv(inference.EnvModel, "qwen/qwen3.8-flash")
	t.Setenv(inference.EnvEmbeddingModel, "some-embedder")
	t.Setenv(inference.EnvAllowlist, "openrouter.ai,embeddings.example")

	for name, endpoint := range map[string]string{
		"plaintext to a remote host":    "http://embeddings.example/v1",
		"a host that is not listed":     "https://elsewhere.example/v1",
		"a prefix of a listed host":     "https://openrouter.ai.attacker.example/v1",
		"a different port on that host": "https://embeddings.example:8443/v1",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(inference.EnvEmbeddingEndpoint, endpoint)
			if _, err := inference.ConfigFromEnv(); err == nil {
				t.Fatalf("%s was permitted for embedding", name)
			}
		})
	}
}
