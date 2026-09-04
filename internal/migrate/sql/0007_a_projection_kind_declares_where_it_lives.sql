-- ── A projection kind declares where it lives, so erasure never needs a list ───────────────────
--
--  `projection_dependency` records that an observation produced a projection, as a kind and an id.
--  Erasure walks it — but a kind and an id do not say which table to delete from, so something has
--  to hold that mapping.
--
--  ── WHY THE MAPPING IS A TABLE AND NOT A SWITCH IN THE ERASER ────────────────────────────────
--
--  A switch is a list somebody maintains. Every milestone after this one adds a projection —
--  embeddings, community reports, summaries — and each is written by the code that produces it and
--  deleted by code somewhere else. The two drift in one direction only: the producer ships and the
--  eraser is updated later, or not at all. What survives is a residual count that says zero because
--  it counted the kinds somebody remembered.
--
--  As a table with a foreign key from `projection_dependency`, a projection kind that is not
--  declared here CANNOT BE REGISTERED AT ALL. So the failure moves from "erasure quietly misses it"
--  to "the write fails the first time it runs", which is a failure somebody notices on the day they
--  cause it.
--
--  And because the eraser iterates this table rather than a list of its own, a kind declared here is
--  covered by erasure without the eraser being changed.
--
--  ── WHY THE IDENTIFIERS ARE CONSTRAINED HERE ─────────────────────────────────────────────────
--
--  The table and column names are interpolated into a DELETE: they cannot be bind parameters,
--  because PostgreSQL parameters are values and never identifiers. The CHECK is what makes the
--  interpolation safe at the source, so the eraser is reading names that could not have been
--  anything else rather than sanitising them on the way out.

CREATE TABLE {schema}.projection_kind (
    kind             text NOT NULL PRIMARY KEY,
    projection_table text NOT NULL,
    id_column        text NOT NULL,
    description      text NOT NULL,

    CONSTRAINT projection_kind_name_chk   CHECK (kind             ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT projection_kind_table_chk  CHECK (projection_table ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT projection_kind_column_chk CHECK (id_column        ~ '^[a-z][a-z0-9_]*$')
);

INSERT INTO {schema}.projection_kind (kind, projection_table, id_column, description) VALUES
    ('chunk',          'chunk',          'chunk_id',
     'A message, stored as retrievable text. Carries the words verbatim, so it is the most direct thing an erasure has to remove.'),
    ('fact',           'fact',           'fact_id',
     'An edge derived from a message. Its evidence row goes with it, by cascade from the fact.'),
    ('entity',         'entity',         'entity_id',
     'A thing facts are about. Shared: an entity registered to a surviving observation as well as an erased one is kept, because it is also somebody else''s.'),
    ('rejected_claim', 'rejected_claim', 'rejected_claim_id',
     'A proposal that did not become a fact. Still holds a verbatim quote, so a refused claim is as erasable as an accepted one.');

-- The constraint that makes the declaration mandatory rather than customary.
--
-- Added after the seed so it validates against the kinds already registered: the migration fails
-- here if anything has been writing a projection kind this file did not declare, rather than
-- accepting the schema and leaving the disagreement for an erasure to discover.
ALTER TABLE {schema}.projection_dependency
    ADD CONSTRAINT projection_dependency_kind_fk
    FOREIGN KEY (projection_kind) REFERENCES {schema}.projection_kind (kind);

COMMENT ON TABLE {schema}.projection_kind IS
    'Every kind of derived row, and the table it lives in. projection_dependency references it, so a '
    'projection kind that is not declared here cannot be registered — which is what makes erasure''s '
    'residual count cover everything rather than everything somebody remembered.';
