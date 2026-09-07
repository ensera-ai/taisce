-- ── Credentials, inside the fence that was built before them ──────────────────────────────────
--
--  A caller reaching the memory service presents a credential. This is where the credential lives,
--  which is deliberately not where the memory lives: the data-plane identity has no privilege in
--  this schema at all, so a memory query cannot read a key, cannot widen its own authorisation, and
--  cannot enumerate who else holds one — not because a predicate excludes it, but because the grant
--  does not exist.
--
--  ── WHAT A CREDENTIAL RESOLVES TO, AND WHAT IT DELIBERATELY DOES NOT ─────────────────────────
--
--  It resolves to a set of project scopes, and to nothing else. There is one tenant, so there is no
--  tenant to resolve; there is no commercial line, so there are no entitlements to carry. A
--  credential that resolved to more than the projects it may reach would be a place for authority to
--  accumulate.
--
--  The set is authoritative in both directions: a read sees exactly these scopes, and a write is
--  refused for a scope outside them. An empty set is a valid state and it means the credential can
--  reach nothing — which is the correct behaviour for a key whose grants were removed, and it is why
--  the column is NOT NULL with a default of empty rather than nullable.
--
--  ── WHY THE TOKEN IS NOT HERE ────────────────────────────────────────────────────────────────
--
--  Only its SHA-256 digest is. A token is high-entropy random bytes rather than a password, so the
--  attack a slow hash defends against — guessing — is not available: there is nothing to guess at
--  256 bits. What a digest defends against is the read that should never have happened, and it does
--  that as well as any construction while costing one hash per request instead of a deliberate
--  hundred milliseconds on a path every call pays.
--
--  The prefix is stored in the clear so a person can tell two keys apart in a list without the
--  system being able to reconstruct either.

CREATE TABLE IF NOT EXISTS {schema}.credential (
    credential_id  uuid        PRIMARY KEY,
    -- The digest is the lookup key. Unique because two credentials with one digest is one
    -- credential that two people believe is theirs.
    token_digest   bytea       NOT NULL UNIQUE,
    -- Enough of the token to recognise it, never enough to use it.
    token_prefix   text        NOT NULL,
    name           text        NOT NULL,
    -- The projects this credential may reach. Empty means nothing, and that is a usable state.
    scopes         text[]      NOT NULL DEFAULT '{}',
    created_at     timestamptz NOT NULL DEFAULT now(),
    -- Revocation is a timestamp rather than a delete, so that a key that was used can still be
    -- named by whatever recorded the use of it.
    revoked_at     timestamptz,

    CONSTRAINT credential_prefix_chk CHECK (length(token_prefix) BETWEEN 4 AND 16),
    CONSTRAINT credential_name_chk   CHECK (length(name) > 0)
);

-- Resolution happens on every authenticated request, so it is one index lookup on the digest.
CREATE INDEX IF NOT EXISTS credential_live_idx ON {schema}.credential (token_digest)
    WHERE revoked_at IS NULL;

COMMENT ON TABLE {schema}.credential IS
    'What a caller presents, and the project scopes it resolves to. Holds a digest and never a '
    'token. Lives in the registry schema because the memory service must not be able to read it: '
    'the data-plane role has no privilege here, so a memory query cannot widen its own authority.';
