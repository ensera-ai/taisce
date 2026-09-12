-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
-- Validate existing relationships atomically. No mismatched project or missing endpoint is guessed
-- into correctness. Deferred checks permit erasure's projection order inside one transaction, while
-- refusing an incomplete transaction at commit.
ALTER TABLE {schema}.entity ADD CONSTRAINT entity_scope_id_uniq UNIQUE(scope,entity_id);
ALTER TABLE {schema}.fact ADD CONSTRAINT fact_scope_id_uniq UNIQUE(scope,fact_id);
ALTER TABLE {schema}.fact
    ADD CONSTRAINT fact_subject_project_fk FOREIGN KEY(scope,subject_entity_id)
        REFERENCES {schema}.entity(scope,entity_id) DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT fact_object_project_fk FOREIGN KEY(scope,object_entity_id)
        REFERENCES {schema}.entity(scope,entity_id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {schema}.fact_evidence ADD COLUMN scope text;
UPDATE {schema}.fact_evidence e SET scope=f.scope FROM {schema}.fact f WHERE f.fact_id=e.fact_id;
ALTER TABLE {schema}.fact_evidence
    ALTER COLUMN scope SET NOT NULL,
    DROP CONSTRAINT fact_evidence_fact_id_fkey,
    ADD CONSTRAINT evidence_fact_project_fk FOREIGN KEY(scope,fact_id)
        REFERENCES {schema}.fact(scope,fact_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT evidence_source_project_fk FOREIGN KEY(scope,source_observation_id)
        REFERENCES {schema}.observation(scope,observation_id) DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT evidence_message_fk FOREIGN KEY(source_observation_id,source_ordinal)
        REFERENCES {schema}.turn_message(observation_id,ordinal) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX evidence_source_message_idx ON {schema}.fact_evidence(source_observation_id,source_ordinal);

ALTER TABLE {schema}.projection_dependency
    DROP CONSTRAINT projection_dependency_source_observation_id_fkey,
    ADD CONSTRAINT dependency_source_project_fk FOREIGN KEY(scope,source_observation_id)
        REFERENCES {schema}.observation(scope,observation_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE {schema}.chunk ADD CONSTRAINT chunk_source_project_fk FOREIGN KEY(scope,source_observation_id)
    REFERENCES {schema}.observation(scope,observation_id) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX chunk_source_idx ON {schema}.chunk(scope,source_observation_id);
ALTER TABLE {schema}.rejected_claim
    ADD CONSTRAINT rejected_source_project_fk FOREIGN KEY(scope,source_observation_id)
        REFERENCES {schema}.observation(scope,observation_id) DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT rejected_source_message_fk FOREIGN KEY(source_observation_id,source_ordinal)
        REFERENCES {schema}.turn_message(observation_id,ordinal) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX rejected_source_message_idx ON {schema}.rejected_claim(source_observation_id,source_ordinal);
ALTER TABLE {schema}.community_member ADD CONSTRAINT member_entity_project_fk FOREIGN KEY(scope,entity_id)
    REFERENCES {schema}.entity(scope,entity_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE {schema}.community ADD CONSTRAINT community_parent_project_fk FOREIGN KEY(scope,parent_id)
    REFERENCES {schema}.community(scope,community_id) DEFERRABLE INITIALLY DEFERRED;

-- Older report registrations matched quote text and omitted substituted children's sources. Those
-- generated summaries cannot safely be grandfathered. The worker regenerates missing reports from
-- retained facts with exact source provenance; no observation, message, fact, or evidence is removed.
DELETE FROM {schema}.community_report;
DELETE FROM {schema}.projection_dependency WHERE projection_kind='community_report';

-- Materialised reference columns give the polymorphic registry ordinary foreign keys. Exactly one
-- reference must be present; adding a projection kind therefore requires its relationship migration,
-- not merely a catalog row which the database cannot validate.
ALTER TABLE {schema}.rejected_claim ADD CONSTRAINT rejected_scope_id_uniq UNIQUE(scope,rejected_claim_id);
ALTER TABLE {schema}.projection_dependency
    ADD COLUMN entity_ref uuid GENERATED ALWAYS AS (CASE WHEN projection_kind='entity' THEN projection_id::uuid END) STORED,
    ADD COLUMN fact_ref uuid GENERATED ALWAYS AS (CASE WHEN projection_kind='fact' THEN projection_id::uuid END) STORED,
    ADD COLUMN chunk_ref uuid GENERATED ALWAYS AS (CASE WHEN projection_kind='chunk' THEN projection_id::uuid END) STORED,
    ADD COLUMN rejected_ref uuid GENERATED ALWAYS AS (CASE WHEN projection_kind='rejected_claim' THEN projection_id::uuid END) STORED,
    ADD COLUMN report_ref uuid GENERATED ALWAYS AS (CASE WHEN projection_kind='community_report' THEN projection_id::uuid END) STORED,
    ADD CONSTRAINT dependency_one_target_chk CHECK (num_nonnulls(entity_ref,fact_ref,chunk_ref,rejected_ref,report_ref)=1),
    ADD CONSTRAINT dependency_entity_project_fk FOREIGN KEY(scope,entity_ref)
        REFERENCES {schema}.entity(scope,entity_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT dependency_fact_project_fk FOREIGN KEY(scope,fact_ref)
        REFERENCES {schema}.fact(scope,fact_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT dependency_chunk_project_fk FOREIGN KEY(scope,chunk_ref)
        REFERENCES {schema}.chunk(scope,chunk_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT dependency_rejected_project_fk FOREIGN KEY(scope,rejected_ref)
        REFERENCES {schema}.rejected_claim(scope,rejected_claim_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT dependency_report_project_fk FOREIGN KEY(scope,report_ref)
        REFERENCES {schema}.community_report(scope,report_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX dependency_entity_ref_idx ON {schema}.projection_dependency(scope,entity_ref) WHERE entity_ref IS NOT NULL;
CREATE INDEX dependency_fact_ref_idx ON {schema}.projection_dependency(scope,fact_ref) WHERE fact_ref IS NOT NULL;
CREATE INDEX dependency_chunk_ref_idx ON {schema}.projection_dependency(scope,chunk_ref) WHERE chunk_ref IS NOT NULL;
CREATE INDEX dependency_rejected_ref_idx ON {schema}.projection_dependency(scope,rejected_ref) WHERE rejected_ref IS NOT NULL;
CREATE INDEX dependency_report_ref_idx ON {schema}.projection_dependency(scope,report_ref) WHERE report_ref IS NOT NULL;
