// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// Package pg holds the PostgreSQL adapters. Every statement is a package-level const with {schema}
// where the tenant goes, so which tenant a query reads is answerable by reading the call site.
package pg

import (
	"fmt"
	"strings"
)

// Schema is a validated schema identifier, and a tenant is one.
//
// The tenant boundary is hard: a cross-tenant read is impossible because the other tenant's name is
// not in the statement, not because a predicate excluded it. That rests entirely on the identifier
// being trustworthy where it is interpolated — and a schema name cannot be a bind parameter, since
// PostgreSQL parameters are values and never identifiers.
//
// So interpolation is unavoidable, which means validation cannot be something each call site
// remembers. A Schema exists only if it passed NewSchema, and every builder takes nothing else.
type Schema string

// PostgreSQL's NAMEDATALEN - 1. A longer name is silently TRUNCATED, which for a tenant identifier
// means two tenants colliding into one schema — the worst failure this type can prevent.
const maxIdentifierLength = 63

// NewSchema validates a tenant's schema name.
func NewSchema(name string) (Schema, error) {
	if name == "" {
		return "", fmt.Errorf("schema name is empty")
	}
	if len(name) > maxIdentifierLength {
		return "", fmt.Errorf("schema name %q is %d bytes; PostgreSQL truncates past %d and two tenants would collide",
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

// SQL renders a statement template for this tenant.
func (s Schema) SQL(template string) string {
	return strings.ReplaceAll(template, "{schema}", string(s))
}
