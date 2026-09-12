// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package notify_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ensera-ai/taisce/internal/notify"
)

// ── #49's egress ──────────────────────────────────────────────────────────────────────────────
//
// A notification goes to an address a customer supplies, from inside whatever network this
// deployment runs in. That is the one egress in the product whose destination is not an operator's
// decision, so two things have to be true and neither is a default: the operator listed the host,
// and the host does not resolve into somewhere only this deployment can reach.
func TestNothingIsSentUntilAnOperatorSaysWhereAndNeverIntoTheDeploymentsOwnNetwork(t *testing.T) {
	public := func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil }

	// Unconfigured, every destination is refused. A deployment nobody set up makes no outbound call.
	off := notify.Permit("", false)
	if off.Enabled() {
		t.Fatal("notifications are on with no destination listed")
	}
	if err := off.Allow("https://hooks.example.com/taisce", public); !errors.Is(err, notify.ErrNotPermitted) {
		t.Fatalf("an unconfigured deployment sent somewhere: %v", err)
	}

	allowed := notify.Permit("hooks.example.com, .partner.example", false)
	if !allowed.Enabled() {
		t.Fatal("a listed host did not enable notifications")
	}
	for _, ok := range []string{
		"https://hooks.example.com/taisce",
		"https://anything.partner.example/in",
	} {
		if err := allowed.Allow(ok, public); err != nil {
			t.Fatalf("a listed destination was refused: %s: %v", ok, err)
		}
	}

	refused := map[string]string{
		"a host nobody listed":              "https://evil.example.com/in",
		"a suffix that only looks like one": "https://notpartner.example/in",
		"plain http":                        "http://hooks.example.com/in",
		"a credential in the URL":           "https://user:pass@hooks.example.com/in",
		"not a URL at all":                  "not a url",
		"no host":                           "https:///in",
	}
	for name, destination := range refused {
		if err := allowed.Allow(destination, public); !errors.Is(err, notify.ErrNotPermitted) {
			t.Fatalf("%s was permitted: %s (%v)", name, destination, err)
		}
	}

	// And the addresses. Every one of these is somewhere the internet cannot reach and this
	// deployment can, which is the whole shape of the attack.
	for name, address := range map[string]string{
		"loopback":                             "127.0.0.1",
		"loopback in IPv6":                     "::1",
		"a private range":                      "10.1.2.3",
		"another":                              "192.168.0.7",
		"link-local":                           "169.254.169.254",
		"IPv6 link-local":                      "fe80::1",
		"IPv6 unique-local":                    "fd00::1",
		"carrier-grade NAT":                    "100.64.0.1",
		"an IPv4 address wearing IPv6 clothes": "::ffff:127.0.0.1",
		"unspecified":                          "0.0.0.0",
	} {
		resolve := func(string) ([]net.IP, error) { return []net.IP{net.ParseIP(address)}, nil }
		if err := allowed.Allow("https://hooks.example.com/in", resolve); !errors.Is(err, notify.ErrNotPermitted) {
			t.Fatalf("%s (%s) was permitted", name, address)
		}
	}

	// One bad answer among good ones is still bad: a name resolving to both is the attack, not luck.
	mixed := func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("169.254.169.254")}, nil
	}
	if err := allowed.Allow("https://hooks.example.com/in", mixed); !errors.Is(err, notify.ErrNotPermitted) {
		t.Fatal("a destination resolving to one private address among public ones was permitted")
	}

	// A name that does not resolve is refused rather than attempted.
	dead := func(string) ([]net.IP, error) { return nil, errors.New("no such host") }
	if err := allowed.Allow("https://hooks.example.com/in", dead); !errors.Is(err, notify.ErrNotPermitted) {
		t.Fatal("an unresolvable destination was permitted")
	}

	// An operator whose receiver genuinely is on the private network says so, and then it is allowed
	// — deliberately, by configuration, and never by default.
	stated := notify.Permit("hooks.internal", true)
	if err := stated.Allow("https://hooks.internal/in", func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.0.0.5")}, nil
	}); err != nil {
		t.Fatalf("an operator who stated a private destination was still refused: %v", err)
	}

	// The refusal says nothing about what is behind it.
	err := allowed.Allow("https://hooks.example.com/in", func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.11.12.13")}, nil
	})
	if strings.Contains(err.Error(), "10.11.12.13") {
		t.Fatalf("the refusal disclosed a private address: %v", err)
	}
}

