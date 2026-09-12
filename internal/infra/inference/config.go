// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package inference reaches a chat model over the OpenAI-compatible HTTP interface.
//
// One interface rather than one per vendor, because the shape is the same everywhere it matters and
// the differences are in fields this package does not send. What that buys is the ability to change
// where inference happens — a hosted provider, a gateway, a model on the same machine — without any
// of it reaching the extractor, which is the only place the correctness argument lives.
package inference

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Environment variables. Prefixed, because a process that reads a bare `API_KEY` picks up whatever
// happened to be exported in the shell that started it.
const (
	EnvEndpoint  = "TAISCE_INFERENCE_ENDPOINT"
	EnvAPIKey    = "TAISCE_INFERENCE_API_KEY"
	EnvModel     = "TAISCE_INFERENCE_EXTRACTOR_MODEL"
	EnvAllowlist = "TAISCE_INFERENCE_ALLOWLIST"
	// EnvEmbeddingModel names the model that turns text into a vector. Separate from the extractor
	// because they are different jobs with different costs: extraction is a generation and runs once
	// per message, embedding is a forward pass and runs once per entity and once per question.
	// Operators pick different models for them, and a single variable would force one choice.
	EnvEmbeddingModel = "TAISCE_INFERENCE_EMBEDDING_MODEL"
	// An explicit revision opts the API into passage search against matching operator generations.
	EnvEmbeddingRevision = "TAISCE_INFERENCE_EMBEDDING_REVISION"
	// EnvEmbeddingEndpoint and EnvEmbeddingAPIKey send embedding somewhere other than generation.
	//
	// # Why the two roles can be different providers
	//
	// They are different jobs with different shapes. Generation is slow, is the expensive part, and is
	// where a better model shows — and it runs on a background pass, so nobody is waiting on it.
	// Embedding is a forward pass over short text, is cheap, and is the one an operator is most likely
	// to want on their own hardware because the vectors never leave anyway.
	//
	// So an operator may reasonably send extraction to a hosted provider and keep embedding local, and
	// a single endpoint would force one choice for both.
	//
	// Both are OPTIONAL and fall back to the generation endpoint. One provider is the ordinary case
	// and it should need one variable.
	EnvEmbeddingEndpoint = "TAISCE_INFERENCE_EMBEDDING_ENDPOINT"
	EnvEmbeddingAPIKey   = "TAISCE_INFERENCE_EMBEDDING_API_KEY"
)

// Config is where inference happens and what is permitted to see it.
//
// # Why the allowlist is one list across both roles
//
// It is a list of hosts permitted to receive somebody's words, and both roles send them: extraction
// sends a message, embedding sends a report written from several messages. A list per role would be
// two places to forget a host and two ways to widen the boundary by accident, and it would let a
// deployment permit a host for one role while believing it had not permitted it at all.
//
// One list, every endpoint checked against it, empty permits nothing.
type Config struct {
	// Endpoint is the base URL generation goes to. `/chat/completions` is appended, so this is the
	// same value a vendor's own documentation calls the base URL.
	Endpoint string
	APIKey   string
	Model    string
	// EmbeddingModel is empty when none is configured, and that is not an error here. A deployment
	// that extracts and never embeds is a working deployment; what must not happen is a caller
	// silently getting a vector of zeros, so the embedder refuses at the point of use instead.
	EmbeddingModel string
	// EmbeddingEndpoint and EmbeddingAPIKey are where embedding goes when it is somewhere else. Both
	// fall back to the generation endpoint's, so one provider needs one set of variables.
	EmbeddingEndpoint string
	EmbeddingAPIKey   string
	// Timeout bounds one generation request at the HTTP client, beneath whatever deadline the
	// caller's context carries. Zero selects the two-minute default. The formation worker sets it
	// to its turn budget so that the deadline that fires is the one the operator configured and
	// the one the failure reports, rather than a second, lower number hidden in the client.
	Timeout time.Duration
}

// ConfigFromEnv reads the configuration and refuses anything that would send message content
// somewhere unlisted.
//
// # Why an allowlist, and why it is fatal rather than a warning
//
// Extraction sends a customer's message text to a model. That is an egress of exactly the data this
// product exists to hold, and the endpoint is a string in the environment — one typo, one copied
// deployment file, and the content goes somewhere nobody chose.
//
// So the permitted hosts are named separately from the endpoint, and the two have to agree. A
// misconfiguration then fails at startup, loudly, before a single message has left. A warning would
// be read after the fact, which for an egress is after it has already happened.
//
// Listing a host is a deliberate act. That is the whole mechanism: it cannot be arrived at by
// default, only by somebody writing the host down.
func ConfigFromEnv() (Config, error) {
	cfg := inferenceEnvironment()
	if cfg.Endpoint == "" {
		return Config{}, fmt.Errorf("%s is not set", EnvEndpoint)
	}
	if cfg.Model == "" {
		return Config{}, fmt.Errorf("%s is not set", EnvModel)
	}
	allowlist := os.Getenv(EnvAllowlist)
	if err := permitted(cfg.Endpoint, allowlist); err != nil {
		return Config{}, err
	}
	// Both roles must be allowed when the configuration is used for generation and embedding.
	if cfg.EmbeddingEndpoint != "" && cfg.EmbeddingEndpoint != cfg.Endpoint {
		if err := permitted(cfg.EmbeddingEndpoint, allowlist); err != nil {
			return Config{}, fmt.Errorf("%s: %w", EnvEmbeddingEndpoint, err)
		}
	}
	return cfg, nil
}

