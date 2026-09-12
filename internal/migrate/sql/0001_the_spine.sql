-- ═══════════════════════════════════════════════════════════════════════════════════════════════
--  The spine — the smallest schema an observation can be written into and recalled from
-- ═══════════════════════════════════════════════════════════════════════════════════════════════
--
--  Not a port of a mature schema: everything here is load-bearing for docs/00-goals.md §3, and
--  anything that is not yet needed is not yet here.
--
--  ── THE TWO BOUNDARIES, WHICH ARE NOT THE SAME KIND OF THING ─────────────────────────────────
--
--  A TENANT IS A SCHEMA. Hard boundary. This file runs once per tenant, inside that tenant's
--  schema, and `{schema}` is interpolated by a validated identifier type rather than bound as a
--  parameter — PostgreSQL parameters are values, never identifiers.
--
--  That is the whole reason the boundary is hard: a cross-tenant read is impossible because the
--  other tenant's NAME IS NOT IN THE STATEMENT, not because a predicate excluded it. A bug in a
--  WHERE clause cannot leak across it. There is no `tenant_id` column here to forget.
--
--  A PROJECT IS A SCOPE. Soft boundary, inside one tenant. Cross-project reads are INTENDED — a
--  principal granted several projects recalls across them in one bundle — so `scope` is a column,
--  every read carries the authorised set, and the check is a permission rather than a wall.
--
--  Two decisions are taken NOW because retrofitting either is a migration rather than an edit —
--  see the sections marked ── DECIDED HERE ──.

-- ── The observation log ────────────────────────────────────────────────────────────────────────
--
-- Append-only, and the root of every erasure. Nothing else in this schema is authoritative: every
-- other table is a PROJECTION, derived from this and re-derivable from it. That is what makes
-- "rebuild, never repair" possible — after an erasure the next rebuild is derived only from
-- surviving content, so residual is zero BY CONSTRUCTION rather than by the correctness of a
-- repair routine.
CREATE TABLE {schema}.observation (
    observation_id  uuid        PRIMARY KEY,
    scope           text        NOT NULL,
    -- Contiguous per scope. The freshness watermark is the highest offset with no gap below it, so
    -- a customer reading "we are current to N" is reading a number that cannot lie by omission.
    log_offset      bigint      NOT NULL,
    kind            text        NOT NULL,
    occurred_at     timestamptz NOT NULL,
    ingested_at     timestamptz NOT NULL DEFAULT now(),

    -- WHO SAID IT, carried from the first migration rather than added later.
    --
    -- The role policy — an assistant's claim never becomes a user's fact — can only be enforced if
    -- every write surface carries the speaker. A role that is optional at the bottom becomes
    -- optional everywhere above it, and the surface that skips it is usually the one written last
    -- and used most. NOT NULL from the first migration, so there is no surface that can omit it.
    source_role     text        NOT NULL,
    actor           jsonb       NOT NULL DEFAULT '{}'::jsonb,
    payload         jsonb       NOT NULL,

    -- Governance travels with the row, not in a side table, because every read has to filter on it
    -- and a join is a way to forget.
    data_subject_id text,
    lawful_basis    text,
    classification  text        NOT NULL DEFAULT 'internal',
    retention_until timestamptz,

    CONSTRAINT observation_source_role_chk
        CHECK (source_role = ANY (ARRAY['user', 'assistant', 'system', 'tool', 'document'])),
    CONSTRAINT observation_scope_offset_uniq UNIQUE (scope, log_offset)
);

CREATE INDEX observation_subject_idx ON {schema}.observation (scope, data_subject_id)
    WHERE data_subject_id IS NOT NULL;

