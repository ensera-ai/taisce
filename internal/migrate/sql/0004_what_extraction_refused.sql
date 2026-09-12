-- ── What extraction refused, kept with the model's own words ──────────────────────────────────
--
--  Two things stop a proposal from becoming a fact, and both of them are silent. (A third was added
--  in 0008: a relation this message has already asserted. The reasoning is there.)
--
--  A relation outside the closed vocabulary is refused because admitting one makes the set
--  advisory. A quote that is not in the message is refused because there is nothing to cite.
--  Neither refusal is a failure — most messages assert nothing, and a model that paraphrases is
--  behaving normally — so neither can be an error, and an error log is not where either belongs.
--
--  ── WHY THIS IS A TABLE AND NOT A COUNTER ────────────────────────────────────────────────────
--
--  The claim behind the vocabulary is that thirty-nine relations cover what conversations assert.
--  The claim behind the span rule is that a located quote is available for the claims worth
--  keeping. Both are assertions until somebody can ask what was refused and read the answer.
--
--  A counter says the rate is eleven percent. It cannot say whether that is one missing relation
--  asserted five hundred times — in which case the vocabulary is wrong and the fix is one row — or
--  five hundred relations asserted once, which is the tail and is noise. Those need opposite
--  responses, and only the rejected text distinguishes them.
--
--  ── WHY THE PREDICATE HAS NO FOREIGN KEY, WHEN THAT IS THE POINT EVERYWHERE ELSE ─────────────
--
--  `fact.predicate` references the vocabulary so an unknown relation cannot be stored. This column
--  holds exactly the relations the vocabulary refused, so a reference here would make the table
--  unable to record the thing it exists to record.
--
--  ── THIS IS PERSONAL DATA AND ERASURE HAS TO SEE IT ──────────────────────────────────────────
--
--  A rejected claim carries a verbatim quote from somebody's message. A refused claim is not a
--  discarded one, and a table of "things we did not store" that turns out to store them is the
--  worst version of this feature. So it registers in `projection_dependency` like every other
--  projection, and the residual count covers it without knowing it exists.

CREATE TABLE {schema}.rejected_claim (
    rejected_claim_id     uuid        PRIMARY KEY,
    scope                 text        NOT NULL,
    -- No foreign key to the observation, matching every other projection: erasure deletes
    -- observations and projections through sweeps that do not coordinate their order, and a
    -- reference would make one fail depending on which ran first — on a path whose whole job is to
    -- complete.
    source_observation_id uuid        NOT NULL,
    source_ordinal        integer     NOT NULL,

    -- What the model tried to say, kept verbatim. Normalising it here would destroy the evidence:
    -- the near-duplicate relations that motivate adding a predicate are visible only in the exact
    -- words that were proposed.
    predicate             text        NOT NULL,
    statement             text        NOT NULL,
    quote                 text        NOT NULL,

    reason                text        NOT NULL,
    extractor_version     text        NOT NULL,
    data_subject_id       text,
    recorded_at           timestamptz NOT NULL DEFAULT now(),

    -- Closed, so a reason no reader knows about cannot be written. Widening it is a
    -- migration and a decision, which is the right weight for changing what "refused" covers.
    CONSTRAINT rejected_claim_reason_chk
        CHECK (reason = ANY (ARRAY['unmapped_relation', 'unlocatable_quote'])),
    CONSTRAINT rejected_claim_ordinal_chk CHECK (source_ordinal >= 0)
);

-- Erasure's sweep. Same shape as every other projection's, because erasure must not need to know
-- what kind of row it is deleting.
CREATE INDEX rejected_claim_subject_idx ON {schema}.rejected_claim (scope, data_subject_id)
    WHERE data_subject_id IS NOT NULL;

COMMENT ON TABLE {schema}.rejected_claim IS
    'Proposals that did not become facts, with the model''s own wording. A relation outside the '
    'vocabulary and a quote that is not in the message are both ordinary outcomes, so neither is an '
    'error — but a rate that cannot be read is a claim that cannot be checked. Registers for '
    'erasure: a refused claim still carries somebody''s words.';