// ── #49's signature ───────────────────────────────────────────────────────────────────────────
//
// A receiver must be able to tell our call from anybody's, and must not be fooled by one of ours
// replayed later.
func TestASignatureFailsOnOneAlteredByteAndOnADeliveryReplayedLater(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	body := []byte(`{"scope":"p1","formed_through":42}`)
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	header := notify.Sign(secret, at, body)

	if err := notify.Verify(secret, header, body, at.Add(3*time.Second), time.Minute); err != nil {
		t.Fatalf("a fresh delivery did not verify: %v", err)
	}

	// One byte, anywhere.
	altered := append([]byte(nil), body...)
	altered[len(altered)-2] = '3'
	if err := notify.Verify(secret, header, altered, at, time.Minute); err == nil {
		t.Fatal("a body altered by one byte still verified")
	}

	// Somebody else's secret.
	if err := notify.Verify([]byte("fedcba9876543210fedcba9876543210"), header, body, at, time.Minute); err == nil {
		t.Fatal("a signature verified under the wrong secret")
	}

	// The same delivery, a week later.
	if err := notify.Verify(secret, header, body, at.Add(7*24*time.Hour), 5*time.Minute); err == nil {
		t.Fatal("a delivery replayed a week later still verified")
	}
	// And a timestamp moved forward in the header rather than re-signed.
	forged := strings.Replace(header, "t=1789128000", "t=1789732800", 1)
	if forged != header {
		if err := notify.Verify(secret, forged, body, time.Unix(1789732800, 0), time.Minute); err == nil {
			t.Fatal("a delivery with its timestamp rewritten still verified")
		}
	}
	// A header that is not one.
	for _, bad := range []string{"", "nonsense", "v2 t=1 s=abc"} {
		if err := notify.Verify(secret, bad, body, at, time.Minute); err == nil {
			t.Fatalf("a malformed header verified: %q", bad)
		}
	}
}

// The destination is checked again at the moment of sending, not only when it was registered. A
// name's answer is somebody else's to change after we agreed to it, and that change is the attack.
func TestASenderRefusesADestinationThatHasStartedResolvingSomewhereElse(t *testing.T) {
	body := []byte("{}")
	_ = body
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	host := hostFrom(t, receiver.URL)

	// While it resolves where the operator expects, it sends.
	sender := notify.NewSender(notify.Permit(host, true), nil).WithHTTPClient(receiver.Client())
	if result := sender.Send(context.Background(), notify.Delivery{
		ID: "d1", Scope: "p1", URL: receiver.URL + "/hook", Secret: []byte("0123456789abcdef0123456789abcdef"),
	}); result.Err != nil {
		t.Fatalf("a permitted destination was refused: %v", result.Err)
	}

	// The same deployment, the same registration, a host that now answers with a private address.
	// Refused, and refused finally: this is policy, not weather.
	strict := notify.NewSender(notify.Permit(host, false), nil).WithHTTPClient(receiver.Client())
	result := strict.Send(context.Background(), notify.Delivery{
		ID: "d1", Scope: "p1", URL: receiver.URL + "/hook", Secret: []byte("0123456789abcdef0123456789abcdef"),
	})
	if !errors.Is(result.Err, notify.ErrNotPermitted) || result.Retryable {
		t.Fatalf("a destination inside the deployment's own network was sent to, or retried: %+v", result)
	}

	// A destination nobody listed never reaches the network at all.
	none := notify.NewSender(notify.Permit("", false), nil)
	if none.Enabled() {
		t.Fatal("an unconfigured sender reported itself enabled")
	}
	if result := none.Send(context.Background(), notify.Delivery{URL: "https://hooks.example.com/in"}); result.Retryable {
		t.Fatal("an unconfigured sender offered to try again")
	}
	if err := none.Allow("https://hooks.example.com/in"); !errors.Is(err, notify.ErrNotPermitted) {
		t.Fatalf("an unconfigured sender permitted a destination: %v", err)
	}
}

