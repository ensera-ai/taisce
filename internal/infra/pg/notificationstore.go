// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package pg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errors a caller can act on. Everything else is this system's problem and says so.
var (
	// ErrEndpointNotFound is an endpoint that is not this project's, or is not there at all. One
	// error for both, so an id cannot be used to learn what another project registered.
	ErrEndpointNotFound = errors.New("notification endpoint not found")
	// ErrEndpointExists is the same URL registered twice in one project. Refused rather than
	// duplicated, because two rows would deliver every formation twice to one listener.
	ErrEndpointExists = errors.New("that destination is already registered for this project")
)

// NotificationStore holds where a project wants to be told, and what is owed.
type NotificationStore struct {
	pool   *pgxpool.Pool
	schema Schema
}

func NewNotificationStore(pool *pgxpool.Pool, schema Schema) *NotificationStore {
	return &NotificationStore{pool: pool, schema: schema}
}

// Endpoint is a destination as it is listed. The secret is absent: it is returned once, by Register,
// and never read back — an operator who has lost it rotates rather than looks.
type Endpoint struct {
	ID         string     `json:"id"`
	URL        string     `json:"url"`
	CreatedAt  time.Time  `json:"created_at"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}

// Registered is an endpoint and the one sight of its secret.
type Registered struct {
	Endpoint
	// Secret signs every delivery to this destination. Shown once, like a credential, for the same
	// reason: what is stored is what we must sign with, and what is shown is what the receiver must
	// verify with, and there is no third party who needs it afterwards.
	Secret string `json:"secret"`
}

// Register adds a destination to a project and mints its signing secret.
func (s *NotificationStore) Register(ctx context.Context, scope, url string) (Registered, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return Registered{}, fmt.Errorf("mint a signing secret: %w", err)
	}
	out := Registered{Secret: hex.EncodeToString(secret)}
	err := s.pool.QueryRow(ctx, s.schema.SQL(`
		INSERT INTO {schema}.notification_endpoint (endpoint_id, scope, url, secret)
		VALUES (gen_random_uuid(), $1, $2, $3)
		RETURNING endpoint_id::text, url, created_at`), scope, url, secret).
		Scan(&out.ID, &out.URL, &out.CreatedAt)
	if isUniqueViolation(err) {
		return Registered{}, ErrEndpointExists
	}
	if err != nil {
		return Registered{}, fmt.Errorf("register the destination: %w", err)
	}
	return out, nil
}

// Endpoints lists a project's destinations, disabled ones included, because an operator asking
// "where does this go" is owed the ones that used to.
func (s *NotificationStore) Endpoints(ctx context.Context, scope string) ([]Endpoint, error) {
	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT endpoint_id::text, url, created_at, disabled_at
		  FROM {schema}.notification_endpoint WHERE scope=$1 ORDER BY created_at, endpoint_id`), scope)
	if err != nil {
		return nil, fmt.Errorf("list destinations: %w", err)
	}
	defer rows.Close()
	out := []Endpoint{}
	for rows.Next() {
		var e Endpoint
		if err := rows.Scan(&e.ID, &e.URL, &e.CreatedAt, &e.DisabledAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Disable stops delivery to a destination without forgetting it, so a parked delivery can still say
// where it was going.
func (s *NotificationStore) Disable(ctx context.Context, scope, id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return ErrEndpointNotFound
	}
	tag, err := s.pool.Exec(ctx, s.schema.SQL(`
		UPDATE {schema}.notification_endpoint SET disabled_at=now()
		 WHERE scope=$1 AND endpoint_id=$2::uuid AND disabled_at IS NULL`), scope, parsed.String())
	if err != nil {
		return fmt.Errorf("disable the destination: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrEndpointNotFound
	}
	return nil
}

// Owed enqueues a delivery per live endpoint for a scope that has formed further than that endpoint
// has been told.
//
// # Why this reads the watermark rather than being called by formation
//
// A delivery written inside the formation transaction leaves two choices, both bad: a failed
// delivery rolls back a turn, so memory is lost to somebody else's broken endpoint; or the failure
// is swallowed, which is a promise made and not kept. Reading the watermark afterwards costs one
// query per pass and owes nothing to the outcome of the send.
//
// The unique constraint on (endpoint, formed_through) is what makes this safe to call on every pass:
// formation runs continuously and most passes have no news, so the insert simply finds the row it
// would have written already there.
func (s *NotificationStore) Owed(ctx context.Context, scope string, formed, stored int64, parked int) (int, error) {
	tag, err := s.pool.Exec(ctx, s.schema.SQL(`
		INSERT INTO {schema}.notification_delivery
		       (delivery_id, scope, endpoint_id, formed_through, stored_through, parked_turns)
		SELECT gen_random_uuid(), e.scope, e.endpoint_id, $2, $3, $4
		  FROM {schema}.notification_endpoint e
		 WHERE e.scope=$1 AND e.disabled_at IS NULL
		ON CONFLICT (endpoint_id, formed_through) DO NOTHING`), scope, formed, stored, parked)
	if err != nil {
		return 0, fmt.Errorf("record what is owed: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// Due is one delivery to attempt, claimed so that two workers cannot take the same one.
type Due struct {
	ID            string
	Scope         string
	EndpointID    string
	URL           string
	Secret        []byte
	FormedThrough int64
	StoredThrough int64
	ParkedTurns   int
	Attempts      int
}

// Claim takes the oldest delivery that is due, or reports that nothing is.
//
// `FOR UPDATE SKIP LOCKED` rather than a status column: a worker that dies holding a claim releases
// it when its connection goes, which is the same guarantee formation's scope lock relies on, and it
// needs no sweeper to notice.
//
// # One clock, and it is the database's
//
// Due-ness is decided by `now()` here rather than by a timestamp the caller supplies. The schedule
// is written by the database — `next_attempt_at` defaults to its clock and backoff is computed from
// it — so comparing it against an application's clock makes delivery depend on two machines
// agreeing. They do not: the database in this repository's own test environment runs about a tenth
// of a second ahead of its host, which is enough to make every freshly written delivery not yet due;
// a host a minute behind would deliver nothing at all and report no error. Whichever clock writes
// the schedule is the one that must read it.
func (s *NotificationStore) Claim(ctx context.Context) (Due, bool, error) {
	var d Due
	err := s.pool.QueryRow(ctx, s.schema.SQL(`
		WITH claimed AS (
		  SELECT d.delivery_id FROM {schema}.notification_delivery d
		   WHERE d.delivered_at IS NULL AND d.parked_at IS NULL AND d.next_attempt_at <= now()
		   ORDER BY d.next_attempt_at, d.delivery_id
		   LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		UPDATE {schema}.notification_delivery d
		   SET attempts = d.attempts + 1
		  FROM claimed c, {schema}.notification_endpoint e
		 WHERE d.delivery_id = c.delivery_id AND e.endpoint_id = d.endpoint_id
		RETURNING d.delivery_id::text, d.scope, e.endpoint_id::text, e.url, e.secret,
		          d.formed_through, d.stored_through, d.parked_turns, d.attempts`)).
		Scan(&d.ID, &d.Scope, &d.EndpointID, &d.URL, &d.Secret,
			&d.FormedThrough, &d.StoredThrough, &d.ParkedTurns, &d.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Due{}, false, nil
	}
	if err != nil {
		return Due{}, false, fmt.Errorf("claim a delivery: %w", err)
	}
	return d, true, nil
}

// Delivered closes a delivery.
func (s *NotificationStore) Delivered(ctx context.Context, id string, status int) error {
	_, err := s.pool.Exec(ctx, s.schema.SQL(`
		UPDATE {schema}.notification_delivery
		   SET delivered_at=now(), last_status=$2, last_error=NULL WHERE delivery_id=$1::uuid`), id, status)
	return err
}

// Failed records an attempt that did not land, and decides whether to wait or to stop.
//
// Parking rather than retrying forever: an endpoint that has refused a delivery for hours is not
// coming back within this delivery's usefulness, and a queue that never empties is a queue nobody
// can read. Parked is visible, which is the point — it is a state to be asked about, not a log line.
func (s *NotificationStore) Failed(ctx context.Context, id string, attempts, budget int,
	status int, reason string, backoff time.Duration) error {
	if len(reason) > 512 {
		reason = reason[:512]
	}
	if attempts >= budget {
		_, err := s.pool.Exec(ctx, s.schema.SQL(`
			UPDATE {schema}.notification_delivery
			   SET parked_at=now(), last_status=$2, last_error=$3 WHERE delivery_id=$1::uuid`),
			id, nullIfZero(status), reason)
		return err
	}
	_, err := s.pool.Exec(ctx, s.schema.SQL(`
		UPDATE {schema}.notification_delivery
		   SET next_attempt_at=now()+$4::interval, last_status=$2, last_error=$3
		 WHERE delivery_id=$1::uuid`), id, nullIfZero(status), reason, backoff.String())
	return err
}

// DeliveryRecord is one delivery as an operator or a customer reads it back. No content, because
// there was none: a scope, a watermark and what happened.
type DeliveryRecord struct {
	ID            string     `json:"id"`
	EndpointID    string     `json:"endpoint_id"`
	URL           string     `json:"url"`
	FormedThrough int64      `json:"formed_through"`
	StoredThrough int64      `json:"stored_through"`
	Attempts      int        `json:"attempts"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
	ParkedAt      *time.Time `json:"parked_at,omitempty"`
	LastStatus    *int       `json:"last_status,omitempty"`
	LastError     *string    `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Deliveries lists a project's recent deliveries, newest first.
func (s *NotificationStore) Deliveries(ctx context.Context, scope string, limit int) ([]DeliveryRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, s.schema.SQL(`
		SELECT d.delivery_id::text, e.endpoint_id::text, e.url, d.formed_through, d.stored_through,
		       d.attempts, d.delivered_at, d.parked_at, d.last_status, d.last_error, d.created_at
		  FROM {schema}.notification_delivery d
		  JOIN {schema}.notification_endpoint e ON e.endpoint_id = d.endpoint_id
		 WHERE d.scope=$1 ORDER BY d.created_at DESC, d.delivery_id LIMIT $2`), scope, limit)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	defer rows.Close()
	out := []DeliveryRecord{}
	for rows.Next() {
		var d DeliveryRecord
		if err := rows.Scan(&d.ID, &d.EndpointID, &d.URL, &d.FormedThrough, &d.StoredThrough,
			&d.Attempts, &d.DeliveredAt, &d.ParkedAt, &d.LastStatus, &d.LastError, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func nullIfZero(status int) any {
	if status == 0 {
		return nil
	}
	return status
}