-- ── Erasure's index ────────────────────────────────────────────────────────────────────────────
--
-- Every projection registers what it derived and from which observation. Erasure walks THIS, not a
-- hand-maintained list of tables, which is what lets `COUNT(*)` after the delete mean "nothing
-- derived from that observation survives" rather than "nothing survives in the tables somebody
-- remembered to check".
--
-- A projection kind that does not appear here is invisible to erasure. G6's test asserts the set of
-- kinds registered matches the set of kinds written — so a new projection added in M2..M5 cannot
-- silently escape.
CREATE TABLE {schema}.projection_dependency (
    source_observation_id uuid        NOT NULL REFERENCES {schema}.observation (observation_id) ON DELETE CASCADE,
    scope                 text        NOT NULL,
    projection_kind       text        NOT NULL,
    projection_id         text        NOT NULL,
    data_subject_id       text,
    classification        text        NOT NULL DEFAULT 'internal',
    retention_until       timestamptz,
    pipeline_version      text        NOT NULL DEFAULT 'v1',
    registered_at         timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (source_observation_id, projection_kind, projection_id)
);

CREATE INDEX projection_dependency_kind_idx    ON {schema}.projection_dependency (scope, projection_kind, projection_id);
CREATE INDEX projection_dependency_subject_idx ON {schema}.projection_dependency (scope, data_subject_id)
    WHERE data_subject_id IS NOT NULL;

-- ── Entities ───────────────────────────────────────────────────────────────────────────────────
--
-- Primary, not a side table. This is the whole inversion: retrieval anchors here and expands, so
-- cost is proportional to a neighbourhood rather than to the corpus.
--
-- ── ENTITY TYPE IS NOT PART OF IDENTITY ──────────────────────────────────────────────────────
--
-- Identity is (scope, normalized_name). Type is an attribute.
--
-- Putting the type in the key means "Dublin office" extracted once as `place` and once as `thing`
-- is TWO NODES holding two halves of its facts — a silent under-merge on every hop, invisible
-- because both rows look correct on their own. A disagreement about type should be a disagreement
-- about one node, not the creation of a second.
CREATE TABLE {schema}.entity (
    entity_id          uuid        PRIMARY KEY,
    scope              text        NOT NULL,
    canonical_name     text        NOT NULL,
    normalized_name    text        NOT NULL,
    entity_type        text        NOT NULL DEFAULT 'thing',
    aliases            text[]      NOT NULL DEFAULT '{}',
    normalized_aliases text[]      NOT NULL DEFAULT '{}',
    -- Stamped so a corpus resolved under changed rules is distinguishable from one resolved under
    -- these. It migrates nothing; it makes the difference visible when somebody asks why two rows
    -- that look alike are separate.
    resolution_version text        NOT NULL DEFAULT 'entity/v1',
    first_seen_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT entity_identity_uniq UNIQUE (scope, normalized_name)
);

-- The alias lookup a seed resolution issues on every recall.
CREATE INDEX entity_aliases_idx ON {schema}.entity USING gin (normalized_aliases);

