-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- ── A delivery is in its endpoint's project, and the schema says so ─────────────────────────────
--
-- A delivery carries a project and points at an endpoint. Its foreign key was on the endpoint's
-- identifier alone, so nothing in the database said the two agree: a delivery filed under one
-- project and pointed at another project's endpoint was a row the schema accepted.
--
-- A delivery is the one row that leaves the instance — it is posted to the endpoint's address. One
-- that crossed projects would tell an endpoint's owner about another project's formation: an
-- offset and counts, no memory, and still somebody else's. The statement that writes a delivery
-- takes both values from the same endpoint row, so no such row has been written. That is a
-- convention held by one statement. A boundary belongs where a statement cannot get around it.
--
-- So the key that links them carries the project. An endpoint is unique by project and identifier
-- (it is already unique by identifier; this is the same fact, stated in the form a foreign key
-- can reference), and a delivery references both columns.
--
-- If any delivery already disagrees with its endpoint, this migration fails and names the
-- constraint. That is deliberate: such a row is the defect, and removing it silently would hide
-- that it had existed.
--
-- The link on the identifier alone is dropped, because the new one implies it and two keys that
-- say overlapping things are two things to keep true.
ALTER TABLE {schema}.notification_endpoint
    ADD CONSTRAINT notification_endpoint_scope_id_uniq UNIQUE (scope, endpoint_id);

ALTER TABLE {schema}.notification_delivery
    ADD CONSTRAINT notification_delivery_endpoint_in_project_fk
        FOREIGN KEY (scope, endpoint_id)
        REFERENCES {schema}.notification_endpoint (scope, endpoint_id) ON DELETE CASCADE;

ALTER TABLE {schema}.notification_delivery
    DROP CONSTRAINT notification_delivery_endpoint_id_fkey;
