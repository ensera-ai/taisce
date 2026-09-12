// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package notify decides where a formation notification may be sent, and signs it.
//
// # What this package is allowed to decide, and what it must not
//
// It decides whether a destination is permitted and what a signature is. It decides nothing about
// when a notification is owed, what it says, or how often it is retried — those are the store's and
// the sender's, because they are about the log and about failure, and this is about egress.
//
// # Why this is the most dangerous string in the product
//
// Every other place memory or metadata leaves this system goes to an address an OPERATOR wrote down
// once: the model endpoint, the database. A notification goes to an address a CUSTOMER supplies
// through the API, with a credential they already hold. That is a request this deployment makes, to
// wherever it is told, from inside whatever network it runs in — the classic shape of an attack
// where the interesting target is not on the internet at all but one hop away on a private address.
//
// So two independent things have to be true, and neither is a default. An operator must name the
// hosts a project may nominate, which means notifications do not exist until somebody deliberately
// turns them on. And the address must not resolve into this deployment's own neighbourhood, checked
// at the moment of sending rather than at the moment of registration, because a name that answered
// with a public address on Tuesday can answer with a private one on Wednesday.
package notify

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrNotPermitted is a destination this deployment will not send to. One error for every reason,
// because a caller registering an endpoint should learn that it is not allowed and not learn which
// private addresses exist behind the refusal.
var ErrNotPermitted = errors.New("this deployment does not send notifications to that destination")

// Destinations is what an operator permitted. The zero value permits nothing, which is the point: a
// deployment that has not been configured for notifications does not make outbound requests.
type Destinations struct {
	// hosts are exact hostnames or a leading-dot suffix ("example.com", ".example.com"). A suffix
	// permits subdomains and nothing else; an empty list permits nothing.
	hosts []string
	// permitPrivate lets a destination resolve to a private or loopback address. Off unless an
	// operator says otherwise, and the only reason to say otherwise is a deployment whose receiver
	// genuinely is on the same private network — which is a thing to state, not to discover.
	permitPrivate bool
}

// Permit builds the operator's list. An empty list is not an error: it is a deployment with
// notifications off, and the sender refuses every destination.
func Permit(list string, permitPrivate bool) Destinations {
	var hosts []string
	for _, h := range strings.Split(list, ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	return Destinations{hosts: hosts, permitPrivate: permitPrivate}
}

// Enabled reports whether any destination could be permitted at all.
func (d Destinations) Enabled() bool { return len(d.hosts) > 0 }

// Allow checks a URL against what the operator permitted: the scheme, the host and, unless the
// operator said otherwise, that it does not resolve into somewhere this deployment can reach and the
// internet cannot.
//
// The resolution is deliberately at send time as well as at registration. A destination is a name,
// and a name's answer is somebody else's to change.
func (d Destinations) Allow(raw string, resolve func(string) ([]net.IP, error)) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: it is not a URL", ErrNotPermitted)
	}
	// HTTPS only. A notification carries a signature and a customer's scope name; over plaintext the
	// signature is still valid and the scope is still readable by anybody on the path.
	if parsed.Scheme != "https" {
		return fmt.Errorf("%w: only https", ErrNotPermitted)
	}
	if parsed.User != nil {
		// Credentials in the URL would be sent by us, logged by them, and are never what a webhook
		// needs — the signature is the authentication.
		return fmt.Errorf("%w: a destination carries no credentials", ErrNotPermitted)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || !d.listed(host) {
		return fmt.Errorf("%w: the operator has not listed that host", ErrNotPermitted)
	}
	if d.permitPrivate {
		return nil
	}
	if resolve == nil {
		resolve = func(h string) ([]net.IP, error) { return net.LookupIP(h) }
	}
	addresses, err := resolve(host)
	if err != nil || len(addresses) == 0 {
		return fmt.Errorf("%w: it does not resolve", ErrNotPermitted)
	}
	for _, ip := range addresses {
		if !routable(ip) {
			// One bad answer is enough. A name that resolves to both a public and a private address
			// is the shape of the attack, not a coincidence to tolerate.
			return fmt.Errorf("%w: it resolves inside this deployment's own network", ErrNotPermitted)
		}
	}
	return nil
}

func (d Destinations) listed(host string) bool {
	for _, allowed := range d.hosts {
		if strings.HasPrefix(allowed, ".") {
			if strings.HasSuffix(host, allowed) {
				return true
			}
			continue
		}
		if host == allowed {
			return true
		}
	}
	return false
}

// routable reports whether an address is one the public internet could have given us. Everything
// else — loopback, link-local, private, multicast, unspecified, and the IPv4 address embedded in an
// IPv6 form of it — is somewhere only this deployment can reach.
func routable(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsPrivate() {
		return false
	}
	// An IPv4-mapped IPv6 address answers the IPv6 questions above as itself; ask them again of the
	// address it actually is.
	if four := ip.To4(); four != nil && !ip.Equal(four) {
		return routable(four)
	}
	// Carrier-grade NAT and the IPv6 unique-local range are not private by the standard library's
	// reckoning and are not the public internet either.
	if four := ip.To4(); four != nil && four[0] == 100 && four[1] >= 64 && four[1] <= 127 {
		return false
	}
	if len(ip) == net.IPv6len && ip.To4() == nil && (ip[0]&0xfe) == 0xfc {
		return false
	}
	return true
}
