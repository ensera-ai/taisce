-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- A segment is a summary standing in for a contiguous range of one subject's formed turns, at a
-- level of a roll-up: level 1 over turns, level 2 over level-1 segments, and so on.
--
-- It is a PROJECTION and not an observation. The log holds what people said; a segment is
-- prose a model wrote from it, registered to every observation it covers, so an erasure of any of
-- them deletes it through its registrations, and the pass writes a new one from what survived.
-- Nothing on the read path writes here; the pass does, off the read path.

CREATE TABLE {schema}.segment (
    scope            text        NOT NULL,
    segment_id       uuid        NOT NULL,
    data_subject_id  text        NOT NULL,
    level            integer     NOT NULL,
    -- The contiguous log range covered, inclusive at both ends, in the subject's own turns.
    from_offset      bigint      NOT NULL,
    to_offset        bigint      NOT NULL,
    -- How many turns the range held when the segment was written, so a later reader can tell a
    -- range whose turns were erased from one that was always sparse.
    covered          integer     NOT NULL,
    summary          text        NOT NULL,
    written_at       timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (scope, segment_id),
    -- One segment per range per level per subject. A second would be two accounts of one stretch
    -- with nothing saying which is current.
    UNIQUE (scope, data_subject_id, level, from_offset),
    CONSTRAINT segment_level_chk   CHECK (level >= 1 AND level <= 6),
    CONSTRAINT segment_range_chk   CHECK (from_offset <= to_offset),
    CONSTRAINT segment_covered_chk CHECK (covered >= 1),
    CONSTRAINT segment_summary_chk CHECK (btrim(summary) <> '')
);

-- The read a context assembly issues: every segment of one subject, in level and range order.
CREATE INDEX segment_subject_idx ON {schema}.segment (scope, data_subject_id, level, from_offset);

COMMENT ON TABLE {schema}.segment IS
    'A summary standing in for a contiguous range of one subject''s turns at one level of the '
    'roll-up. Registered to every observation it covers and deleted when any of them erases; '
    'what replaces it is written from what survived.';

-- The declaration that makes erasure cover it without the eraser being changed. It holds the
-- subject's words, so being shared does not save it; and it is never shared, each segment being
-- one subject's.
INSERT INTO {schema}.projection_kind
    (kind, projection_table, id_column, description, survives_sharing)
VALUES ('segment', 'segment', 'segment_id',
        'A summary standing in for a range of one subject''s turns. Holds their words, so an '
        'erasure of any covered turn deletes it, and the pass writes a new one from what survived.',
        false)
ON CONFLICT (kind) DO NOTHING;

-- The registry's typed reference for this kind, so a registration is an ordinary foreign key the
-- database validates: exactly one reference per row, and a segment deleted takes its registrations.
ALTER TABLE {schema}.projection_dependency
    ADD COLUMN segment_ref uuid GENERATED ALWAYS AS (CASE WHEN projection_kind='segment' THEN projection_id::uuid END) STORED,
    DROP CONSTRAINT dependency_one_target_chk,
    ADD CONSTRAINT dependency_one_target_chk CHECK(num_nonnulls(entity_ref,fact_ref,chunk_ref,rejected_ref,report_ref,history_ref,artifact_ref,embedding_generation_ref,entity_embedding_generation_ref,report_embedding_generation_ref,segment_ref)=1),
    ADD CONSTRAINT dependency_segment_fk FOREIGN KEY (scope, segment_ref)
        REFERENCES {schema}.segment (scope, segment_id) ON DELETE CASCADE;
CREATE INDEX dependency_segment_ref_idx ON {schema}.projection_dependency (scope, segment_ref) WHERE segment_ref IS NOT NULL;

-- Assembling a context is a read of one subject's words, and every read is on the ledger:
-- the check that names what the ledger admits learns the operation here, where the operation
-- arrives, so a deployment cannot serve it unrecorded.
ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK(operation IN (
    'observe','recall','erase','freshness','export','authenticate',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke',
    'formation.unpark','formation.recover','formation.rebuild','formation.rebuild.start','formation.rebuild.cancel',
    'citation.resolve','message.get','record.list','record.history','record.retract','record.correct','record.assert',
    'artifact.put','artifact.get','artifact.list','artifact.delete','artifact.configure',
    'subject.register','subject.get','subject.list','subject.update','entity.list','entity.get',
    'embedding.start','embedding.build','embedding.activate','embedding.cancel','embedding.prune','embedding.repair','embedding.search',
    'entity_embedding.start','entity_embedding.build','entity_embedding.activate','entity_embedding.cancel','entity_embedding.prune','entity_embedding.repair','entity_embedding.search',
    'report_embedding.start','report_embedding.build','report_embedding.activate','report_embedding.cancel','report_embedding.prune','report_embedding.repair','report_embedding.search',
    'context.assemble'));