// What an operator's two variables mean, including the case where they mean nothing.
func TestTheOperatorsConfigurationIsOffUntilItSaysOtherwise(t *testing.T) {
	t.Setenv(notify.EnvDestinations, "")
	t.Setenv(notify.EnvPermitPrivate, "")
	if notify.FromEnv().Enabled() {
		t.Fatal("an unset deployment permits a destination")
	}
	t.Setenv(notify.EnvDestinations, " hooks.example.com , ")
	destinations := notify.FromEnv()
	if !destinations.Enabled() {
		t.Fatal("a listed host did not enable notifications")
	}
	// Whitespace and an empty trailing entry are a typo, not a permission.
	if err := destinations.Allow("https://hooks.example.com/in", func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}); err != nil {
		t.Fatalf("a listed host was refused: %v", err)
	}
	// Private is off unless the word is exactly true, case aside.
	t.Setenv(notify.EnvPermitPrivate, "yes")
	if err := notify.FromEnv().Allow("https://hooks.example.com/in", func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.0.0.1")}, nil
	}); !errors.Is(err, notify.ErrNotPermitted) {
		t.Fatal("a private destination was permitted by a value that is not true")
	}
	t.Setenv(notify.EnvPermitPrivate, "TRUE")
	if err := notify.FromEnv().Allow("https://hooks.example.com/in", func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.0.0.1")}, nil
	}); err != nil {
		t.Fatalf("an operator who said true was still refused: %v", err)
	}
}

func hostFrom(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Hostname()
}

// ── #233: a permitted destination cannot choose a second one ─────────────────────────────────
//
// A redirect from a listed host is answered, never followed, whatever it points at — here another
// listener on this machine, standing for the deployment's own network. The signed body never
// reaches it, and the refusal is final: the receiver chose to redirect, and asking again gets the
// same answer.
func TestARedirectFromAPermittedDestinationIsNeverFollowed(t *testing.T) {
	var reached atomic.Int64
	inside := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer inside.Close()
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, inside.URL+"/admin", status)
		}))
		// Private addresses are permitted, so the only thing that can stop the second hop is the
		// refusal to take it.
		sender := notify.NewSender(notify.Permit(hostFrom(t, receiver.URL), true), nil).WithHTTPClient(receiver.Client())
		result := sender.Send(context.Background(), delivery(receiver.URL+"/hook"))
		receiver.Close()
		if result.Err == nil || result.Retryable || result.Status != status {
			t.Fatalf("a %d was followed, accepted or offered a retry: %+v", status, result)
		}
		if result.Reason() != "redirect not followed" {
			t.Fatalf("a refused %d is recorded as %q", status, result.Reason())
		}
	}
	if n := reached.Load(); n != 0 {
		t.Fatalf("the redirect target received %d signed delivery(ies)", n)
	}
}

// The check before sending resolves the name; the connection resolves it again. A rebinding answer
// — public for the first lookup, private for the second — passes the first, so the second is checked
// where it is dialled. Staged with an injected resolver that answers public, and a name the machine
// itself resolves to loopback. The listener behind it is never reached, and the reason recorded names
// no address.
func TestARebindingAnswerIsRefusedOnTheAddressActuallyDialled(t *testing.T) {
	var reached atomic.Int64
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	port := strings.TrimPrefix(receiver.URL, "https://127.0.0.1:")
	public := func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil }

	sender := notify.NewSender(notify.Permit("localhost", false), nil).
		WithHTTPClient(receiver.Client()).WithResolver(public)
	if err := sender.Allow("https://localhost:" + port + "/hook"); err != nil {
		t.Fatalf("the staging is wrong: the check before sending refused the public answer: %v", err)
	}
	result := sender.Send(context.Background(), delivery("https://localhost:"+port+"/hook"))
	if result.Err == nil || result.Retryable {
		t.Fatalf("a connection to loopback was made, or offered a retry: %+v", result)
	}
	if reached.Load() != 0 {
		t.Fatal("the listener behind a rebinding answer received the delivery")
	}
	if reason := result.Reason(); reason != "address not permitted" || strings.Contains(reason, port) {
		t.Fatalf("the rebinding refusal is recorded as %q", reason)
	}
	// The raw error does carry the address — which is exactly why it is not what gets stored.
	if !strings.Contains(result.Err.Error(), port) {
		t.Logf("the transport's error no longer names the port it failed on: %v", result.Err)
	}
}

