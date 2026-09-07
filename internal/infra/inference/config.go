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
	"net/url"
	"os"
	"strings"
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
)

// Config is where inference happens and what is permitted to see it.
type Config struct {
	// Endpoint is the base URL. `/chat/completions` or `/embeddings` is appended, so this is the same
	// value a vendor's own documentation calls the base URL.
	Endpoint string
	APIKey   string
	Model    string
	// EmbeddingModel is empty when none is configured, and that is not an error here. A deployment
	// that extracts and never embeds is a working deployment; what must not happen is a caller
	// silently getting a vector of zeros, so the embedder refuses at the point of use instead.
	EmbeddingModel string
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
	cfg := Config{
		Endpoint: strings.TrimSuffix(strings.TrimSpace(os.Getenv(EnvEndpoint)), "/"),
		APIKey:   strings.TrimSpace(os.Getenv(EnvAPIKey)),
		Model:    strings.TrimSpace(os.Getenv(EnvModel)),

		EmbeddingModel: strings.TrimSpace(os.Getenv(EnvEmbeddingModel)),
	}
	if cfg.Endpoint == "" {
		return Config{}, fmt.Errorf("%s is not set", EnvEndpoint)
	}
	if cfg.Model == "" {
		return Config{}, fmt.Errorf("%s is not set", EnvModel)
	}
	if err := permitted(cfg.Endpoint, os.Getenv(EnvAllowlist)); err != nil {
		return Config{}, err
	}
	return cfg, nil
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
