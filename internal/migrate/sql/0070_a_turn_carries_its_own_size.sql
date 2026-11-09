-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- Both readers of a subject's history computed each turn's size in the query, by joining
-- turn_message and summing the length of every role and content. Nothing narrowed that join to the
-- subject, because turn_message carries no scope and no subject — it is reached through the
-- observation — so the planner read every message row in the project to size the turns of one
-- person in it.
--
-- Measured on a project of 45,960 turns across 81 subjects: answering for the 5,000-turn subject
-- found its own rows through an index in 92 buffers and then spent 1,699 sizing them, 85% of the
-- 1,982 the whole query touched. The eighty other subjects' messages were read to answer a question
-- about none of them, and the cost of every subject's context rose when a new subject was added.
--
-- Rewriting the query does not fix it, which was measured rather than assumed. A correlated scalar
-- subquery turns the scan into one index probe per turn, which is bounded by the subject — and costs
-- four buffers a probe, so the same answer took 20,283 buffers instead of 1,985. Each shape wins on
-- a different shape of project, which is the definition of a plan that flips under different data.
--
-- So the size lives on the row that is already being read. A turn's messages are written once in the
-- transaction that creates it and their text is never updated afterwards, so a count taken then
-- cannot drift from what it describes.
ALTER TABLE {schema}.observation ADD COLUMN content_characters integer;

-- The same expression both readers used, so the column means exactly what the join meant: the
-- characters of every message's role label and content, which is what a context's budget spends.
UPDATE {schema}.observation o
   SET content_characters = coalesce((SELECT sum(length(m.role) + length(m.content))
                                        FROM {schema}.turn_message m
                                       WHERE m.observation_id = o.observation_id), 0);

-- NOT NULL after the backfill, and with no default. A default would let a write path that forgets
-- this column store a turn claiming a size of zero, which a context would silently value at nothing;
-- without one, such a path is refused by the substrate at the moment it tries. That is also why the
-- count is taken in Go with the turn in hand rather than from the rows afterwards: a NOT NULL column
-- cannot be deferred to the end of a transaction, so a size computed after the insert would need
-- either a nullable column or a default, and both of those are the silent zero.
ALTER TABLE {schema}.observation
    ALTER COLUMN content_characters SET NOT NULL,
    ADD CONSTRAINT observation_content_characters_chk CHECK (content_characters >= 0);

COMMENT ON COLUMN {schema}.observation.content_characters IS
    'The characters of this turn''s message roles and contents, counted when it was written. What a '
    'context budgets, kept here so sizing one subject''s history does not read every message in the '
    'project. Safe to store because a message''s text is never updated after the turn is created.';
