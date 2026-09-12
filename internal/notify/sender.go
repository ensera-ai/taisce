// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Payload is the whole of what a notification says.
//
// # Why it carries no memory
//
// The destination is chosen per project by whoever holds a credential, which makes this the widest
// egress in the product — wider than the model endpoint, which an operator names once. So a
// notification carries identifiers and counts, and the content is read back through the
// authenticated API. That keeps every path that moves memory on the one road that already has a
// guard on it, and it means a misdirected notification discloses that a project formed, not what.
//
// # Why the offset is the whole protocol
//
// Delivery is at-least-once and nothing here pretends otherwise. A log offset is contiguous within a
// scope, so a receiver that remembers the highest one it has handled discards a redelivery without
// coordinating with anybody. Exactly-once would be a promise kept by whoever believed it.
type Payload struct {
	Version       string    `json:"version"`
	Scope         string    `json:"scope"`
	FormedThrough int64     `json:"formed_through"`
	StoredThrough int64     `json:"stored_through"`
	ParkedTurns   int       `json:"parked_turns"`
	OccurredAt    time.Time `json:"occurred_at"`
}

// PayloadVersion is what a receiver branches on when this shape changes.
const PayloadVersion = "taisce-notification/v1"

// Delivery is one thing to send.
type Delivery struct {
	ID            string
	Scope         string
	URL           string
	Secret        []byte
	FormedThrough int64
	StoredThrough int64
	ParkedTurns   int
}

// Result is what happened, in the terms the store records.
type Result struct {
	Status int
	Err    error
	// Retryable is false for a refusal that will not become acceptance: a destination the operator
	// no longer permits, a body the receiver called malformed. Retrying those burns the attempt
	// budget on an answer that is already final.
	Retryable bool
}

// Sender posts notifications to permitted destinations.
type Sender struct {
	destinations Destinations
	http         *http.Client
	log          *slog.Logger
	resolve      func(string) ([]net.IP, error)
	now          func() time.Time
}

// NewSender builds one. The timeout is short on purpose: a notification that takes ten seconds to
// deliver is a worker not forming memory for ten seconds, and the receiver has the offset to catch
// up with anyway.
func NewSender(destinations Destinations, log *slog.Logger) *Sender {
	return &Sender{
		destinations: destinations,
		http:         guard(&http.Client{Timeout: 5 * time.Second}, destinations, log),
		log:          log,
		now:          time.Now,
	}
}

// WithHTTPClient replaces the client used to deliver.
//
// The reader is an operator whose receiver presents a certificate from their own authority, which
// the default client has no reason to trust and every reason not to trust silently. It is also what
// lets the suite deliver to a server it stood up itself, which is the only way to hold the delivery
// path to anything: a mock of the receiver would prove the mock matched the assertion.
//
// Whatever client is supplied is guarded exactly as the default one is. Replacing the client is how an
// operator with a private certificate authority, and the suite, deliver at all — so a protection
// built only into the default would vanish for precisely those callers.
func (s *Sender) WithHTTPClient(client *http.Client) *Sender {
	if client != nil {
		s.http = guard(client, s.destinations, s.log)
	}
	return s
}

// WithResolver replaces the name lookup the destination check performs before sending.
//
// It exists so a test can make the answer at check time differ from the answer at connect time,
// which is the whole of a rebinding attack and cannot otherwise be staged on one machine. The
// connect-time check does not use it: that one reads the address actually dialled.
func (s *Sender) WithResolver(resolve func(string) ([]net.IP, error)) *Sender {
	s.resolve = resolve
	return s
}

// Enabled reports whether any destination could be sent to.
func (s *Sender) Enabled() bool { return s.destinations.Enabled() }

// Allow is the same check the sender makes, exposed so a registration can refuse a destination when
// somebody types it rather than hours later in a delivery nobody is watching.
func (s *Sender) Allow(url string) error { return s.destinations.Allow(url, s.resolve) }

