-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- A human correction is source input. It survives loss of derived facts and is never reinterpreted
-- by an extractor. Source ownership controls export and deletion; the fact ID is a recovery receipt.
CREATE TABLE {schema}.curated_claim (
    scope text NOT NULL,
    source_observation_id uuid NOT NULL,
    fact_id uuid NOT NULL,
    principal_id uuid NOT NULL,
    claim jsonb NOT NULL CHECK(jsonb_typeof(claim)='object'),
    PRIMARY KEY(scope,source_observation_id),
    UNIQUE(scope,fact_id),
    FOREIGN KEY(scope,source_observation_id) REFERENCES {schema}.observation(scope,observation_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
);

-- Erasing a replacement must remove its citation link while preserving withdrawal of the old claim.
ALTER TABLE {schema}.record_retraction
    ADD COLUMN replacement_observation_id uuid,
    ADD COLUMN replacement_fact_id uuid,
    ADD CONSTRAINT retraction_replacement_pair CHECK((replacement_observation_id IS NULL)=(replacement_fact_id IS NULL)),
    ADD CONSTRAINT retraction_replacement_source FOREIGN KEY(scope,replacement_observation_id)
        REFERENCES {schema}.curated_claim(scope,source_observation_id)
        ON DELETE SET NULL (replacement_observation_id) DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION {schema}.clear_replacement_identifier() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF NEW.replacement_observation_id IS NULL THEN NEW.replacement_fact_id := NULL; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER retraction_replacement_deletion BEFORE UPDATE OF replacement_observation_id
    ON {schema}.record_retraction FOR EACH ROW EXECUTE FUNCTION {schema}.clear_replacement_identifier();
REVOKE ALL ON FUNCTION {schema}.clear_replacement_identifier() FROM PUBLIC;

ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK (operation IN (
    'observe','recall','erase','freshness','export','citation.resolve','record.list','record.history','record.retract','record.correct',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke','authenticate','formation.unpark'
));
