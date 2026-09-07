-- ── Sharing saves an identity, and must not save text ─────────────────────────────────────────
--
--  An erasure keeps a projection that another subject also registered. `Ensera` is registered by
--  everyone who mentioned it, and removing it because one of them left would take a node out of
--  everybody else's graph — so the delete predicate excludes anything with a registration belonging
--  to somebody other than the departing subject, and the residual is counted with the same predicate
--  so that a kept row is correctly not a residual.
--
--  That rule is right for every kind declared so far, and it is applied to all of them, which is the
--  defect this migration closes before the kind it would be wrong for exists.
--
--  ── WHY ONE RULE CANNOT COVER BOTH ───────────────────────────────────────────────────────────
--
--  An entity is a shared IDENTITY. The row means the same thing to everybody who registered it: it
--  says a thing exists and what it is called. Nothing about it belongs to one contributor, so keeping
--  it when one leaves discloses nothing about them.
--
--  A community report is shared TEXT. It is prose written from what several people said, and it
--  contains what each of them said. Keeping it because somebody else also contributed is exactly the
--  leak — the departing subject's material stays, in a row an erasure walked past and counted as
--  correctly retained.
--
--  The same is true of anything else that aggregates content: a summary, a digest, a description
--  written from several sources. So the property belongs to the KIND rather than to the eraser, and
--  it is declared beside the table name and the id column, where a kind added later cannot avoid
--  answering the question.
--
--  ── WHY THE DEFAULT IS THE SAFE ONE ──────────────────────────────────────────────────────────
--
--  `false` — sharing does not save it. A kind added later by somebody who did not read this file gets
--  the behaviour that deletes too much rather than the one that leaks, because an over-deletion is
--  visible to the person who lost data and a leak is visible to nobody. Rule 8: every default is
--  chosen for what happens when something upstream forgets.
--
--  The four kinds that exist are set explicitly to `true`, which is the behaviour they already had,
--  so this migration changes nothing about them. What it changes is that the fifth kind has to say.

ALTER TABLE {schema}.projection_kind
    ADD COLUMN IF NOT EXISTS survives_sharing boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN {schema}.projection_kind.survives_sharing IS
    'Whether a projection of this kind is KEPT when another data subject also registered it. True for '
    'a shared identity, whose row means the same thing to everybody who registered it. False for '
    'anything holding text drawn from several people, where keeping it because somebody else also '
    'contributed leaves the departing subject''s words in a row the erasure counted as retained. '
    'Defaults to false: a kind added by somebody who did not read this deletes too much rather than '
    'leaking, because over-deletion is visible to the person who lost data and a leak is visible to '
    'nobody.';

-- The four that exist keep the behaviour they have. Stated one at a time rather than as an UPDATE
-- with no predicate, so that adding a kind between this migration and the next does not silently
-- acquire the permissive setting from a statement that was written before it existed.
UPDATE {schema}.projection_kind SET survives_sharing = true WHERE kind = 'chunk';
UPDATE {schema}.projection_kind SET survives_sharing = true WHERE kind = 'fact';
UPDATE {schema}.projection_kind SET survives_sharing = true WHERE kind = 'entity';
UPDATE {schema}.projection_kind SET survives_sharing = true WHERE kind = 'rejected_claim';

-- ── A note on the three that are not entities ────────────────────────────────────────────────
--
--  `chunk`, `fact` and `rejected_claim` each hold one person's words and are registered to the one
--  observation that produced them, so in practice no second subject ever registers them and the
--  setting is unreachable for them either way. They are set to `true` because that is what they did
--  yesterday and this migration is not the place to change behaviour that nothing has argued about.
--  If one of them ever becomes shareable, the question this column asks is one it will have to answer.
