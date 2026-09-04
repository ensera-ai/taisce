-- ── Evidence names the message it indexes, not just the turn it came from ─────────────────────
--
--  `fact_evidence` recorded which OBSERVATION a span came from. An observation is a whole turn, and
--  a span indexes ONE MESSAGE — which is the rule the extractor enforces and the first migration
--  states. The ordinal that connects the two was in the claim and was never written down.
--
--  So resolving a citation meant joining evidence to the turn's messages with nothing to choose
--  between them. Every message of the turn matched, and the span was then taken against whichever
--  one the join returned.
--
--  That failure is silent by construction. A byte offset into the wrong message does not error: it
--  returns a plausible fragment of a different sentence and attributes it to the fact as its
--  evidence. The rows look correct, the quote column looks correct, and the two disagree only if
--  somebody reads them side by side.
--
--  ── WHY THE ORDINAL IS IN THE KEY ────────────────────────────────────────────────────────────
--
--  One fact can be evidenced from two messages — the same claim restated later in the turn — and
--  two spans starting at the same offset in different messages are different evidence. Without the
--  ordinal in the key the second one silently replaces the first, which loses a citation rather
--  than duplicating one.
--
--  ── WHY THIS FAILS LOUDLY ON A TABLE THAT ALREADY HAS ROWS ───────────────────────────────────
--
--  NOT NULL with no default. There is no value that would be right for an existing row: the ordinal
--  it should carry is exactly the thing that was never recorded, and a default of zero would state
--  that every historical citation came from a turn's first message. That is a claim, and it would
--  be wrong for most of them.

ALTER TABLE {schema}.fact_evidence
    DROP CONSTRAINT IF EXISTS fact_evidence_pkey;

ALTER TABLE {schema}.fact_evidence
    ADD COLUMN source_ordinal integer NOT NULL;

ALTER TABLE {schema}.fact_evidence
    ADD CONSTRAINT fact_evidence_ordinal_chk CHECK (source_ordinal >= 0);

ALTER TABLE {schema}.fact_evidence
    ADD PRIMARY KEY (fact_id, source_observation_id, source_ordinal, byte_start);

COMMENT ON COLUMN {schema}.fact_evidence.source_ordinal IS
    'Ordinal of the turn_message this span indexes. Byte offsets are meaningless without it: taken '
    'against another message of the same turn they resolve to a plausible fragment of the wrong '
    'sentence rather than to nothing.';
