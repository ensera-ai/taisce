-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- ── A doubt is not yet a belief ────────────────────────────────────────────────────────────────
--
-- Everything this system can be told is, until now, something it then holds true. An assertion, a
-- correction and a retraction each change what recall returns the moment they commit. So the only
-- way to report that a record looks wrong has been to act on it: withdraw the record, or replace it
-- with something the reporter may not be sure of. The report and the decision were the same write.
--
-- ── WHY FEEDBACK IS ITS OWN TABLE AND NOT A COLUMN ON `fact` ─────────────────────────────────
--
-- A fact has a validity range, a knowledge range, a cardinality, a subject entity, an object entity
-- and a predecessor it supersedes. Every one of those is machinery for deciding what is true when.
-- Feedback has none of them: it is not true of anything, it is not about a moment, and nothing it
-- says competes with anything else. Given a `kind` column on `fact`, every query on the read path
-- would acquire `AND kind='fact'` — and the one written under pressure that forgets it would return
-- somebody's unreviewed complaint as knowledge. A separate table cannot be read by accident.
--
-- It also keeps the promotion honest. Because feedback is not a fact, promoting it cannot be an
-- UPDATE that flips a flag: it has to produce a real correction or a real retraction, which is what
-- carries the curated provenance, the replay behaviour and the temporal bookkeeping this table
-- deliberately does not reimplement.
--
-- ── WHAT IT IS ATTACHED TO ────────────────────────────────────────────────────────────────────
--
-- A record id — the same identifier recall and `/records/list` return, which is the only identifier
-- a caller has ever seen. Attaching feedback to an entity would make it about a thing rather than
-- about a claim, and "this is wrong" needs to say which claim. The foreign key is to `fact`, so
-- feedback about a record that is erased goes with it by cascade, before the registry is consulted
-- at all.
--
-- ── HOW ERASURE REACHES IT ────────────────────────────────────────────────────────────────────
--
-- Through `projection_dependency`, like every other derived row, registered against each observation
-- that supports the target record. It is declared `survives_sharing = false`: feedback is somebody's
-- own words about a claim, so a second contributor to the same record does not make those words
-- theirs to keep. The generated `feedback_ref` column and its foreign key are what make the
-- registration real to the database rather than a catalog row nothing validates.

