-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- ── A delivery's failure names no address ─────────────────────────────────────────────────────
--
-- `last_error` was written from the transport's own error text, and that text carries the address
-- and port a destination led to — `dial tcp 10.0.4.7:5432: connect: connection refused`. The
-- delivery list is readable by any credential in the project, read-only ones included. So the column
-- told anybody holding a key which internal addresses a registered destination reached: a map of the
-- deployment's network, drawn by its own notifications.
--
-- The sender now records a category instead, and this makes a category the only thing the column
-- can hold. A rule in the sender is kept by the code written today; a constraint is kept by every
-- write, including one added later by somebody who never read this.
--
-- ── ROWS WRITTEN BEFORE ───────────────────────────────────────────────────────────────────────
--
-- Rewritten, not left: a raw string already stored is the same disclosure as one written tomorrow.
-- They cannot honestly be categorised after the fact — the text was never a contract — so they say
-- that they were not. `last_status` still holds what the destination answered, where it answered.
-- Adding the constraint validates every existing row, so a rewrite that missed one fails this
-- migration instead of leaving it behind.
--
-- ── A NEW CATEGORY ────────────────────────────────────────────────────────────────────────────
--
-- Needs this constraint widened in the same change. Until it is, the store refuses the write and the
-- delivery loop reports the error — loud, which is the intended failure for a category nobody has
-- decided is safe to show.
UPDATE {schema}.notification_delivery
   SET last_error = 'unrecorded'
 WHERE last_error IS NOT NULL
   AND last_error NOT IN ('destination not permitted', 'address not permitted', 'redirect not followed',
                          'timeout', 'connection refused', 'tls failure', 'network error', 'unrecorded')
   AND last_error !~ '^destination (answered|refused with) [0-9]{3}$';

ALTER TABLE {schema}.notification_delivery
    ADD CONSTRAINT notification_delivery_last_error_category_chk CHECK (
           last_error IS NULL
        OR last_error IN ('destination not permitted', 'address not permitted', 'redirect not followed',
                          'timeout', 'connection refused', 'tls failure', 'network error', 'unrecorded')
        OR last_error ~ '^destination (answered|refused with) [0-9]{3}$'
    );