-- ── Facts, which are the edges ─────────────────────────────────────────────────────────────────
--
-- One edge list. There is no co-occurrence layer and there will not be one: an edge between terms
-- that appeared near each other has no direction and no meaning, so there is nothing to follow and
-- a multi-hop question has no hops.
--
-- ── DECIDED HERE: TIME IS A RANGE, NOT A SENTINEL ─────────────────────────────────────────────
--
-- The alternative is a pair of scalar columns per axis with a sentinel for "open", which puts
-- `(invalid_at = 0 OR invalid_at > $3)` in every query and makes the end column a poor leading key
-- in every index. That is a representation problem surfacing as an indexing problem.
--
--   `valid`  — when the world was this way
--   `known`  — when we believed it
--
-- Two ranges, two GiST indexes, containment operators. The exclusion constraint that makes
-- supersession the DATABASE's invariant rather than the worker's discipline lands in M3; the
-- columns are shaped for it now so that arrives as a constraint rather than as a rewrite.
CREATE TABLE {schema}.fact (
    fact_id           uuid        PRIMARY KEY,
    scope             text        NOT NULL,

    -- NO FOREIGN KEY on either end, deliberately. Erasure deletes facts through
    -- `projection_dependency` and entities through their own sweep, in orders neither coordinates
    -- with the other; a foreign key would make one of those fail depending on which ran first, on a
    -- path whose whole job is to complete. A dangling id means an unresolvable hop — the traversal
    -- does not follow it — which is the right failure: a fact whose subject was erased should stop
    -- being reachable through that subject.
    subject_entity_id uuid,
    object_entity_id  uuid,
    -- Closed ontology from M2. Text here because the ontology table does not exist yet, and a
    -- foreign key to a table that has not been designed is a guess.
    predicate         text        NOT NULL,
    statement         text        NOT NULL,

    valid             tstzrange   NOT NULL,
    known             tstzrange   NOT NULL DEFAULT tstzrange(now(), NULL),

    -- The extractor's own certainty, kept separate from salience. Salience is COMPUTED — recency,
    -- frequency, centrality, pinning — and confidence is REPORTED by whatever produced the claim.
    -- Collapsing them means a weakly-extracted fact that is mentioned often becomes indistinguishable
    -- from a certain one, and there is then no way to review extraction quality after the fact.
    confidence        real        NOT NULL DEFAULT 0,
    salience          real        NOT NULL DEFAULT 0,
    lang              text        NOT NULL DEFAULT 'en',
    recorded_at       timestamptz NOT NULL DEFAULT now(),
    data_subject_id   text,
    retention_until   timestamptz,

    -- A range that is empty says a fact was true for no time at all, which is not a claim anybody
    -- can have made.
    CONSTRAINT fact_valid_not_empty_chk CHECK (NOT isempty(valid)),
    CONSTRAINT fact_known_not_empty_chk CHECK (NOT isempty(known))
);

-- Traversal, both directions. Written as two indexes rather than one because the hop's final join
-- is `subject = $1 OR object = $1`, and an OR across two columns defeats a single index — the
-- planner falls back to a sequential scan once per traversal. The query is written as two
-- index-driven arms under UNION ALL from the start; these are what it drives.
CREATE INDEX fact_subject_idx ON {schema}.fact USING gist (scope, subject_entity_id, valid)
    WHERE subject_entity_id IS NOT NULL;
CREATE INDEX fact_object_idx  ON {schema}.fact USING gist (scope, object_entity_id, valid)
    WHERE object_entity_id IS NOT NULL;

-- ── The supersession lookup, which runs on EVERY append ──────────────────────────────────────
--
-- Asserting a fact means first asking "is there a current fact with this subject and predicate that
-- this one contradicts". That is the hottest read on the write path — more frequent than any
-- retrieval query, because every observation issues it per extracted triple.
--
-- Neither traversal index serves it: they lead with the entity and then the validity range, so
-- finding one predicate among a subject's facts means reading all of them. This is the btree that
-- makes reconciliation a lookup, and in M3 it is also what the exclusion constraint rests on.
--
-- Partial on the open interval, so it holds only currently-valid facts and stays small as history
-- accumulates rather than growing with every superseded version.
CREATE INDEX fact_reconcile_idx ON {schema}.fact (scope, subject_entity_id, predicate)
    WHERE upper_inf(valid) AND subject_entity_id IS NOT NULL;

-- "What was true at T1, as best we knew at T2" — the bitemporal question, as one index.
CREATE INDEX fact_bitemporal_idx ON {schema}.fact USING gist (scope, valid, known);

CREATE INDEX fact_subject_ref_idx ON {schema}.fact (scope, data_subject_id)
    WHERE data_subject_id IS NOT NULL;

-- ── Evidence ───────────────────────────────────────────────────────────────────────────────────
--
-- A fact is a claim WITH its receipt: the verbatim span that produced it, located in the source by
-- byte offset. This is what `resolve_citation` reads, and it is the feature nothing else in the
-- market has — so it is in the first migration rather than added when somebody asks for it.
CREATE TABLE {schema}.fact_evidence (
    fact_id               uuid    NOT NULL REFERENCES {schema}.fact (fact_id) ON DELETE CASCADE,
    source_observation_id uuid    NOT NULL,
    quote                 text    NOT NULL,
    byte_start            integer NOT NULL,
    byte_end              integer NOT NULL,
    extractor_version     text    NOT NULL,

    PRIMARY KEY (fact_id, source_observation_id, byte_start),
    CONSTRAINT fact_evidence_span_chk CHECK (byte_end > byte_start)
);

