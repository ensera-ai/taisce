-- ── What happens to a turn that will not form ─────────────────────────────────────────────────
--
--  Extraction can fail, and it fails in ways retrying will not fix: a message the model refuses, a
--  provider that rejects the request every time, a turn so long no budget covers it. Until now a
--  failure left the turn at the head of its backlog with the formed watermark below it, which is
--  the right FAILURE — visible as a scope that stopped advancing rather than as a gap nobody
--  notices — and the wrong OUTCOME, because one such turn freezes a scope's memory forever. Every
--  turn after it is stored, never formed, and the customer sees a memory that quietly stopped
--  growing.
--
--  ── PARKED ADVANCES THE WATERMARK, AND SAYS SO ───────────────────────────────────────────────
--
--  Two answers were available and they say different things to a customer.
--
--  Stalling behind a parked turn keeps `formed` strictly honest — it never claims a turn is in
--  memory that is not — and trades a defect in ONE turn for an outage across the WHOLE scope. That
--  outage is silent: nothing is broken, nothing errors, memory simply stops.
--
--  Advancing past it keeps the scope alive and makes `formed` mean "formed, except the ones we gave
--  up on". That is a smaller lie and it is only a lie if it is hidden — so it is not hidden. The
--  parked count travels with freshness, so a caller and an operator can both see that a number has
--  exceptions and how many, and a non-zero count is something to act on rather than something to
--  discover.
--
--  A parked turn is not a lost turn. The observation and its messages are untouched, so re-forming
--  it is a rebuild rather than a repair, and unparking is one UPDATE.
--
--  ── WHY THE ERROR LIVES HERE AND NOT IN A LOG ────────────────────────────────────────────────
--
--  A provider's error body is text somebody else wrote and can contain the message that was sent to
--  it — which is a customer's words. On the observation it inherits that observation's governance:
--  erasure walks this table, so the error goes when the subject does. In a log file it would be a
--  copy of personal data in a place no erasure can reach and no residual can count.
--
--  It is truncated for the same reason a bundle is capped: an unbounded field fed by a remote system
--  is a way for that system to decide how much of our storage it uses.

ALTER TABLE {schema}.observation
    ADD COLUMN IF NOT EXISTS formation_attempts integer     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS formation_failed_at timestamptz,
    ADD COLUMN IF NOT EXISTS formation_error    text,
    ADD COLUMN IF NOT EXISTS parked_at          timestamptz;

ALTER TABLE {schema}.observation
    ADD CONSTRAINT observation_attempts_chk CHECK (formation_attempts >= 0),
    -- A parked turn has been tried. Parking one that was never attempted would mean giving up on
    -- work nobody started, which is a bug rather than a state.
    ADD CONSTRAINT observation_parked_chk
        CHECK (parked_at IS NULL OR formation_attempts > 0),
    -- Parked and formed are exclusive: giving up on a turn that is already in memory is
    -- contradictory, and a row in that state would make the parked count wrong.
    ADD CONSTRAINT observation_parked_not_formed_chk
        CHECK (parked_at IS NULL OR formed_at IS NULL),
    ADD CONSTRAINT observation_error_length_chk
        CHECK (formation_error IS NULL OR length(formation_error) <= 2000);

-- The backlog is what is neither formed nor given up on. The old index covered `formed_at IS NULL`,
-- which after this change includes parked turns — so a driver would keep finding the turn it already
-- abandoned, forever.
DROP INDEX IF EXISTS {schema}.observation_unformed_idx;
CREATE INDEX observation_backlog_idx ON {schema}.observation (scope, log_offset)
    WHERE formed_at IS NULL AND parked_at IS NULL;

-- Parked turns are asked about by count per scope, by the driver and by an operator's view of the
-- instance. Small and worth its own index because the question is asked on every freshness read.
CREATE INDEX observation_parked_idx ON {schema}.observation (scope)
    WHERE parked_at IS NOT NULL;

COMMENT ON COLUMN {schema}.observation.parked_at IS
    'When the driver stopped trying to form this turn. The observation and its messages are '
    'untouched, so re-forming it is a rebuild rather than a repair. A parked turn does not hold the '
    'formed watermark back, and the count of parked turns travels with freshness so that the '
    'exception to that number is visible rather than hidden.';

COMMENT ON COLUMN {schema}.observation.formation_error IS
    'Why the last attempt failed, truncated. Kept on the observation rather than in a log because a '
    'provider error can contain the message that was sent to it: here it inherits the observation''s '
    'governance and an erasure takes it, and in a log file it would be personal data no sweep '
    'can reach.';
