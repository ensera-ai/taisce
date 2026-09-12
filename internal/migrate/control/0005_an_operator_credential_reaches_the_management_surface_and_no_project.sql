-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- A credential is one of two kinds, and the kind is a column the registry holds, never a claim the
-- token makes. A project credential reaches one project's memory and nothing an operator
-- does. An operator credential reaches the management surface and no project's memory: it names
-- no project, which the constraint below makes the same statement as its kind, so a row cannot be
-- both or neither.
--
-- Least privilege in the substrate: the memory door resolves only rows of kind 'project', the
-- management door only rows of kind 'operator', and each is refused at the other's door with the
-- one answer a stranger gets.

ALTER TABLE {schema}.credential
    ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'project'
        CHECK (kind IN ('project', 'operator'));

-- An operator credential has no project; a project credential always has one. The existing project
-- constraint says project <> '' for every row; it is replaced by one that ties emptiness to kind.
ALTER TABLE {schema}.credential DROP CONSTRAINT IF EXISTS credential_project_chk;
ALTER TABLE {schema}.credential
    ADD CONSTRAINT credential_project_chk CHECK ((kind = 'project') = (project <> ''));

COMMENT ON COLUMN {schema}.credential.kind IS
    'project: reaches one project''s memory routes. operator: reaches the management surface and no '
    'memory. Read from the registry at every request; the token carries no claim about it.';
