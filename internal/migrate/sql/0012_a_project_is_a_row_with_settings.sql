-- ── A project becomes a thing rather than a string ────────────────────────────────────────────
--
--  Until now a project was a `scope` value appearing on every row, plus a table partition and a
--  vector index created beside it. Nothing recorded that a project EXISTED: `project list` read the
--  watermark table, so a project created and not yet written to did not appear, and a credential
--  naming a project that was never created was minted happily and reached nothing.
--
--  That is fine for a namespace and useless for a boundary. A project is where data belongs, what a
--  token is attached to, what declares the kinds of memory it keeps, and what carries a retention
--  policy — and none of those can live on a string.
--
--  ── WHY IT LIVES HERE AND NOT IN THE REGISTRY ────────────────────────────────────────────────
--
--  Credentials live in the registry schema, which the memory service cannot read: that is the grant
--  that stops a memory query reading a key. A project's settings are the opposite kind of thing —
--  they are read ON the memory path, by the service, to decide what it does. Putting them behind
--  that fence would mean either the memory service cannot read its own configuration or the fence
--  is weakened for everything else behind it.
--
--  So the division is: the registry holds WHO MAY REACH WHAT, the tenant schema holds WHAT A
--  PROJECT DOES.
--
--  ── SURFACES ARE ADDITIVE, WHICH IS WHY THEY ARE AN ARRAY AND NOT FLAGS ──────────────────────
--
--  A surface is turned on and never off. Turning one off is a destructive act wearing a setting's
--  clothes: the data it already stored is either stranded where nobody can read it, or destroyed by
--  a checkbox. Stopping a surface is an erasure and looks like one, with a receipt and a counted
--  residual.
--
--  Recorded as a set with a trigger that refuses removal, rather than as boolean columns, because a
--  column per surface is a migration per surface and there is no way to express "this one may only
--  ever go from false to true" across a dozen of them.

CREATE TABLE IF NOT EXISTS {schema}.project (
    -- The scope value that appears on every row belonging to this project. It is the identity, not a
    -- surrogate key: every other table already references it by name and a second identifier would be
    -- a second thing to join through on the read path.
    scope          text        PRIMARY KEY,

    -- What a person calls it. Separate from the scope because the scope is an identifier that has to
    -- survive being interpolated into a partition name, and a display name should not have to.
    label          text        NOT NULL,

    -- The kinds of memory this project keeps. Chosen at creation, added to afterwards, never removed.
    surfaces       text[]      NOT NULL DEFAULT '{}',

    -- How long this project keeps what it stores. NULL means indefinitely, which is a policy rather
    -- than an absence of one: an operator has to choose it, and the sweep that reads this treats NULL
    -- as "keep" rather than as "not configured".
    retention      interval,

    created_at     timestamptz NOT NULL DEFAULT now(),
    -- Suspension is not deletion. A suspended project's memory is intact and unreachable, which is
    -- reversible; deleting it is not, and deleting memory without a counted residual is the one thing
    -- this product promises never to do quietly.
    suspended_at   timestamptz,

    CONSTRAINT project_scope_chk CHECK (scope ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT project_label_chk CHECK (length(label) > 0),
    -- A retention of zero or less would delete on write. Whatever an operator meant by it, it was not
    -- that.
    CONSTRAINT project_retention_chk CHECK (retention IS NULL OR retention > interval '0')
);

-- Surfaces may be added and never removed.
--
-- Enforced here rather than in the service because it is the kind of rule a write path forgets: an
-- administrative endpoint written next year that sets the whole array from a form would silently drop
-- whatever the form did not send, and nothing would notice until a caller asked for memory that had
-- stopped being served.
CREATE OR REPLACE FUNCTION {schema}.project_surfaces_only_grow() RETURNS trigger AS $$
BEGIN
    IF NOT (OLD.surfaces <@ NEW.surfaces) THEN
        RAISE EXCEPTION
            'a memory surface cannot be turned off: % would remove %',
            NEW.scope, (SELECT array_agg(s) FROM unnest(OLD.surfaces) s WHERE s <> ALL(NEW.surfaces))
            USING HINT = 'stopping a surface means erasing what it stored, which is an erasure with a receipt';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER project_surfaces_only_grow
    BEFORE UPDATE ON {schema}.project
    FOR EACH ROW EXECUTE FUNCTION {schema}.project_surfaces_only_grow();

COMMENT ON TABLE {schema}.project IS
    'A project: where data belongs, what a token is attached to, which kinds of memory it keeps and '
    'how long it keeps them. Before this, a project was a string appearing on rows — which meant a '
    'project nobody had written to did not exist, and there was nowhere for a setting to live.';

COMMENT ON COLUMN {schema}.project.surfaces IS
    'Chosen at creation, added to afterwards, never removed — enforced by a trigger. Turning a '
    'surface off would either strand the data it stored or destroy it from a settings change; '
    'stopping one is an erasure and produces a receipt like any other.';
