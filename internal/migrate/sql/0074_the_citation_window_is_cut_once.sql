-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- The words around a quote were cut out of the whole message on every read of every fact.
--
-- The evidence join did `substring(convert_to(m.content,'UTF8') from … for …)` per returned row, to
-- keep three hundred and twenty bytes of a message that can be a hundred kilobytes. That converts
-- the entire message to bytes, once per fact per read, and the cost is a function of how long the
-- message was rather than of how much context is shown. It is on the read path, so every caller pays
-- it on every call.
--
-- Quote, span and message content are immutable once stored, so the window cannot drift: it is the
-- same bytes every read recomputed. It is cut here instead, once, when the evidence row is written.
--
-- The read still joins the message, and selects no column of its text. The joins were never only
-- about the words: `src.scope = r.scope` is the cross-project guard, and the message's presence is
-- what says the window is still honest. A fact whose message is gone has a quote it cites and three
-- hundred bytes of surrounding words nobody cited, and only the first should survive the message. So
-- the window is returned only when the message is still there, in this project — the same answer as
-- before, at the cost of an index lookup instead of a detoasted message.
--
-- # Why a trigger rather than the three insert statements
--
-- Three paths write evidence today — an assertion, a receipt restored by recovery, and support
-- rebuilt for a fact — and a fourth will be written by somebody who has never read this file. A
-- window each statement has to remember to compute is one the next statement forgets, and forgetting
-- it is silent: the fact comes back with its quote and no words around it, which is exactly what an
-- erased message looks like. The trigger is the only place the arithmetic exists, so every path that
-- inserts evidence gets it and no path can get it differently.
--
-- # What the width is, and why it is here as a number
--
-- One hundred and sixty bytes either side, which is `domain.ContextWindow`. That is a second
-- statement of one number, and the honest answer to it is a test rather than a comment:
-- TestTheStoredCitationWindowIsTheWidthTheDomainDeclares reads a stored window back and holds it
-- against the constant, so the two cannot drift without the suite saying so.
--
-- # Governance, which is the part that would normally sink this
--
-- These three columns hold a copy of message text, so an erasure has to reach them. The reason they
-- are columns on `fact_evidence` rather than a table of their own is that `fact_evidence` is not a
-- new place text lives: its rows are registered through `projection_dependency`, they cascade away
-- with the fact, the eraser deletes any whose observation it erased, and the retention sweep deletes
-- them for an expired turn before it deletes the turn's messages. So a column on that row is covered
-- by the sweep that already covers the quote beside it, and a separate table would have been a new
-- kind with a new residual key to prove.
--
-- That is not an ordering anybody has to remember, either. `evidence_message_fk` references
-- `turn_message` with no delete action, so the substrate refuses to remove a message while an
-- evidence row cites it: a statement that tried to orphan a stored window fails, and the only way to
-- remove the message is to remove the evidence first, which removes the window with it.
--
-- And where that constraint has been dropped — damaged data, which the read already has a branch
-- for — the message join is what withholds the window, so the words still do not outlive the
-- message. Both halves are held by tests that predate this change:
-- TestAFactWhoseMessageIsGoneKeepsItsQuoteWithNoContext and
-- TestContextIsNotReadFromAnotherProjectsMessage. Both caught this change's first attempt, which
-- removed the joins along with the cut.
--
-- That is also the regression this had to close rather than introduce. Before this change a fact
-- whose message was gone came back with its quote and an empty context, through a left join. A
-- stored window could have outlived the message instead, and a quote outliving the message it came
-- from is not worth a faster read. It does not, and
-- TestAStoredCitationWindowIsErasedWithTheEvidenceItBelongsTo holds it against both paths.
--
-- The window is NULL when the message is not there to cut, which is what a read already reports as a
-- degraded citation context — so a fact whose message was erased before this migration behaves
-- exactly as it did.
--
-- # What this does not change
--
-- Citation resolution. That path re-derives the quote and the window from the message on purpose:
-- its job is to prove the stored span still resolves against the stored bytes, and a stored copy
-- would be the thing under test answering for itself. Its cost is paid once per inspection, by a
-- caller who asked for exactly that, and not per fact per recall.
ALTER TABLE {schema}.fact_evidence
    ADD COLUMN citation_window bytea,
    ADD COLUMN citation_window_start integer CHECK (citation_window_start >= 0),
    ADD COLUMN citation_message_bytes integer CHECK (citation_message_bytes >= 0);

CREATE FUNCTION {schema}.cut_citation_window() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
DECLARE
    said text;
    window_start integer;
BEGIN
    SELECT m.content INTO said
      FROM {schema}.turn_message m
     WHERE m.observation_id = NEW.source_observation_id
       AND m.ordinal = NEW.source_ordinal;
    IF said IS NULL THEN
        -- No message to cut from. The quote and the span are still what they were, and a citation
        -- with no words around it is a receipt that lost legibility rather than validity, which the
        -- read already reports.
        NEW.citation_window := NULL;
        NEW.citation_window_start := NULL;
        NEW.citation_message_bytes := NULL;
        RETURN NEW;
    END IF;
    window_start := greatest(0, NEW.byte_start - 160);
    NEW.citation_window := substring(convert_to(said, 'UTF8')
        FROM window_start + 1
        FOR (NEW.byte_end + 160) - window_start);
    NEW.citation_window_start := window_start;
    NEW.citation_message_bytes := octet_length(said);
    RETURN NEW;
END;
$$;

CREATE TRIGGER fact_evidence_citation_window BEFORE INSERT ON {schema}.fact_evidence
    FOR EACH ROW EXECUTE FUNCTION {schema}.cut_citation_window();
REVOKE ALL ON FUNCTION {schema}.cut_citation_window() FROM PUBLIC;

COMMENT ON COLUMN {schema}.fact_evidence.citation_window IS
    'The bytes around the quote, cut once when this row was written rather than on every read. NULL '
    'when the message was not there to cut from, which a read reports as a degraded citation '
    'context. A copy of message text, erased with this row.';

-- Every row already written. The read becomes unconditional — it selects these columns and computes
-- nothing — so a row left behind would lose the words around its quote, which is memory loss rather
-- than a slower read. One pass, doing per row what the read was doing per row per read.
UPDATE {schema}.fact_evidence e
   SET citation_window = substring(convert_to(m.content, 'UTF8')
                                   FROM greatest(0, e.byte_start - 160) + 1
                                   FOR (e.byte_end + 160) - greatest(0, e.byte_start - 160)),
       citation_window_start = greatest(0, e.byte_start - 160),
       citation_message_bytes = octet_length(m.content)
  FROM {schema}.turn_message m
 WHERE m.observation_id = e.source_observation_id
   AND m.ordinal = e.source_ordinal;
