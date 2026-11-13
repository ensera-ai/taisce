-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- ── The schema says how old a binary may be ─────────────────────────────────────────────────────
--
-- An upgrade is promised one release at a time: a release's schema is one the previous release can
-- serve on, and a check holds every migration to that. Nothing was promised about a process two or
-- more releases old, and nothing stopped one. An operator who upgrades across two releases in one
-- step leaves processes of the old release serving, until the roll replaces them, on a schema that
-- was only ever checked against the release in between — and each of their requests fails in its
-- own way.
--
-- Readiness could not help, by design: a schema newer than a binary is the ordinary state of every
-- upgrade, and refusing it would take every process out of service the moment a migration commits.
--
-- So the schema carries one number: the oldest schema version a binary may have been built for and
-- still serve here. It is the previous release's, written by whoever migrates, after the migrations
-- (the value is the binary's, not this file's, because it moves with every release and a migration
-- that has been released is never edited). A binary compares the version it was built for with
-- that number on the readiness check it already runs. The previous release is at the number and
-- stays ready. Anything older is below it and reports not ready, so it stops taking traffic when
-- the migration commits instead of failing requests until it is replaced.
--
-- It starts at zero, which refuses nobody: a namespace migrated by something that writes no number
-- behaves as it did.
--
-- What it does not do: stop a binary that does not carry the comparison. A release older than the
-- one that introduced it never looks.
CREATE TABLE {schema}.schema_serves (
    singleton     boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    oldest_binary integer NOT NULL DEFAULT 0 CHECK (oldest_binary >= 0)
);
INSERT INTO {schema}.schema_serves (singleton) VALUES (true);
