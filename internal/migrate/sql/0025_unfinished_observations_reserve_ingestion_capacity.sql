-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- These are operational reservations, not inferred memory. Source deletion cascades the
-- reservation; its deferred release participates in the same commit as erasure or retention.
CREATE TABLE {schema}.ingestion_budget (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    max_pending bigint NOT NULL DEFAULT 4096 CHECK (max_pending BETWEEN 1 AND 1000000),
    max_pending_per_project bigint NOT NULL DEFAULT 512
        CHECK (max_pending_per_project BETWEEN 1 AND max_pending),
    pending bigint NOT NULL DEFAULT 0 CHECK (pending >= 0)
);
CREATE TABLE {schema}.ingestion_project_usage (
    scope text PRIMARY KEY,
    pending bigint NOT NULL CHECK (pending >= 0)
);
CREATE TABLE {schema}.ingestion_reservation (
    observation_id uuid PRIMARY KEY,
    scope text NOT NULL,
    FOREIGN KEY (scope,observation_id) REFERENCES {schema}.observation(scope,observation_id)
        ON DELETE CASCADE
);

-- Preserve an existing backlog even when it exceeds the new defaults. New writes refuse until
-- it drains below the configured limit; an upgrade never discards accepted observations.
INSERT INTO {schema}.ingestion_reservation(observation_id,scope)
SELECT observation_id,scope FROM {schema}.observation WHERE kind='turn' AND formed_at IS NULL;
INSERT INTO {schema}.ingestion_budget(singleton,pending)
SELECT true,count(*) FROM {schema}.ingestion_reservation;
INSERT INTO {schema}.ingestion_project_usage(scope,pending)
SELECT scope,count(*) FROM {schema}.ingestion_reservation GROUP BY scope;

-- Run at transaction end, after the ordinary write path has acquired its observation/watermark
-- locks. The global counter update serializes only the final reservation and forces stale
-- repeatable-read snapshots to fail with a serialization error rather than oversubscribe.
CREATE FUNCTION {schema}.sync_ingestion_reservation() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE
    project_scope text;
    project_limit bigint;
BEGIN
    IF TG_TABLE_SCHEMA <> '{schema}' OR TG_TABLE_NAME <> 'observation'
       OR TG_OP NOT IN ('INSERT','UPDATE') THEN
        RAISE EXCEPTION 'invalid ingestion reservation trigger target';
    END IF;
    SELECT o.scope INTO project_scope FROM {schema}.observation o
     WHERE o.observation_id=NEW.observation_id AND o.kind='turn' AND o.formed_at IS NULL;
    IF NOT FOUND THEN
        DELETE FROM {schema}.ingestion_reservation WHERE observation_id=NEW.observation_id;
        RETURN NULL;
    END IF;
    IF EXISTS(SELECT 1 FROM {schema}.ingestion_reservation WHERE observation_id=NEW.observation_id) THEN
        RETURN NULL;
    END IF;
    UPDATE {schema}.ingestion_budget SET pending=pending+1
     WHERE singleton AND pending<max_pending RETURNING max_pending_per_project INTO project_limit;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'unfinished observation capacity is full'
            USING ERRCODE='P0001',CONSTRAINT='ingestion_backlog_capacity';
    END IF;
    INSERT INTO {schema}.ingestion_project_usage(scope,pending) VALUES(project_scope,1)
    ON CONFLICT(scope) DO UPDATE SET pending={schema}.ingestion_project_usage.pending+1
      WHERE {schema}.ingestion_project_usage.pending<project_limit;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'unfinished observation capacity is full'
            USING ERRCODE='P0001',CONSTRAINT='ingestion_backlog_capacity';
    END IF;
    INSERT INTO {schema}.ingestion_reservation(observation_id,scope) VALUES(NEW.observation_id,project_scope);
    RETURN NULL;
END;
$$;

CREATE FUNCTION {schema}.release_ingestion_reservation() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF TG_TABLE_SCHEMA <> '{schema}' OR TG_TABLE_NAME <> 'ingestion_reservation' OR TG_OP <> 'DELETE' THEN
        RAISE EXCEPTION 'invalid ingestion release trigger target';
    END IF;
    UPDATE {schema}.ingestion_budget SET pending=pending-1 WHERE singleton AND pending>0;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'ingestion counter is inconsistent' USING ERRCODE='23514';
    END IF;
    UPDATE {schema}.ingestion_project_usage SET pending=pending-1 WHERE scope=OLD.scope AND pending>0;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'project ingestion counter is inconsistent' USING ERRCODE='23514';
    END IF;
    DELETE FROM {schema}.ingestion_project_usage WHERE scope=OLD.scope AND pending=0;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER observation_ingestion_reservation
AFTER INSERT OR UPDATE OF formed_at,scope,kind ON {schema}.observation
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION {schema}.sync_ingestion_reservation();
CREATE CONSTRAINT TRIGGER ingestion_reservation_release
AFTER DELETE ON {schema}.ingestion_reservation
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
EXECUTE FUNCTION {schema}.release_ingestion_reservation();

-- Runtime DML fires the installed triggers without receiving the right to invoke privileged
-- functions from an attacker-controlled table. Every relation is explicitly schema-qualified.
REVOKE ALL ON FUNCTION {schema}.sync_ingestion_reservation() FROM PUBLIC;
REVOKE ALL ON FUNCTION {schema}.release_ingestion_reservation() FROM PUBLIC;
REVOKE ALL ON {schema}.ingestion_budget,{schema}.ingestion_project_usage,{schema}.ingestion_reservation FROM PUBLIC;
