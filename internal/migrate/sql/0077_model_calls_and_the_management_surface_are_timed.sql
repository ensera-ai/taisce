-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- Two more things whose duration is kept: a call to a model, and a request to the management
-- surface.
--
-- ── WHAT THEY ARE FOR ────────────────────────────────────────────────────────────────────────
--
-- A turn's formation is timed as a whole, and a turn makes several model calls. "Formation is
-- slow" could not be narrowed to which pass was slow, and a slow embedding provider showed only as
-- a slow recall. `model_call_duration` is one call, named for its purpose — a closed set the
-- inference package declares — and for whether the provider answered, because a refused connection
-- and a timeout are not samples of how long an answer takes.
--
-- The management surface was not timed at all. It is not on the read path, which is why it was
-- left; an operator waiting on a slow ops centre still had nothing to look at.
-- `management_duration` is one request, named for the operation from that surface's own table, or
-- for the portal as a page or an action — never for a route, whose path carries a project's name.
--
-- ── WHAT DOES NOT CHANGE ─────────────────────────────────────────────────────────────────────
--
-- What a row may carry. The name is still held to the shape that cannot be read as label syntax,
-- and still never a project, a subject or a credential. Only the set of kinds grows.
ALTER TABLE {schema}.measurement DROP CONSTRAINT IF EXISTS measurement_kind_chk;
ALTER TABLE {schema}.measurement ADD CONSTRAINT measurement_kind_chk CHECK (kind IN
    ('operation_duration', 'formation_turn_duration', 'formation_lag',
     'model_call_duration', 'management_duration', 'counter'));
