-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- A third kind of credential: one that reads the instance's numbers and opens nothing else.
--
-- ── WHAT IT IS FOR ───────────────────────────────────────────────────────────────────────────
--
-- The scrape is on the management surface, and the management surface opened for one kind of
-- credential: the operator's, which creates and suspends projects, issues and revokes credentials
-- and seals the ledger. So the system that scraped had to hold that credential — in a scrape
-- configuration, or a Secret in the monitoring namespace, readable by whoever administers
-- monitoring. The credential that can mint a key for any project was held by the system least
-- likely to be treated as holding one.
--
-- An observer is what a monitoring system is given instead. It opens the metrics route and no
-- other: not a management operation, not the portal, not a memory route.
--
-- ── WHY A KIND AND NOT AN ACCESS MODE ON THE OPERATOR ────────────────────────────────────────
--
-- Each door accepts only its own kind, and a wrong kind is refused with the answer a stranger gets.
-- A mode on the operator kind would be a column every management route has to remember to read,
-- and a route that forgot would open for a credential issued to scrape. A kind is checked at the
-- door, once, by the function that decides whether there is a grant at all.
--
-- ── THE CONSTRAINT THAT DOES NOT CHANGE ──────────────────────────────────────────────────────
--
-- `credential_project_chk` already says a credential names a project exactly when its kind is
-- `project`. An observer names none, like an operator, and the constraint holds it to that without
-- being touched.
ALTER TABLE {schema}.credential DROP CONSTRAINT IF EXISTS credential_kind_check;
ALTER TABLE {schema}.credential
    ADD CONSTRAINT credential_kind_check CHECK (kind IN ('project', 'operator', 'observer'));

COMMENT ON COLUMN {schema}.credential.kind IS
    'project: reaches one project''s memory routes. operator: reaches the management surface and no '
    'memory. observer: reaches the instance''s metrics and nothing else. Read from the registry at '
    'every request; the token carries no claim about it.';
