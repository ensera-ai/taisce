-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- Formation happens behind the append, so there is a window in which a turn is stored and its facts
-- do not exist yet. The watermark answers "is it in memory" to whoever asks, and asking is the
-- problem: at agent turn rates most polls exist only to learn that nothing changed.
--
-- So a project can nominate somewhere to be told. Two tables, because the two things have different
-- lifetimes and different failure modes: an endpoint is configuration a customer sets and forgets, a
-- delivery is an attempt that can fail, wait and be abandoned.

CREATE TABLE {schema}.notification_endpoint (
    endpoint_id uuid        PRIMARY KEY,
    scope       text        NOT NULL REFERENCES {schema}.project (scope) ON DELETE CASCADE,
    url         text        NOT NULL CHECK (octet_length(url) BETWEEN 8 AND 2048),

    -- The shared secret a receiver checks our signature with.
    --
    -- Stored rather than digested, because we sign with it — a digest cannot produce an HMAC. It is
    -- not a credential to this system: it authenticates US to the RECEIVER, so what its disclosure
    -- buys an attacker is the ability to forge notifications at somebody else's endpoint, not access
    -- here. It is returned once when the endpoint is created and never again, so an operator who
    -- needs a new one rotates rather than reads.
    secret      bytea       NOT NULL CHECK (octet_length(secret) = 32),

    created_at  timestamptz NOT NULL DEFAULT now(),
    -- Disabled rather than deleted while deliveries still point at it, so a parked delivery can
    -- still say where it was going.
    disabled_at timestamptz,

    -- One endpoint per URL per project. A customer who registers the same address twice meant once,
    -- and two rows would mean every formation delivered twice to the same listener.
    UNIQUE (scope, url)
);

CREATE INDEX notification_endpoint_scope_idx
    ON {schema}.notification_endpoint (scope) WHERE disabled_at IS NULL;

-- A delivery is a row before it is an attempt.
--
-- Firing inside the formation transaction would mean one of two bad things: a delivery failure rolls
-- back a turn, so memory is lost to a customer's broken endpoint; or the failure is discarded, which
-- is a claim made and not kept. So a delivery is written durably and attempted afterwards, and
-- formation never waits on one.
CREATE TABLE {schema}.notification_delivery (
    delivery_id     uuid        PRIMARY KEY,
    scope           text        NOT NULL,
    endpoint_id     uuid        NOT NULL REFERENCES {schema}.notification_endpoint (endpoint_id) ON DELETE CASCADE,

    -- What the notification says, and the whole of it. A scope, an offset and counts: the content is
    -- read back through the authenticated API, so the widest egress in the product carries no
    -- memory. Deliberately no data subject — that would make a delivery row personal data and a
    -- projection erasure must cover, in exchange for a convenience a recall already provides.
    formed_through  bigint      NOT NULL,
    stored_through  bigint      NOT NULL,
    formed_turns    integer     NOT NULL DEFAULT 0,
    parked_turns    integer     NOT NULL DEFAULT 0,

    attempts        integer     NOT NULL DEFAULT 0,
    -- When it may next be tried. In the past means now; backoff moves it forward.
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    delivered_at    timestamptz,
    -- Abandoned after the attempt budget, and visible: a notification nobody is coming for should be
    -- findable by asking rather than by reading a log.
    parked_at       timestamptz,
    last_status     integer,
    last_error      text CHECK (last_error IS NULL OR octet_length(last_error) <= 512),
    created_at      timestamptz NOT NULL DEFAULT now(),

    -- One delivery per endpoint per watermark. Formation runs continuously and would otherwise
    -- enqueue the same news on every pass that found nothing new.
    UNIQUE (endpoint_id, formed_through)
);

-- The claim query: what is owed, oldest first, ready now.
CREATE INDEX notification_delivery_due_idx
    ON {schema}.notification_delivery (next_attempt_at, delivery_id)
    WHERE delivered_at IS NULL AND parked_at IS NULL;

CREATE INDEX notification_delivery_scope_idx
    ON {schema}.notification_delivery (scope, created_at DESC);
