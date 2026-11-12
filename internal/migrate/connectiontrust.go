// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FollowTrust makes every new connection of a pool read again the certificates its connection
// string names, instead of using the ones that were there when the pool was built.
//
// # What it is protecting, and from whom
//
// A connection string that says `sslmode=verify-full` and names a certificate authority is how a
// process knows that what answered at the database's address is the database. Without that check
// a connection is encrypted to whoever answers, and something on the path between a pod and its
// database can answer.
//
// # Why reading the file once is not enough
//
// A pool reads the authority's file when it is built and verifies every later connection against
// what it read. An authority does not last: one issued by an operator inside a cluster is replaced
// on a schedule measured in weeks. A process that has been running since before the replacement
// goes on verifying against the old certificate until that certificate expires — and from that
// moment every new connection it opens is refused, across every replica that started before the
// replacement, with nothing restarting them, because a process that cannot reach its database is
// alive and merely not ready. Verification would be an outage with a date on it.
//
// So the file is read again whenever a connection is opened. The file is whatever the deployment
// keeps current — a mounted secret is rewritten in place when it changes — and a connection opened
// after a replacement is verified against the authority as it is now. No restart, and no date.
//
// # Why it parses the whole string again
//
// The driver turns a connection string into TLS settings in one place, and what it produces
// differs by mode in ways that are easy to get wrong by hand: `verify-ca` is built as a custom
// verification that closes over the authority it was given, so replacing the authority on the
// settings afterwards would leave the old one in force. Parsing the string again yields exactly
// what a newly started process would use, for every mode, and for a client certificate and key as
// well, which are replaced on the same kind of schedule.
//
// # What it costs, and what it does not cover
//
// A parse and a file read for each physical connection, beside a TLS handshake that costs far
// more; a pool opens connections rarely. A file that cannot be read when a connection is opened
// fails that connection rather than falling back to the old authority or to none: the pool tries
// again on the next request, and a deployment whose trust material has gone missing does not
// quietly stop checking.
//
// It does nothing for a connection that is already open, which was verified when it was opened and
// stays as it is. And it reads a file: an authority supplied some other way, by an environment
// variable for instance, is read again too, but only changes if the process's environment does.
func FollowTrust(config *pgxpool.Config, dsn string) {
	previous := config.BeforeConnect
	config.BeforeConnect = func(ctx context.Context, connecting *pgx.ConnConfig) error {
		if previous != nil {
			if err := previous(ctx, connecting); err != nil {
				return err
			}
		}
		current, err := pgconn.ParseConfig(dsn)
		if err != nil {
			return fmt.Errorf("read the connection's certificates again: %w", err)
		}
		// The settings for the first host, and for every alternative the string allows. Nothing
		// else is taken: where to connect, as whom, and every parameter are the pool's.
		connecting.TLSConfig = current.TLSConfig
		connecting.Fallbacks = current.Fallbacks
		return nil
	}
}

// NewAdminPool opens the pool a command or the management surface holds: no privilege check, since
// it is the connection that grants privileges, and the same following of the certificate authority
// the serving pools have. The management surface holds it for the life of its process, which is
// exactly as long as the serving processes hold theirs.
func NewAdminPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	FollowTrust(config, dsn)
	return pgxpool.NewWithConfig(ctx, config)
}
