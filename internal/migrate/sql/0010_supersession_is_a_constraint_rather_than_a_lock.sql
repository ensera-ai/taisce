-- ── A fact that stops being true ──────────────────────────────────────────────────────────────
--
--  Eleven of the thirty-nine relations are single-cardinality: a person lives in one place, not in
--  every place they have ever lived. Until now nothing said so. Two `lives_in` facts for one person,
--  both current, both cited, is a memory that answers "where do they live" with a list — and every
--  reader downstream has to guess which one is meant.
--
--  ── WHY A CONSTRAINT AND NOT A LOCK ──────────────────────────────────────────────────────────
--
--  The application answer is a lock per (subject, predicate) taken on the write path. It is correct
--  today and it is correct only while every write path remembers to take it, including the one
--  written next year by somebody who has not read this file. It is also the hottest contention point
--  in formation, taken on every assertion to protect a case that is rare.
--
--  A constraint is correct because the database will not store the violation. There is no path around
--  it and nothing to remember.
--
--  ── WHY EXCLUDE AND NOT UNIQUE ───────────────────────────────────────────────────────────────
--
--  The invariant is about time, not about rows. Two `lives_in` facts for one person are perfectly
--  legal when their valid intervals do not overlap — that is exactly what a move looks like, and it
--  is the history the product exists to keep. A unique index cannot express "no two overlapping
--  intervals"; an exclusion constraint over a range with `&&` is precisely that statement.
--
--  ── WHY CARDINALITY IS COPIED ONTO THE FACT ──────────────────────────────────────────────────
--
--  An exclusion constraint cannot consult another table, so it cannot ask the vocabulary whether this
--  predicate is single-cardinality. The alternatives were a hardcoded list of predicate names in the
--  WHERE clause — a second definition of a closed set, which is the thing this schema refuses
--  everywhere else — or a trigger, which is application logic wearing a constraint's clothes.
--
--  So the fact carries the cardinality, and a COMPOSITE FOREIGN KEY makes it impossible for that copy
--  to disagree with the vocabulary: (predicate, cardinality) must be a pair that exists in the
--  predicate table. Changing a predicate's cardinality cascades. The denormalisation is safe because
--  the database refuses the state where it would be wrong.

-- The composite key the fact will reference. A plain primary key on `predicate` is not enough: the
-- reference has to be to the PAIR, or the copy could name a real predicate with the wrong cardinality.
ALTER TABLE {schema}.predicate
    ADD CONSTRAINT predicate_cardinality_uniq UNIQUE (predicate, cardinality);

ALTER TABLE {schema}.fact
    ADD COLUMN IF NOT EXISTS cardinality text;

-- Backfilled from the vocabulary rather than defaulted, because a default would be right for
-- twenty-eight predicates and silently wrong for eleven.
UPDATE {schema}.fact f
   SET cardinality = p.cardinality
  FROM {schema}.predicate p
 WHERE f.predicate = p.predicate
   AND f.cardinality IS NULL;

ALTER TABLE {schema}.fact
    ALTER COLUMN cardinality SET NOT NULL;

ALTER TABLE {schema}.fact
    ADD CONSTRAINT fact_cardinality_matches_vocabulary
        FOREIGN KEY (predicate, cardinality)
        REFERENCES {schema}.predicate (predicate, cardinality)
        ON UPDATE CASCADE;

-- The invariant itself.
--
-- Scoped to facts with a resolved subject: a fact whose subject end never resolved to an entity has
-- nothing to be single about, and including NULLs would make every unresolved fact conflict with
-- every other. Scoped to `one` because `many` is the ordinary case and constraining it would forbid
-- somebody working at two organisations.
--
-- The object is deliberately NOT in the key. "Lives in Dublin" and "lives in Amman" overlapping is
-- the violation being prevented; including the object would make them different slots and permit
-- exactly the state this exists to stop.
ALTER TABLE {schema}.fact
    ADD CONSTRAINT fact_single_cardinality_excl
        EXCLUDE USING gist (
            scope             WITH =,
            subject_entity_id WITH =,
            predicate         WITH =,
            valid             WITH &&
        )
        WHERE (cardinality = 'one' AND subject_entity_id IS NOT NULL);

COMMENT ON COLUMN {schema}.fact.cardinality IS
    'Copied from the vocabulary so that the exclusion constraint can read it — a constraint cannot '
    'consult another table. A composite foreign key on (predicate, cardinality) makes the copy '
    'unable to disagree, so this is a denormalisation the database refuses to let go wrong.';

COMMENT ON CONSTRAINT fact_single_cardinality_excl ON {schema}.fact IS
    'A single-cardinality relation cannot hold two overlapping valid intervals for one subject. '
    'Supersession is closing the old interval before opening the new one; this is what makes '
    'forgetting to impossible rather than merely discouraged.';
