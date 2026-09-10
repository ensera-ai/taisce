-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- The tense rule refuses a past-tense relation because "I used to live in Amman" asserts a relation
-- that is over, and a fact written from it would answer "where do they live" with a place they left.
-- That argument is right for a state and wrong for an event: "voters adopted an amendment" is complete
-- and remains true, and the past tense is the only tense an event is ever reported in. Measured on a
-- news corpus, past-tense refusals outnumbered every other reason six to one, and most were events.
-- The vocabulary now says which relations are events; the extractor admits their past tense.
ALTER TABLE {schema}.predicate
    ADD COLUMN IF NOT EXISTS event boolean NOT NULL DEFAULT false;
UPDATE {schema}.predicate SET event = true
 WHERE predicate IN ('created', 'contributed_to', 'participated_in', 'occurred_on', 'born_in');
COMMENT ON COLUMN {schema}.predicate.event IS
    'True for a relation that reports a completed occurrence and stays true afterwards, so its past '
    'tense is admitted. False for a state, whose past tense means it ended and is refused as not '
    'current.';