CREATE TABLE {schema}.memory_feedback (
    scope           text        NOT NULL,
    feedback_id     uuid        NOT NULL DEFAULT gen_random_uuid(),
    target_fact_id  uuid        NOT NULL,
    -- What the reporter said. Bounded at the same 16 KiB a correction's statement is bounded at,
    -- because promotion may hand exactly this text to `fact.statement` and a bound that only one of
    -- the two enforces is a bound the other discovers at INSERT time.
    note            text        NOT NULL,
    -- The object the reporter proposes instead, or NULL for "this should not be here at all". This
    -- single nullable column is the whole of the promotion's branch: present means the promotion is
    -- a correction, absent means it is a retraction. A `kind` column beside it could disagree with
    -- it, and then one of the two would be the one the promotion actually read.
    proposed_object text,
    -- The credential, kept as a bare identifier the way every other principal column here is: the
    -- credential register is control-plane state and a project schema does not get a foreign key
    -- into it.
    recorded_by     uuid        NOT NULL,
    recorded_at     timestamptz NOT NULL DEFAULT now(),
    -- Promotion is recorded here rather than inferred from the existence of a correction: the
    -- correction does not know it came from feedback, and asking "was this promoted" must not
    -- depend on matching text.
    promoted_by     uuid,
    promoted_at     timestamptz,
    -- The record the promotion produced. NULL for a promoted retraction, which produces none. No
    -- foreign key, for the reason a retraction's replacement has none: the promotion is the history
    -- of what was decided, and it must still read correctly after the record it produced has itself
    -- been corrected away or erased.
    promotion_fact_id uuid,

    PRIMARY KEY (scope, feedback_id),
    CONSTRAINT feedback_note_chk CHECK (octet_length(note) BETWEEN 1 AND 16384),
    CONSTRAINT feedback_object_chk CHECK (proposed_object IS NULL
        OR octet_length(proposed_object) BETWEEN 1 AND 1024),
    -- Promoted is all three or none of them, so "promoted" can never be half true. The second
    -- promotion of one feedback is refused by reading this row FOR UPDATE and finding it set; this
    -- constraint is what makes that reading trustworthy.
    CONSTRAINT feedback_promotion_chk CHECK (num_nonnulls(promoted_by, promoted_at) IN (0, 2)),
    CONSTRAINT feedback_promotion_record_chk CHECK (promotion_fact_id IS NULL OR promoted_at IS NOT NULL),
    CONSTRAINT feedback_target_fk FOREIGN KEY (scope, target_fact_id)
        REFERENCES {schema}.fact (scope, fact_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
);

-- Two reads, two indexes, and both carry the page order rather than only the filter.
--
-- The common read is "what is outstanding on this project", so that index is partial on the open
-- state: the alternative is a scan that reads every promoted report to skip it, and promoted is what
-- almost every row eventually becomes.
--
-- The second read narrows to one record. It carries `(recorded_at DESC, feedback_id DESC)` after the
-- target because the page is ordered by them — measured, not assumed: without the trailing columns
-- PostgreSQL seeks the index and then sorts what it found, which is a sort whose size grows with how
-- much feedback one record has collected.
--
-- `feedback_id` descends with `recorded_at` because the cursor compares the two as a tuple, and an
-- index whose second key ascends cannot serve a descending tuple comparison as a seek.
CREATE INDEX memory_feedback_open_idx ON {schema}.memory_feedback
    (scope, recorded_at DESC, feedback_id DESC) WHERE promoted_at IS NULL;
CREATE INDEX memory_feedback_target_idx ON {schema}.memory_feedback
    (scope, target_fact_id, recorded_at DESC, feedback_id DESC);

COMMENT ON TABLE {schema}.memory_feedback IS
    'A report about a record that asserts nothing. Never read by recall: promoting it produces an '
    'ordinary correction or retraction, and that is what changes what the system believes.';

INSERT INTO {schema}.projection_kind (kind, projection_table, id_column, description, survives_sharing)
VALUES ('feedback', 'memory_feedback', 'feedback_id',
        'Somebody''s words about a claim, held outside the graph until a promotion acts on it. Being '
        'shared does not save it: a second contributor to the record it is about did not write it.',
        false)
ON CONFLICT (kind) DO NOTHING;

ALTER TABLE {schema}.projection_dependency
    ADD COLUMN feedback_ref uuid GENERATED ALWAYS AS
        (CASE WHEN projection_kind = 'feedback' THEN projection_id::uuid END) STORED,
    DROP CONSTRAINT dependency_one_target_chk,
    ADD CONSTRAINT dependency_one_target_chk
        CHECK(num_nonnulls(entity_ref,fact_ref,chunk_ref,rejected_ref,report_ref,history_ref,artifact_ref,embedding_generation_ref,entity_embedding_generation_ref,report_embedding_generation_ref,segment_ref,feedback_ref)=1),
    ADD CONSTRAINT dependency_feedback_project_fk FOREIGN KEY (scope, feedback_ref)
        REFERENCES {schema}.memory_feedback (scope, feedback_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX dependency_feedback_ref_idx ON {schema}.projection_dependency (scope, feedback_ref)
    WHERE feedback_ref IS NOT NULL;

-- The ledger admits the three operations, so a route serving one of them cannot be served
-- unrecorded: a name this constraint does not carry is a write the database refuses.
ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK(operation IN (
    'observe','recall','erase','freshness','export','authenticate',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke',
    'formation.unpark','formation.recover','formation.rebuild','formation.rebuild.start','formation.rebuild.cancel',
    'citation.resolve','message.get','record.list','record.history','record.retract','record.correct','record.assert',
    'feedback.record','feedback.list','feedback.promote',
    'artifact.put','artifact.get','artifact.list','artifact.delete','artifact.configure',
    'subject.register','subject.get','subject.list','subject.update','entity.list','entity.get','entity.purge',
    'notification.register','notification.list','notification.disable','notification.deliveries',
    'embedding.start','embedding.build','embedding.activate','embedding.cancel','embedding.prune','embedding.repair','embedding.search',
    'entity_embedding.start','entity_embedding.build','entity_embedding.activate','entity_embedding.cancel','entity_embedding.prune','entity_embedding.repair','entity_embedding.search',
    'report_embedding.start','report_embedding.build','report_embedding.activate','report_embedding.cancel','report_embedding.prune','report_embedding.repair','report_embedding.search',
    'context.assemble',
    'project.list','credential.list','refusal.summary','erasure.list','formation.status','formation.parked','audit.seal','audit.verify'));
