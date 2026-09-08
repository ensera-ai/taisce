-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- Record versions are opaque compare-and-swap tokens. Every update changes the token, including
-- supersession, so an editor cannot unknowingly withdraw a record it inspected before a change.
ALTER TABLE {schema}.fact ADD COLUMN version uuid NOT NULL DEFAULT gen_random_uuid();
CREATE FUNCTION {schema}.advance_record_version() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    NEW.version := gen_random_uuid();
    RETURN NEW;
END;
$$;
CREATE TRIGGER fact_record_version BEFORE UPDATE ON {schema}.fact
    FOR EACH ROW EXECUTE FUNCTION {schema}.advance_record_version();
REVOKE ALL ON FUNCTION {schema}.advance_record_version() FROM PUBLIC;

-- A withdrawn claim can overlap a later assertion in valid time, provided their knowledge
-- intervals do not overlap. Retraction closes knowledge, without inventing when a claim was true.
ALTER TABLE {schema}.fact DROP CONSTRAINT fact_single_cardinality_excl;
ALTER TABLE {schema}.fact ADD CONSTRAINT fact_single_cardinality_excl
    EXCLUDE USING gist(scope WITH =, subject_entity_id WITH =, predicate WITH =, valid WITH &&, known WITH &&)
    WHERE (cardinality='one' AND subject_entity_id IS NOT NULL);

-- Human withdrawal instructions are source-owned input, not a rebuildable projection. The target
-- UUID is a historical identifier and intentionally survives loss of the derived fact. A source
-- deletion removes its instruction; no claim text or free-form reason is copied into this table.
CREATE TABLE {schema}.record_retraction (
    scope text NOT NULL,
    target_fact_id uuid NOT NULL,
    source_observation_id uuid NOT NULL,
    source_ordinal integer NOT NULL CHECK(source_ordinal>=0),
    claim_signature bytea NOT NULL CHECK(octet_length(claim_signature)=32),
    operation_id uuid NOT NULL,
    target_version uuid NOT NULL,
    principal_id uuid NOT NULL,
    retracted_at timestamptz NOT NULL,
    PRIMARY KEY(scope,target_fact_id,source_observation_id,source_ordinal),
    FOREIGN KEY(scope,source_observation_id) REFERENCES {schema}.observation(scope,observation_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(source_observation_id,source_ordinal) REFERENCES {schema}.turn_message(observation_id,ordinal)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX retraction_source_claim_idx ON {schema}.record_retraction(scope,source_observation_id,source_ordinal,claim_signature);

ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK (operation IN (
    'observe','recall','erase','freshness','export','citation.resolve','record.list','record.history','record.retract',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke','authenticate','formation.unpark'
));
