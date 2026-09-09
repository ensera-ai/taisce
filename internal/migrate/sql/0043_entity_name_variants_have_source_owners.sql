-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- These are admitted source-owned outcomes, retained when a derived entity is lost. An entity FK
-- would destroy the only faithful reconstruction input. Source/project ownership remains a FK.
CREATE TABLE {schema}.entity_name_receipt (
    scope text NOT NULL,
    source_observation_id uuid NOT NULL,
    entity_id uuid NOT NULL,
    name text NOT NULL CHECK(octet_length(name) BETWEEN 1 AND 4096),
    normalized_name text NOT NULL CHECK(octet_length(normalized_name) BETWEEN 1 AND 4096),
    name_hash bytea NOT NULL,
    PRIMARY KEY(scope,source_observation_id,entity_id,name_hash),
    FOREIGN KEY(scope,source_observation_id) REFERENCES {schema}.observation(scope,observation_id) ON DELETE CASCADE
);
CREATE INDEX entity_name_receipt_entity_idx ON {schema}.entity_name_receipt(scope,entity_id);

CREATE FUNCTION {schema}.validate_entity_name_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.name_hash := sha256(convert_to(NEW.name,'UTF8'));
    PERFORM 1 FROM {schema}.entity WHERE scope=NEW.scope AND entity_id=NEW.entity_id
        AND identity_kind='named' AND normalized_name=NEW.normalized_name FOR KEY SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION 'name receipt must match a live named identity' USING ERRCODE='23514'; END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER entity_name_receipt_identity BEFORE INSERT OR UPDATE ON {schema}.entity_name_receipt
    FOR EACH ROW EXECUTE FUNCTION {schema}.validate_entity_name_receipt();

-- No observation owns the old arrays. Do not invent provenance for them in an unreleased schema.
UPDATE {schema}.entity SET aliases='{}',normalized_aliases='{}';
