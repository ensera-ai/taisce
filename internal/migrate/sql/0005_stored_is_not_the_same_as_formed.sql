-- ── Stored is not the same as formed, and freshness is about the second one ────────────────────
--
--  Forming a turn means asking a model what each of its messages asserts. That takes seconds per
--  message against a hosted model, so it cannot happen while a caller waits: an agent's turn would
--  block on its own memory write, and the product would be slower than having no memory at all.
--
--  So a turn is stored immediately and formed afterwards, and the two are different moments. This
--  migration is what makes the second one answerable.
--
--  ── THE WATERMARK MEANT THE WRONG THING ──────────────────────────────────────────────────────
--
--  `watermark.log_offset` advances when a turn is APPENDED. It was written to answer "is what I
--  just wrote visible yet", and it does not: it says the turn was stored, which the caller already
--  knows because the append returned. Between storing and forming, a recall does not see the turn's
--  facts, and the number a caller would check to find that out says everything is current.
--
--  Both numbers are wanted and they are different questions, so there are two columns rather than a
--  redefinition of one. "We have your turn" is what an append confirms. "Your turn is in memory" is
--  what a recall depends on, and it is the one a customer is actually asking about.
--
--  ── WHY NULL AND NOT A SENTINEL ──────────────────────────────────────────────────────────────
--
--  `formed_offset` is NULL until something in the scope has formed, rather than -1. Offsets start
--  at zero, so any sentinel is a value in the same domain as the data, and every comparison then
--  carries a clause to exclude it — which one query eventually forgets.

ALTER TABLE {schema}.observation
    ADD COLUMN IF NOT EXISTS formed_at timestamptz;

COMMENT ON COLUMN {schema}.observation.formed_at IS
    'When extraction ran over this turn''s messages. NULL means the turn is stored and its facts do '
    'not exist yet. Set once, and never cleared: re-forming is a rebuild, which derives from the '
    'log rather than repairing rows in place.';

-- The backlog query, and the only one formation issues to find work.
--
-- Partial on the unformed rows, so it holds the backlog rather than the history: it shrinks back to
-- nothing as formation catches up instead of growing with every turn ever stored. Without the
-- predicate this index would be the largest in the schema and would answer one question.
CREATE INDEX observation_unformed_idx
    ON {schema}.observation (scope, log_offset)
    WHERE formed_at IS NULL;

-- ── The second watermark ──────────────────────────────────────────────────────────────────────
--
-- The highest offset whose turn is formed AND below which nothing is unformed. The second half is
-- what makes it safe to read: without it, one slow turn in the middle of a burst would let the
-- number run ahead of a gap, and a caller told "current to 40" would be missing turn 12.
--
-- Which is why it is derived rather than incremented. An increment per completion is correct only
-- if completions happen in order, and they do not — two turns forming concurrently finish in
-- whichever order their model calls return.
ALTER TABLE {schema}.watermark
    ADD COLUMN IF NOT EXISTS formed_offset bigint;

COMMENT ON COLUMN {schema}.watermark.formed_offset IS
    'Highest log offset in this scope whose turn is formed, with nothing unformed below it. NULL '
    'until the scope''s first turn forms. This is the number a caller checks to know whether a '
    'recall would see what they just wrote; log_offset only says it was stored.';
