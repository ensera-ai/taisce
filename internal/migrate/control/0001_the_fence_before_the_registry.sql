-- ── The fence, built before there is anything behind it ────────────────────────────────────────
--
--  The registry — tenants, projects, principals, credentials, plans — does not exist yet. This
--  migration creates the place it will live and the boundary around it, and nothing else.
--
--  ── WHY THE FENCE COMES FIRST ────────────────────────────────────────────────────────────────
--
--  A registry built in the open and fenced afterwards is a registry that was readable by the
--  memory service for however long that took, and every query written in the meantime was written
--  against a world where that was allowed. Moving a table behind a boundary later means finding
--  every one of those and being sure. Creating the boundary first means the registry lands inside
--  it and the question never arises.
--
--  This is the same reasoning that put the speaker on the observation from the first migration:
--  a constraint that arrives late has a history it did not apply to.
--
--  ── WHAT THIS SCHEMA IS FOR, AND WHAT IT MUST NEVER HOLD ─────────────────────────────────────
--
--  It holds the things that must be asked across every customer at once: which tenants exist, who
--  may reach them, what they are entitled to, what they are paying. Answering any of those means
--  enumerating customers.
--
--  The memory service must never be able to do that. It serves one tenant per statement, and its
--  whole boundary is that another tenant's name is not in the statement it is running. A component
--  that can list every customer is one bad predicate away from crossing it.
--
--  So this schema holds NO memory, NO fact, NO message and NO observation. The two never join,
--  and there is nothing here for a memory query to reach even if one tried.

CREATE SCHEMA IF NOT EXISTS {schema};

-- Its own version table, tracked separately from a tenant's.
--
-- The two migration sets advance independently: a tenant is migrated when its schema is brought up
-- to date, which happens per tenant and at a moment somebody chose. The registry is migrated once.
-- One version number over both would make the second wait on the first, or claim to be at a version
-- half its tables had never seen.
CREATE TABLE IF NOT EXISTS {schema}.schema_migration (
    version    integer     PRIMARY KEY,
    name       text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON SCHEMA {schema} IS
    'The control plane. Holds what must be asked across every customer at once — tenants, '
    'principals, credentials, entitlements — and never holds a memory. The memory service has no '
    'privilege here, so a component that serves one tenant cannot enumerate the rest.';
