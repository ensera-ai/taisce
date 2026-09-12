-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- A retained claim keeps its identity. These are causal references for its current closed validity.
-- Erasing the cause invalidates the derived record; reopening it would guess at remaining history.
ALTER TABLE {schema}.fact
    ADD COLUMN superseded_by uuid,
    ADD COLUMN supersession_source uuid,
    ADD CONSTRAINT fact_successor_project_fk FOREIGN KEY(scope,superseded_by)
        REFERENCES {schema}.fact(scope,fact_id) ON DELETE SET NULL (superseded_by) DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT fact_supersession_source_fk FOREIGN KEY(scope,supersession_source)
        REFERENCES {schema}.observation(scope,observation_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX fact_supersession_source_idx ON {schema}.fact(scope,supersession_source) WHERE supersession_source IS NOT NULL;
CREATE INDEX fact_successor_idx ON {schema}.fact(scope,superseded_by) WHERE superseded_by IS NOT NULL;

CREATE TABLE {schema}.fact_history (
    history_id uuid PRIMARY KEY,
    scope text NOT NULL,
    fact_id uuid NOT NULL,
    valid tstzrange NOT NULL CHECK (NOT isempty(valid)),
    known tstzrange NOT NULL CHECK (NOT isempty(known) AND NOT upper_inf(known)),
    source_observation_id uuid NOT NULL,
    FOREIGN KEY(scope,fact_id) REFERENCES {schema}.fact(scope,fact_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(scope,source_observation_id) REFERENCES {schema}.observation(scope,observation_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    EXCLUDE USING gist (fact_id WITH =, known WITH &&)
);
CREATE INDEX fact_history_source_idx ON {schema}.fact_history(scope,source_observation_id);
INSERT INTO {schema}.projection_kind(kind,projection_table,id_column,survives_sharing,description)
VALUES ('fact_history','fact_history','history_id',false,'Earlier validity and knowledge intervals with the source of their transition.');

ALTER TABLE {schema}.fact_history ADD CONSTRAINT fact_history_scope_id_uniq UNIQUE(scope,history_id);
ALTER TABLE {schema}.projection_dependency
    ADD COLUMN history_ref uuid GENERATED ALWAYS AS (CASE WHEN projection_kind='fact_history' THEN projection_id::uuid END) STORED,
    DROP CONSTRAINT dependency_one_target_chk,
    ADD CONSTRAINT dependency_one_target_chk CHECK (num_nonnulls(entity_ref,fact_ref,chunk_ref,rejected_ref,report_ref,history_ref)=1),
    ADD CONSTRAINT dependency_history_project_fk FOREIGN KEY(scope,history_ref)
        REFERENCES {schema}.fact_history(scope,history_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX dependency_history_ref_idx ON {schema}.projection_dependency(scope,history_ref) WHERE history_ref IS NOT NULL;