// Send delivers one notification, or says why it did not.
func (s *Sender) Send(ctx context.Context, d Delivery) Result {
	// Checked here and not only at registration. A destination is a name, and what a name resolves
	// to is somebody else's to change after we agreed to it.
	if err := s.destinations.Allow(d.URL, s.resolve); err != nil {
		return Result{Err: err, Retryable: false}
	}
	body, err := json.Marshal(Payload{
		Version:       PayloadVersion,
		Scope:         d.Scope,
		FormedThrough: d.FormedThrough,
		StoredThrough: d.StoredThrough,
		ParkedTurns:   d.ParkedTurns,
		OccurredAt:    s.now().UTC().Truncate(time.Second),
	})
	if err != nil {
		return Result{Err: err, Retryable: false}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(body))
	if err != nil {
		return Result{Err: err, Retryable: false}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "taisce")
	request.Header.Set(SignatureHeader, Sign(d.Secret, s.now(), body))
	// A delivery id, so a receiver can tell a redelivery from a second notification without parsing
	// the body it may not have decided to trust yet.
	request.Header.Set("Taisce-Delivery", d.ID)

	response, err := s.http.Do(request)
	if err != nil {
		// A network failure is the case retrying exists for. A connection refused on its address is
		// not one: that is policy, and asking again gets the same answer.
		return Result{Err: err, Retryable: !errors.Is(err, errNotRoutable)}
	}
	defer response.Body.Close()
	// Read and discard a bounded amount so the connection can be reused, and so a receiver that
	// answers with a megabyte of prose cannot make that our problem.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))

	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return Result{Status: response.StatusCode}
	case response.StatusCode >= 300 && response.StatusCode < 400:
		// Not followed, and not retried. A permitted host that answers with a redirect is asking us
		// to send a signed body somewhere the operator never listed; following it would make the
		// allowlist a list of hosts allowed to choose the real destination.
		return Result{Status: response.StatusCode, Err: fmt.Errorf("%w: %d", errRedirectRefused, response.StatusCode), Retryable: false}
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		// The receiver is overwhelmed or broken, which is temporary by definition.
		return Result{Status: response.StatusCode, Err: fmt.Errorf("the destination answered %d", response.StatusCode), Retryable: true}
	default:
		// 4xx is the receiver saying the request is wrong. Sending it again unchanged is asking the
		// same question and expecting a different answer.
		return Result{Status: response.StatusCode, Err: fmt.Errorf("the destination refused with %d", response.StatusCode), Retryable: false}
	}
}

var (
	// errRedirectRefused is a destination that answered with a redirect.
	errRedirectRefused = errors.New("the destination answered with a redirect, which is not followed")
	// errNotRoutable is a connection refused on the address actually dialled.
	errNotRoutable = errors.New("the destination's address is inside this deployment's own network")
)

// guard returns a client that will not be steered anywhere the operator did not permit.
//
// # Redirects are refused, not re-checked per hop
//
// Re-running the allowlist on every hop is the other design, and it is worse: it keeps a way for a
// permitted receiver to choose a second destination, and makes the rule depend on getting a check
// right at every hop instead of once. A webhook has no reason to redirect.
//
// # The address is checked where it is dialled
//
// The destination check before sending resolves the name, and the client resolves it again to
// connect. A name whose answer changes between the two — a rebinding answer — passes the first and
// connects to the second. So the same `routable` rule runs again inside the dialer, on the address
// the connection is actually being made to, under the same permission the operator gave for private
// addresses. There is nothing between that check and the socket for a DNS answer to change.
//
// # No proxy
//
// Through a proxy, the address dialled is the proxy's and the destination is resolved by somebody
// else, so the check above would protect nothing. Notifications therefore ignore any proxy the
// environment names. A deployment that must send through one needs this decided again, deliberately.
//
// # A transport this cannot guard is replaced, not trusted
//
// Only an *http.Transport exposes a dialer to put the check in. A client carrying some other
// transport has its transport replaced by a guarded default, and the replacement is logged: keeping
// an unguarded transport would be the protection failing open without anybody being told.
func guard(client *http.Client, d Destinations, log *slog.Logger) *http.Client {
	guarded := *client
	guarded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var base *http.Transport
	switch t := client.Transport.(type) {
	case nil:
		base = http.DefaultTransport.(*http.Transport).Clone()
	case *http.Transport:
		base = t.Clone()
	default:
		if log != nil {
			log.Warn("notifications: a transport that cannot be guarded was replaced by the default one")
		}
		base = http.DefaultTransport.(*http.Transport).Clone()
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			if d.permitPrivate {
				return nil
			}
			// An address that does not parse — a zoned link-local one, say — is refused, not waved
			// through: this check fails closed.
			host, _, err := net.SplitHostPort(address)
			if err != nil || !routable(net.ParseIP(host)) {
				return errNotRoutable
			}
			return nil
		}}
	base.DialContext = dialer.DialContext
	base.Proxy = nil
	guarded.Transport = base
	return &guarded
}

// Reason is a failure as a category: the form the store may keep and a read-only caller may read.
//
// Never the raw error. A Go network error carries the address and port it failed to reach, and the
// delivery list is readable by read-only credentials — so the raw string would tell anybody with a
// key which internal addresses a destination led to. The category says what kind of thing
// went wrong, which is all a receiver's owner can act on.
func (r Result) Reason() string {
	if r.Err == nil {
		return ""
	}
	switch {
	case errors.Is(r.Err, ErrNotPermitted):
		return "destination not permitted"
	case errors.Is(r.Err, errNotRoutable):
		return "address not permitted"
	case errors.Is(r.Err, errRedirectRefused):
		return "redirect not followed"
	case r.Status == http.StatusTooManyRequests || r.Status >= 500:
		return fmt.Sprintf("destination answered %d", r.Status)
	case r.Status >= 400:
		return fmt.Sprintf("destination refused with %d", r.Status)
	case errors.Is(r.Err, syscall.ECONNREFUSED):
		return "connection refused"
	}
	// Both a request context's deadline and the client's own timeout say so through Timeout().
	var timeout interface{ Timeout() bool }
	if errors.As(r.Err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	var certificate *tls.CertificateVerificationError
	if errors.As(r.Err, &certificate) {
		return "tls failure"
	}
	return "network error"
}
