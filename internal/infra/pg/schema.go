// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package pg holds the PostgreSQL adapters. Templates use {schema} for the instance memory
// namespace. Requests select authorized projects, never database namespaces.
package pg

import (
	"fmt"
	"strings"
)

// Schema is a validated SQL identifier. Validation prevents identifier injection and accidental
// truncation; it is not an authorization boundary. A role granted multiple schemas can read them.
// Project predicates enforce the authorized scope set inside the instance memory namespace.
type Schema string

// PostgreSQL's NAMEDATALEN - 1. A longer name is silently truncated and can select the wrong namespace.
const maxIdentifierLength = 63

// NewSchema validates a database namespace.
func NewSchema(name string) (Schema, error) {
	if name == "" {
		return "", fmt.Errorf("schema name is empty")
	}
	if len(name) > maxIdentifierLength {
		return "", fmt.Errorf("schema name %q is %d bytes; PostgreSQL truncates past %d and namespace names would collide",
			name, len(name), maxIdentifierLength)
	}
	if name[0] < 'a' || name[0] > 'z' {
		return "", fmt.Errorf("schema name %q must start with a lowercase letter", name)
	}
	for i := 1; i < len(name); i++ {
		if ch := name[i]; (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '_' {
			return "", fmt.Errorf("schema name %q contains %q; only [a-z0-9_] are permitted", name, string(ch))
		}
	}
	return Schema(name), nil
}

func (s Schema) String() string { return string(s) }

// SQL renders a statement template for this namespace.
func (s Schema) SQL(template string) string {
	return strings.ReplaceAll(template, "{schema}", string(s))
}