// EmbeddingConfigFromEnv permits an embedding-only API or operator process. It validates the exact
// endpoint/key pair Embedder will use, without requiring an unrelated generation model or host.
func EmbeddingConfigFromEnv() (Config, error) {
	cfg := inferenceEnvironment()
	if cfg.EmbeddingModel == "" {
		return Config{}, fmt.Errorf("%s is not set", EnvEmbeddingModel)
	}
	endpoint, _ := cfg.EmbeddingsAt()
	if endpoint == "" {
		return Config{}, fmt.Errorf("an embedding endpoint is not set")
	}
	if err := permitted(endpoint, os.Getenv(EnvAllowlist)); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func inferenceEnvironment() Config {
	return Config{
		Endpoint: strings.TrimSuffix(strings.TrimSpace(os.Getenv(EnvEndpoint)), "/"),
		APIKey:   strings.TrimSpace(os.Getenv(EnvAPIKey)),
		Model:    strings.TrimSpace(os.Getenv(EnvModel)),

		EmbeddingModel:    strings.TrimSpace(os.Getenv(EnvEmbeddingModel)),
		EmbeddingEndpoint: strings.TrimSuffix(strings.TrimSpace(os.Getenv(EnvEmbeddingEndpoint)), "/"),
		EmbeddingAPIKey:   strings.TrimSpace(os.Getenv(EnvEmbeddingAPIKey)),
	}
}

// EmbeddingsAt is where embedding goes, and with what key.
//
// Falling back rather than requiring both is what keeps one provider a one-variable deployment. The
// key falls back with the endpoint and not independently: a key belongs to a provider, and carrying
// the generation provider's key to a different host would send a credential somewhere it does not
// belong — which is worse than the request failing.
func (c Config) EmbeddingsAt() (endpoint, key string) {
	if c.EmbeddingEndpoint == "" || c.EmbeddingEndpoint == c.Endpoint {
		return c.Endpoint, c.APIKey
	}
	return c.EmbeddingEndpoint, c.EmbeddingAPIKey
}

// modelClient is the client every model call goes through: bounded in time, and never redirected.
//
// The allowlist names the hosts message content may reach. A client that follows redirects turns it
// into a list of hosts allowed to choose where the content goes next — a 307 or 308 re-sends the
// request body, unchanged, to whatever the Location names, and nothing checks that host. So a
// redirect is refused rather than re-checked on each hop: a model endpoint has no reason to redirect,
// and one rule checked once is the one that stays right. The redirect comes back as the answer, and
// every caller already treats anything but 200 as a failure.
//
// Private addresses are not refused here as they are for notifications. An operator's model is
// legitimately inside their own network — the shipped proxy is — and the endpoint is the operator's
// own configuration, not a tenant's input.
func modelClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// permitted reports whether the endpoint's host is one somebody listed.
//
// Matched on host and port, not on a prefix of the URL. A prefix match would accept
// `https://openrouter.ai.attacker.example/v1` for a list containing `https://openrouter.ai`, which
// is the failure an allowlist is for.
func permitted(endpoint, allowlist string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("%s is not a URL: %w", EnvEndpoint, err)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s (%q) has no host", EnvEndpoint, endpoint)
	}
	// http is permitted only to the local machine. A plaintext hop to anywhere else puts the
	// message content on the wire in the clear, which is a worse failure than the one the allowlist
	// is guarding.
	if parsed.Scheme != "https" && !isLocalMachine(parsed.Hostname()) {
		return fmt.Errorf("%s (%q) is not https, and only a loopback address may be plaintext",
			EnvEndpoint, endpoint)
	}

	var listed []string
	for _, entry := range strings.Split(allowlist, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		listed = append(listed, entry)
		if entry == parsed.Host {
			return nil
		}
	}
	if len(listed) == 0 {
		return fmt.Errorf("%s is empty, so no endpoint may receive message content; list %q to permit it",
			EnvAllowlist, parsed.Host)
	}
	return fmt.Errorf("%s is %q, which %s does not list (%s)",
		EnvEndpoint, parsed.Host, EnvAllowlist, strings.Join(listed, ", "))
}

// isLocalMachine reports whether message content sent to this host stays on the machine.
//
// Loopback obviously does. `host.docker.internal` does too, and it has to be here or the whole
// zero-signup path is impossible: a container cannot reach a model running on the operator's own
// laptop by any other name, and requiring TLS for a hop across a local bridge would mean the
// quickstart begins with generating a certificate.
//
// It is a narrow exception and it is worth stating what it assumes. That name is set by the
// container runtime to the host gateway, so anyone able to point it somewhere else already controls
// the deployment's own configuration. Every other host still needs https, which is the case the rule
// exists for: content leaving the machine in the clear.
func isLocalMachine(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	case "host.docker.internal":
		return true
	default:
		return false
	}
}
