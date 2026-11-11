-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- A single-valued relation holds one current value per subject, per person who said it.
--
-- ── WHAT WAS WRONG ───────────────────────────────────────────────────────────────────────────
--
-- A named entity is one node per project, keyed on its normalised name, so two people who each
-- mention "Sarah" are writing about one node. The constraint below allowed that node one current
-- value for a single-valued relation, whoever had spoken. So the second person's statement had to
-- close the first person's to be stored at all, and it did: a true fact was given an end by somebody
-- who never saw it. Nothing was refused, so nothing was counted. Nothing was erased, so there was no
-- receipt. The citation on the fact that did the closing was genuine. A recall scoped to the first
-- person then answered as though they had never said it.
--
-- ── WHAT THE CONSTRAINT SAYS NOW ─────────────────────────────────────────────────────────────
--
-- Supersession is something a speaker does to their own earlier statement. The slot that holds one
-- current value is therefore the subject, the relation and the data subject the statement belongs
-- to. Two people who say two different things about one name hold two current values, which is what
-- was said; one person who says two things still holds one, and the second closes the first.
--
-- A statement with no data subject is its own account, written here as the empty string because an
-- exclusion constraint compares with equality and NULL equals nothing — left as NULL, two
-- unattributed statements would never exclude each other and a project that uses no data subjects at
-- all would lose supersession entirely. The write path never stores an empty data subject (it stores
-- NULL), so the empty string cannot collide with a real one.
--
-- ── WHAT THIS DOES NOT DO ────────────────────────────────────────────────────────────────────
--
-- It does not reopen what was already closed. A fact ended by another person's statement before this
-- ran stays ended; it can be found, because the row records which fact superseded it, and the
-- page on temporal history gives the query. Re-deriving the project is what restores
-- it. A migration that reopened intervals would be inventing validity, and the rule here is rebuild
-- rather than repair.
--
-- It does not separate the node. "Sarah" is still one entity for everybody in the project, so a
-- read across the whole project sees both people's statements about her, each with its own source.
--
-- ── WHAT IT COSTS TO RUN ─────────────────────────────────────────────────────────────────────
--
-- Replacing an exclusion constraint rebuilds its index, and the table is locked against writes and
-- reads while it does. On a project with many facts that is an outage of formation and recall for
-- the length of one index build. Every row that satisfied the old constraint satisfies this one — it
-- excludes strictly less — so the build cannot fail on existing data.
ALTER TABLE {schema}.fact DROP CONSTRAINT fact_single_cardinality_excl;
ALTER TABLE {schema}.fact ADD CONSTRAINT fact_single_cardinality_excl
    EXCLUDE USING gist (
        scope             WITH =,
        subject_entity_id WITH =,
        predicate         WITH =,
        (coalesce(data_subject_id, '')) WITH =,
        valid             WITH &&,
        known             WITH &&
    )
    WHERE (cardinality = 'one' AND subject_entity_id IS NOT NULL);
