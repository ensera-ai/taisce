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
)

// Config is where inference happens and what is permitted to see it.
type Config struct {
	// Endpoint is the base URL. `/chat/completions` is appended, so this is the same value a
	// vendor's own documentation calls the base URL.
	Endpoint string
	APIKey   string
	Model    string
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
	if parsed.Scheme != "https" && !isLoopback(parsed.Hostname()) {
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

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
