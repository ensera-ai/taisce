// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"os"
	"strings"
)

// The operator's two variables, and what their absence means.
const (
	// EnvDestinations names the hosts a project may nominate, comma separated. An entry is an exact
	// hostname or a leading-dot suffix that permits its subdomains.
	//
	// Unset means notifications are off. That is the whole safety of the feature: the destination of
	// every other egress in this product is an operator's decision, and this one is a customer's, so
	// it cannot be arrived at by default — only by an operator writing a host down.
	EnvDestinations = "TAISCE_NOTIFY_DESTINATIONS"
	// EnvPermitPrivate lets a destination resolve to an address only this deployment can reach. Off
	// unless set to "true", because the reason to turn it on — a receiver genuinely inside the same
	// private network — is a thing to state rather than to discover.
	EnvPermitPrivate = "TAISCE_NOTIFY_PERMIT_PRIVATE"
)

// FromEnv reads what the operator permitted. There is no error case: an empty list is a valid
// deployment with notifications off, and a deployment that has not been configured for them makes no
// outbound requests at all.
func FromEnv() Destinations {
	return Permit(os.Getenv(EnvDestinations),
		strings.EqualFold(strings.TrimSpace(os.Getenv(EnvPermitPrivate)), "true"))
}
