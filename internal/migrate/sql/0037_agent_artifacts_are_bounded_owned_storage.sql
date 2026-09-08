-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

CREATE TABLE {schema}.agent_storage_policy (
    scope text PRIMARY KEY REFERENCES {schema}.project(scope) ON DELETE CASCADE,
    max_object_bytes bigint NOT NULL DEFAULT 262144 CHECK(max_object_bytes BETWEEN 1 AND 524288),
    max_bytes bigint NOT NULL DEFAULT 16777216 CHECK(max_bytes BETWEEN 1 AND 1073741824),
    max_objects bigint NOT NULL DEFAULT 128 CHECK(max_objects BETWEEN 1 AND 100000),
    max_age_hours integer NOT NULL DEFAULT 720 CHECK(max_age_hours BETWEEN 1 AND 8760),
    used_bytes bigint NOT NULL DEFAULT 0 CHECK(used_bytes>=0),
    used_objects bigint NOT NULL DEFAULT 0 CHECK(used_objects>=0),
    CHECK(max_object_bytes<=max_bytes)
);
INSERT INTO {schema}.agent_storage_policy(scope) SELECT scope FROM {schema}.project;
CREATE FUNCTION {schema}.initialize_agent_storage_policy() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF TG_TABLE_SCHEMA<>'{schema}' OR TG_TABLE_NAME<>'project' OR TG_OP<>'INSERT' THEN
        RAISE EXCEPTION 'invalid agent storage policy trigger target';
    END IF;
    INSERT INTO {schema}.agent_storage_policy(scope) VALUES(NEW.scope);
    RETURN NULL;
END;
$$;
CREATE TRIGGER project_agent_storage_policy AFTER INSERT ON {schema}.project
FOR EACH ROW EXECUTE FUNCTION {schema}.initialize_agent_storage_policy();