// What a delivery's failure is stored as: a category an owner can act on, and never the transport's
// own words, which name the address and port a destination led to. Each category is produced by the
// real failure it names.
func TestAFailureIsStoredAsACategoryThatNamesNoAddress(t *testing.T) {
	answering := func(status int) *httptest.Server {
		return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
	}
	send := func(server *httptest.Server, client *http.Client) notify.Result {
		sender := notify.NewSender(notify.Permit(hostFrom(t, server.URL), true), nil)
		if client != nil {
			sender = sender.WithHTTPClient(client)
		}
		return sender.Send(context.Background(), delivery(server.URL+"/hook"))
	}

	unavailable := answering(http.StatusServiceUnavailable)
	defer unavailable.Close()
	busy := answering(http.StatusTooManyRequests)
	defer busy.Close()
	missing := answering(http.StatusNotFound)
	defer missing.Close()
	// Held until the test lets go, not until the client leaves: a server notices a departed client
	// only once it has read the body, and waiting on that would make Close wait with it.
	release := make(chan struct{})
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)
	hangingUp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}))
	defer hangingUp.Close()
	closed := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	quick := &http.Client{Timeout: 200 * time.Millisecond, Transport: slow.Client().Transport}
	cases := []struct {
		name   string
		result notify.Result
		want   string
	}{
		{"a success", send(unavailable, nil), ""}, // replaced below; a success has no reason
		{"a 503", send(unavailable, unavailable.Client()), "destination answered 503"},
		{"a 429", send(busy, busy.Client()), "destination answered 429"},
		{"a 404", send(missing, missing.Client()), "destination refused with 404"},
		{"a timeout", send(slow, quick), "timeout"},
		{"a hang-up", send(hangingUp, hangingUp.Client()), "network error"},
		// The default client does not trust the test's certificate authority: a real verification failure.
		{"an untrusted certificate", send(missing, nil), "tls failure"},
		{"nobody listening", notify.NewSender(notify.Permit("127.0.0.1", true), nil).
			Send(context.Background(), delivery(closedURL+"/hook")), "connection refused"},
		{"an unlisted host", notify.NewSender(notify.Permit("hooks.example.com", true), nil).
			Send(context.Background(), delivery(missing.URL+"/hook")), "destination not permitted"},
	}
	cases[0].result = notify.Result{Status: http.StatusOK}
	for _, c := range cases {
		reason := c.result.Reason()
		if reason != c.want {
			t.Errorf("%s is recorded as %q, want %q (raw: %v)", c.name, reason, c.want, c.result.Err)
		}
		if strings.Contains(reason, "127.0.0.1") || strings.Contains(reason, ":") {
			t.Errorf("%s is recorded with an address in it: %q", c.name, reason)
		}
	}
}

// A client whose transport the sender cannot put its checks into is not trusted with a delivery:
// its transport is replaced, and the replacement is logged rather than silent.
func TestATransportTheSenderCannotGuardIsReplacedNotTrusted(t *testing.T) {
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	var used atomic.Int64
	opaque := roundTripper(func(r *http.Request) (*http.Response, error) {
		used.Add(1)
		return receiver.Client().Transport.RoundTrip(r)
	})
	var logged bytes.Buffer
	sender := notify.NewSender(notify.Permit(hostFrom(t, receiver.URL), true), slog.New(slog.NewTextHandler(&logged, nil))).
		WithHTTPClient(&http.Client{Transport: opaque})
	result := sender.Send(context.Background(), delivery(receiver.URL+"/hook"))
	if used.Load() != 0 {
		t.Fatal("a delivery went through a transport the sender could not guard")
	}
	// The replacement does not trust the test's certificate authority, which is how this shows the
	// replacement was the transport used.
	if result.Reason() != "tls failure" {
		t.Fatalf("the replaced transport was not the one used: %+v", result)
	}
	if !strings.Contains(logged.String(), "cannot be guarded") {
		t.Fatalf("the replacement was silent: %q", logged.String())
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func delivery(url string) notify.Delivery {
	return notify.Delivery{ID: "d1", Scope: "p1", URL: url, Secret: []byte("0123456789abcdef0123456789abcdef")}
}
