-- ── One credential, one project ───────────────────────────────────────────────────────────────
--
--  A credential held a SET of project scopes, and a caller named the project it wanted in the
--  request. Both go.
--
--  ── WHY ONE AND NOT MANY ─────────────────────────────────────────────────────────────────────
--
--  With a set, the reach of a leaked token is a list somebody has to read to know. With one project
--  it is a name. Every audit row becomes unambiguous about which project an operation touched
--  without a second field that could disagree. And the direction is the safe one: widening a
--  credential later breaks nothing, narrowing one after clients depend on the width breaks all of
--  them — so it is done now rather than after the first adapter ships.
--
--  What it costs is the cross-project bundle: an agent working across two projects holds two
--  credentials rather than sending a different string.
--
--  ── WHY THERE IS NO FOREIGN KEY, WHICH IS USUALLY THE POINT ──────────────────────────────────
--
--  The project table lives in the TENANT schema and this table lives in the registry, behind a fence
--  the memory service cannot cross. A foreign key across that boundary would require the registry
--  role to read the tenant's tables, which is the grant D29 exists to withhold.
--
--  So the reference is validated where both are visible — by the operator command that mints a
--  credential, which holds a connection that can see both — rather than by a constraint. That is a
--  weaker guarantee than this schema usually accepts, and it is the price of the plane separation
--  being real. What it protects against is a typo becoming a credential that authenticates and
--  reaches nothing.

ALTER TABLE {schema}.credential
    ADD COLUMN IF NOT EXISTS project text;

-- Existing credentials take their first scope, deliberately rather than silently: there are none in
-- any deployment yet, and a migration that quietly picked one of several would be a credential whose
-- reach changed without anybody deciding.
UPDATE {schema}.credential
   SET project = scopes[1]
 WHERE project IS NULL AND array_length(scopes, 1) >= 1;

-- One with no scopes at all reached nothing and still does. Named so it is visible rather than
-- becoming a NULL that the NOT NULL below would refuse.
UPDATE {schema}.credential
   SET project = '', revoked_at = coalesce(revoked_at, now())
 WHERE project IS NULL;

ALTER TABLE {schema}.credential
    ALTER COLUMN project SET NOT NULL,
    DROP COLUMN scopes;

COMMENT ON COLUMN {schema}.credential.project IS
    'The one project this credential reaches. Not a set: the reach of a leaked token is a name '
    'rather than a list, and every audit row names its project without a second field that could '
    'disagree. No foreign key, because the project table is behind the plane boundary this schema '
    'exists on the other side of — the reference is validated when a credential is minted.';