-- Opaque payloads are authoritative stored state. Registration provides erasure reachability;
-- these bytes are never reconstructed by extraction or included in projection generation resets.
CREATE TABLE {schema}.agent_artifact (
    scope text NOT NULL,
    artifact_id uuid NOT NULL,
    source_observation_id uuid NOT NULL UNIQUE,
    data_subject_id text NOT NULL CHECK(octet_length(data_subject_id) BETWEEN 1 AND 1024),
    kind text NOT NULL CHECK(kind IN ('state','file')),
    name text NOT NULL CHECK(octet_length(name)<=256),
    content bytea NOT NULL CHECK(octet_length(content)<=524288),
    version uuid NOT NULL,
    previous_version uuid,
    last_digest bytea NOT NULL CHECK(octet_length(last_digest)=32),
    authored_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    registration_kind text GENERATED ALWAYS AS ('agent_artifact'::text) STORED,
    registration_id text GENERATED ALWAYS AS (artifact_id::text) STORED,
    PRIMARY KEY(scope,artifact_id),
    FOREIGN KEY(scope) REFERENCES {schema}.agent_storage_policy(scope),
    UNIQUE(scope,source_observation_id,artifact_id,data_subject_id),
    FOREIGN KEY(scope,source_observation_id) REFERENCES {schema}.observation(scope,observation_id) ON DELETE CASCADE,
    FOREIGN KEY(source_observation_id,registration_kind,registration_id)
        REFERENCES {schema}.projection_dependency(source_observation_id,projection_kind,projection_id)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX agent_artifact_subject_page_idx ON {schema}.agent_artifact(scope,data_subject_id,artifact_id);
INSERT INTO {schema}.projection_kind(kind,projection_table,id_column,survives_sharing,description)
VALUES('agent_artifact','agent_artifact','artifact_id',false,'Opaque owned agent state and files; never formed');
ALTER TABLE {schema}.projection_dependency
    ADD COLUMN artifact_ref uuid GENERATED ALWAYS AS (CASE WHEN projection_kind='agent_artifact' THEN projection_id::uuid END) STORED,
    DROP CONSTRAINT dependency_one_target_chk,
    ADD CONSTRAINT dependency_one_target_chk CHECK(num_nonnulls(entity_ref,fact_ref,chunk_ref,rejected_ref,report_ref,history_ref,artifact_ref)=1),
    ADD CONSTRAINT artifact_dependency_owner_chk CHECK(artifact_ref IS NULL OR data_subject_id IS NOT NULL),
    ADD CONSTRAINT dependency_artifact_owner_fk FOREIGN KEY(scope,source_observation_id,artifact_ref,data_subject_id)
        REFERENCES {schema}.agent_artifact(scope,source_observation_id,artifact_id,data_subject_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX dependency_artifact_ref_idx ON {schema}.projection_dependency(scope,artifact_ref) WHERE artifact_ref IS NOT NULL;

CREATE FUNCTION {schema}.account_agent_artifact() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE
    delta_bytes bigint;
    delta_objects bigint;
    project_scope text;
BEGIN
    IF TG_TABLE_SCHEMA<>'{schema}' OR TG_TABLE_NAME<>'agent_artifact' OR TG_OP NOT IN ('INSERT','UPDATE','DELETE') THEN
        RAISE EXCEPTION 'invalid agent artifact accounting target';
    END IF;
    IF TG_OP='UPDATE' AND (NEW.scope,NEW.artifact_id,NEW.source_observation_id,NEW.data_subject_id,NEW.kind,NEW.created_at)
        IS DISTINCT FROM (OLD.scope,OLD.artifact_id,OLD.source_observation_id,OLD.data_subject_id,OLD.kind,OLD.created_at) THEN
        RAISE EXCEPTION 'artifact identity and ownership are immutable' USING ERRCODE='23514';
    END IF;
    IF TG_OP='DELETE' THEN
        project_scope:=OLD.scope; delta_bytes:=-octet_length(OLD.content); delta_objects:=-1;
    ELSE
        project_scope:=NEW.scope;
        delta_bytes:=octet_length(NEW.content); delta_objects:=1;
        IF TG_OP='UPDATE' THEN
            delta_bytes:=delta_bytes-octet_length(OLD.content); delta_objects:=0;
        END IF;
        IF NOT EXISTS(SELECT 1 FROM {schema}.observation o WHERE o.scope=NEW.scope
            AND o.observation_id=NEW.source_observation_id AND o.kind='artifact'
            AND o.data_subject_id=NEW.data_subject_id AND o.formed_at IS NOT NULL AND o.retention_until IS NOT NULL) THEN
            RAISE EXCEPTION 'artifact requires a completed owned source with bounded retention' USING ERRCODE='23514';
        END IF;
    END IF;
    UPDATE {schema}.agent_storage_policy SET used_bytes=used_bytes+delta_bytes,used_objects=used_objects+delta_objects
    WHERE scope=project_scope AND (TG_OP='DELETE' OR
        ((delta_bytes<=0 OR used_bytes+delta_bytes<=max_bytes)
         AND (delta_objects<=0 OR used_objects+delta_objects<=max_objects)
         AND (TG_OP='UPDATE' AND delta_bytes<=0 OR octet_length(NEW.content)<=max_object_bytes)));
    IF NOT FOUND THEN
        RAISE EXCEPTION 'agent storage capacity is full' USING ERRCODE='P0001',CONSTRAINT='agent_storage_capacity';
    END IF;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER agent_artifact_accounting BEFORE INSERT OR UPDATE OR DELETE ON {schema}.agent_artifact
FOR EACH ROW EXECUTE FUNCTION {schema}.account_agent_artifact();
REVOKE ALL ON FUNCTION {schema}.initialize_agent_storage_policy(),{schema}.account_agent_artifact() FROM PUBLIC;
REVOKE ALL ON {schema}.agent_storage_policy FROM PUBLIC;

ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK(operation IN (
    'observe','recall','erase','freshness','export','authenticate',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke',
    'formation.unpark','formation.recover','citation.resolve','record.list','record.history','record.retract','record.correct','record.assert',
    'artifact.put','artifact.get','artifact.list','artifact.delete','artifact.configure'));

-- Neither an ordinary runtime update nor an operator retry can promote opaque bytes to input
-- for extraction or move the source away from the owner its registration declares.
CREATE FUNCTION {schema}.protect_artifact_source() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF (OLD.kind='artifact' OR NEW.kind='artifact') AND
       ((OLD.kind,OLD.scope,OLD.observation_id,OLD.data_subject_id,OLD.source_role)
         IS DISTINCT FROM (NEW.kind,NEW.scope,NEW.observation_id,NEW.data_subject_id,NEW.source_role)
        OR NEW.formed_at IS NULL OR NEW.retention_until IS NULL) THEN
        RAISE EXCEPTION 'artifact source ownership and formation exclusion are immutable' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER observation_artifact_source BEFORE UPDATE ON {schema}.observation
FOR EACH ROW EXECUTE FUNCTION {schema}.protect_artifact_source();
REVOKE ALL ON FUNCTION {schema}.protect_artifact_source() FROM PUBLIC;

CREATE INDEX agent_artifact_kind_page_idx ON {schema}.agent_artifact(scope,kind,artifact_id);
CREATE INDEX agent_artifact_name_prefix_idx ON {schema}.agent_artifact(scope,name text_pattern_ops);
