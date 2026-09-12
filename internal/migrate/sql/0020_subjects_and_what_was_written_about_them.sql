-- ── A subject, and the prose written about it ─────────────────────────────────────────────────
--
--  A question with no anchor names no entity and resembles no passage, so it is answered from a
--  property of the graph rather than from any row in it: a set of entities densely connected to each
--  other and sparsely to everything else is a subject somebody has. These are the two tables that
--  hold that — the grouping, and what was written about it.
--
--  ── WHY THE GROUPING IS REPLACED RATHER THAN MAINTAINED ──────────────────────────────────────
--
--  A partition is a function of the whole graph. One new fact can move an entity between subjects,
--  merge two subjects or split one, and there is no incremental edit that produces the same answer as
--  re-partitioning — so an attempt to maintain it in place would drift away from what the algorithm
--  would say, silently, in a direction nobody could reconstruct.
--
--  So a forming pass replaces every community in a scope. That makes the grouping a cache of a pure
--  function over the fact table, which is also what makes it need no erasure of its own: it holds no
--  words, and the next pass derives it from whatever survived.
--
--  ── WHY A MEMBERSHIP HAS NO FOREIGN KEY TO THE ENTITY ────────────────────────────────────────
--
--  The same reason `fact` has none on its ends. Erasure deletes entities through their own sweep and
--  communities are replaced by a pass that does not coordinate with it, so a foreign key would make
--  one of them fail depending on which ran first — on a path whose whole job is to complete. A
--  membership naming an entity that is gone is a stale row in a table that is about to be rebuilt,
--  which is the right failure.
--
--  ── WHY A REPORT IS A PROJECTION AND THE GROUPING IS NOT ─────────────────────────────────────
--
--  The report holds prose written from what several people said. That is text, it is somebody's
--  material, and D57 settles what happens to it: registered to every observation that contributed,
--  deleted when any contributor erases — WITHOUT the exemption that keeps a shared entity, because an
--  entity is a shared identity and a report is shared text. Migration 0019 made that a property of
--  the kind; this is the first kind to use it.

CREATE TABLE IF NOT EXISTS {schema}.community (
    scope        text        NOT NULL,
    community_id uuid        NOT NULL,
    -- Zero is the partition over the whole graph. A level exists where a group was too large to be
    -- one subject and was split again, so the depth is a property of the memory rather than a number
    -- somebody chose.
    level        integer     NOT NULL,
    -- The community this was split out of. Explicit rather than inferred from membership overlap: a
    -- walk that has to work out where it came from by comparing member sets gets it wrong the moment
    -- two siblings share nothing but their parent, and it does that work on every walk.
    parent_id    uuid,
    formed_at    timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (scope, community_id),
    CONSTRAINT community_level_chk CHECK (level >= 0),
    -- A root has no parent and anything deeper has one. A level-1 group with no parent would be a
    -- subject that came from nowhere, and a walk down from the root would never reach it.
    CONSTRAINT community_parent_chk CHECK ((level = 0) = (parent_id IS NULL))
);

CREATE TABLE IF NOT EXISTS {schema}.community_member (
    scope        text NOT NULL,
    community_id uuid NOT NULL,
    entity_id    uuid NOT NULL,

    PRIMARY KEY (scope, community_id, entity_id),
    FOREIGN KEY (scope, community_id)
        REFERENCES {schema}.community (scope, community_id) ON DELETE CASCADE
);

-- The other direction from the primary key: which communities an entity belongs to. The key already
-- answers "which entities are in this community" by leading with it, so this index leads with the
-- entity, which is what is known when a fact has been found and its themes are wanted.
CREATE INDEX IF NOT EXISTS community_member_entity_idx
    ON {schema}.community_member (scope, entity_id);

CREATE TABLE IF NOT EXISTS {schema}.community_report (
    scope             text        NOT NULL,
    report_id         uuid        NOT NULL,
    community_id      uuid        NOT NULL,
    title             text        NOT NULL,
    summary           text        NOT NULL,
    -- How much the subject appears to matter, WITH the reason. The number alone would be a ranking
    -- signal nobody could check, and anything that cannot be argued with gets believed.
    importance        real        NOT NULL DEFAULT 0,
    importance_reason text        NOT NULL DEFAULT '',
    -- The specific things the subject consists of, each with the reasoning behind it. jsonb because
    -- nothing queries inside a finding: they are read whole, with the report, by whoever received it.
    -- A table would be a join on every read for a shape nobody filters on.
    findings          jsonb       NOT NULL DEFAULT '[]'::jsonb,
    -- What the report was written from, so a rebuild that produces a different summary is
    -- distinguishable from one that was given different material.
    substituted       integer     NOT NULL DEFAULT 0,
    dropped           integer     NOT NULL DEFAULT 0,
    written_at        timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (scope, report_id),
    -- One report per community. A second would be two descriptions of one subject with nothing
    -- saying which is current, and a thematic answer would return whichever the plan reached first.
    UNIQUE (scope, community_id),
    FOREIGN KEY (scope, community_id)
        REFERENCES {schema}.community (scope, community_id) ON DELETE CASCADE,
    CONSTRAINT community_report_title_chk   CHECK (btrim(title) <> ''),
    CONSTRAINT community_report_summary_chk CHECK (btrim(summary) <> ''),
    CONSTRAINT community_report_importance_chk CHECK (importance >= 0 AND importance <= 10),
    CONSTRAINT community_report_counted_chk CHECK (substituted >= 0 AND dropped >= 0)
);

COMMENT ON TABLE {schema}.community_report IS
    'Prose written from what several people said about one subject. Registered to every observation '
    'that contributed and deleted when any contributor erases — without the exemption that keeps a '
    'shared entity, because an entity is a shared identity and this is shared text.';

-- The declaration that makes erasure cover it without the eraser being changed, and the first kind
-- to answer 0019's question with `false`.
INSERT INTO {schema}.projection_kind
    (kind, projection_table, id_column, description, survives_sharing)
VALUES ('community_report', 'community_report', 'report_id',
        'Prose about a subject, written from what several people said. Holds their words, so being '
        'shared does not save it: a report a departing subject contributed to is deleted even when '
        'somebody else contributed too, and what replaces it is written from what survived.',
        false)
ON CONFLICT (kind) DO NOTHING;

-- ── What is deliberately not here yet ────────────────────────────────────────────────────────
--
--  The embedding. A vector column needs a fixed dimension before an index can be built on it, and the
--  dimension is a property of the embedder an operator chose rather than of this schema — so it is
--  created where that is known rather than guessed here. A column with no dimension would take the
--  index away and make every thematic read a sequential scan, which is a worse answer than not having
--  the column: it would look built and be unusable.