-- ── Chunks and their embeddings ────────────────────────────────────────────────────────────────
--
-- Passages are EVIDENCE for what the traversal found, not the thing retrieval searches. That is
-- the inversion, and it is why this table is last in the file rather than first.
--
-- ── DECIDED HERE: SOURCE CHUNKS PARTITIONED BY PROJECT ─────────────────────────────────────────
--
-- The project partition keeps source lookup, erasure and maintenance physically local when projects
-- are large enough to justify it. This is an operating-layout choice rather than an authorization
-- boundary; every query and constraint still carries scope. Migration 0046 removes the unstamped
-- vector column below. Message, entity and report vectors live in model-identified tables partitioned
-- by generation, with a directly-created HNSW index on each generation child. There is no virtual
-- HNSW index on a partitioned parent. Project-count and small-project costs require the controlled
-- layout measurements tracked in #101.
CREATE TABLE {schema}.chunk (
    chunk_id              uuid        NOT NULL,
    scope                 text        NOT NULL,
    source_observation_id uuid        NOT NULL,
    text                  text        NOT NULL,
    lang                  text        NOT NULL DEFAULT 'en',
    occurred_at           timestamptz NOT NULL,
    source_role           text        NOT NULL,
    data_subject_id       text,
    embedding             vector(1024),

    PRIMARY KEY (scope, chunk_id)
) PARTITION BY LIST (scope);

-- The default partition catches a scope whose partition was never created. It exists so that a
-- missing partition is a slow query rather than a failed write — losing source memory because
-- provisioning missed a step is the worse failure, and a row here is visible to the same erasure.
CREATE TABLE {schema}.chunk_unpartitioned PARTITION OF {schema}.chunk DEFAULT;

COMMENT ON TABLE {schema}.chunk_unpartitioned IS
    'Catches writes for a scope with no partition. Should always be empty; a non-zero count means '
    'provisioning did not create a partition and that project has no ANN index.';

-- ── Freshness ──────────────────────────────────────────────────────────────────────────────────
--
-- The number a customer reads when they ask "is what I just wrote visible yet". It is the highest
-- log offset with NO GAP BELOW IT, which is why `observation.log_offset` is assigned per scope
-- rather than by a global identity sequence: a globally-issued offset has holes in every scope by
-- construction, so "contiguous" would be unanswerable and the watermark would have to mean
-- something weaker.
CREATE TABLE {schema}.watermark (
    scope        text        PRIMARY KEY,
    log_offset   bigint      NOT NULL,
    watermark_at timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- ── Erasure requests ───────────────────────────────────────────────────────────────────────────
--
-- `projection_dependency` says WHAT to delete. This says who asked, when, whether it finished, and
-- — the part that is the product — what the residual count was afterwards.
--
-- A request that has no `completed_at` is an erasure in flight; one with a non-zero residual is an
-- erasure that did not do what it claimed, and it must be visible as a row rather than as an
-- absence in a log.
CREATE TABLE {schema}.erasure_request (
    request_id     uuid        PRIMARY KEY,
    scope          text        NOT NULL,
    selector       jsonb       NOT NULL,
    reason         text        NOT NULL,
    requested_at   timestamptz NOT NULL DEFAULT now(),
    completed_at   timestamptz,
    -- Per projection kind, counted after the delete in the same transaction. This is the artefact
    -- the whole product is sold on; it is a column, not a log line.
    residual       jsonb,

    CONSTRAINT erasure_residual_with_completion_chk
        CHECK ((completed_at IS NULL) = (residual IS NULL))
);

CREATE INDEX erasure_request_open_idx ON {schema}.erasure_request (scope, requested_at)
    WHERE completed_at IS NULL;

-- ── Schema version ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE {schema}.schema_migration (
    version    integer     PRIMARY KEY,
    name       text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
);
