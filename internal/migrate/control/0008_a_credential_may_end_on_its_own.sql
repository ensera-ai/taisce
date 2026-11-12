-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- A credential may carry the moment it stops working.
--
-- ── WHAT IT IS FOR ───────────────────────────────────────────────────────────────────────────
--
-- A credential lived until somebody revoked it. Revocation needs somebody to know the credential
-- should end: that it leaked, that its holder left, that the system it was pasted into is gone. The
-- credentials that most need ending are the ones nobody is thinking about, and for those the only
-- thing that ends them is a date written down while somebody still was.
--
-- ── WHY IT IS A COLUMN THE RESOLVER'S QUERY READS ────────────────────────────────────────────
--
-- A credential stops resolving in one place: the WHERE clause that finds it. `revoked_at IS NULL`
-- is there, and the expiry is beside it, compared against the database's clock rather than the
-- clock of whichever replica asked. A check made afterwards, in the caller, would be one each door
-- has to remember; in the query, an expired credential is a row the query does not return, and the
-- caller cannot tell it from a token nobody issued — which is the answer it is supposed to give.
--
-- ── NULL MEANS NO DATE, AND THAT IS WHAT EVERY EXISTING ROW GETS ─────────────────────────────
--
-- Nothing here ends a credential that is in use. A migration that stamped a date on rows already
-- issued would choose, for every deployment at once, the day its applications stop authenticating.
-- What changes is what is issued from now on, and that is decided where a credential is issued.
ALTER TABLE {schema}.credential ADD COLUMN IF NOT EXISTS expires_at timestamptz;

-- An expiry at or before issuance is a credential that never worked, and the holder would learn it
-- from a refusal indistinguishable from a typing mistake.
ALTER TABLE {schema}.credential DROP CONSTRAINT IF EXISTS credential_expires_after_issue_chk;
ALTER TABLE {schema}.credential
    ADD CONSTRAINT credential_expires_after_issue_chk CHECK (expires_at IS NULL OR expires_at > created_at);

COMMENT ON COLUMN {schema}.credential.expires_at IS
    'When this credential stops resolving, by the database''s clock. NULL: it has no date and ends '
    'only by revocation. Read in the same predicate as revoked_at, so an expired credential is '
    'answered exactly as one nobody issued.';
