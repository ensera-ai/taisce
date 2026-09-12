-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- A report is a model's prose about a subject, written under a particular prompt. Until now nothing
-- recorded which — so a prompt edit, which rule 14 treats as a change to behaviour, could not reach
-- a single report already stored. The invalidation trigger fires on a changed FACT; a changed
-- WRITER was invisible.
--
-- The column holds a digest of what wrote it: the origin, the model, the compiled code that composes
-- the request and parses the reply, and the prompt file itself. A digest rather than a version field,
-- because a version field is a number somebody must remember to change and the edit that matters most
-- is the small change of wording made while tuning — exactly the one where they will not.
--
-- Empty for reports written before this, which is the truthful value: what wrote them is not
-- recorded and cannot be reconstructed. They are treated as written by an unknown writer, which is
-- to say they are rewritten once and then carry an identity like everything after them.

ALTER TABLE {schema}.community_report
    ADD COLUMN written_by text NOT NULL DEFAULT ''
        CHECK (octet_length(written_by) <= 128);

-- The subject pass asks for communities needing a report, and now that includes one whose report was
-- written by something else. Not partial: what counts as "something else" is the writer identity of
-- the running deployment, which changes with it, and an index predicate has to be immutable.
CREATE INDEX community_report_writer_idx ON {schema}.community_report (scope, written_by);
