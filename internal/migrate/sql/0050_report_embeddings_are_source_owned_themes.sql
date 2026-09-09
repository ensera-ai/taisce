-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- Report vectors answer a different question from message and entity vectors. Their generation identity is
-- therefore separate: equal dimensions and model names do not make two input contracts one space.
CREATE TABLE {schema}.report_embedding_generation (
    generation_id uuid PRIMARY KEY,
    scope text NOT NULL REFERENCES {schema}.project(scope) ON DELETE CASCADE,
    operation_key uuid NOT NULL,
    model_name text NOT NULL CHECK(octet_length(model_name) BETWEEN 1 AND 256),
    model_revision text NOT NULL CHECK(octet_length(model_revision) BETWEEN 1 AND 128),
    endpoint_hash text NOT NULL CHECK(endpoint_hash ~ '^[0-9a-f]{64}$'),
    identity_hash text NOT NULL CHECK(identity_hash ~ '^[0-9a-f]{64}$'),
    dimensions integer NOT NULL CHECK(dimensions BETWEEN 1 AND 4000),
    metric text NOT NULL DEFAULT 'cosine' CHECK(metric='cosine'),
    input_version text NOT NULL DEFAULT 'community-report/v1' CHECK(input_version='community-report/v1'),
    target_count bigint NOT NULL DEFAULT 0 CHECK(target_count>=0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(scope,operation_key),
    UNIQUE(scope,generation_id),
    UNIQUE(scope,generation_id,dimensions)
);
CREATE FUNCTION {schema}.immutable_report_embedding_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'report embedding generation identity is immutable'; END $$;
CREATE TRIGGER report_embedding_identity_immutable BEFORE UPDATE ON {schema}.report_embedding_generation
FOR EACH ROW EXECUTE FUNCTION {schema}.immutable_report_embedding_identity();

-- A target set materialized in the start transaction is finite even while formation continues.
-- A report inserted after this snapshot makes the generation stale rather than being silently absent.
CREATE TABLE {schema}.report_embedding_target (
    scope text NOT NULL,
    generation_id uuid NOT NULL,
    report_id uuid NOT NULL,
    PRIMARY KEY(scope,generation_id,report_id),
    FOREIGN KEY(scope,generation_id) REFERENCES {schema}.report_embedding_generation(scope,generation_id) ON DELETE CASCADE,
    FOREIGN KEY(scope,report_id) REFERENCES {schema}.community_report(scope,report_id) ON DELETE CASCADE
);
CREATE TABLE {schema}.report_embedding_build (
    scope text NOT NULL,
    generation_id uuid NOT NULL,
    state text NOT NULL DEFAULT 'building' CHECK(state IN ('building','ready','stale','cancelled','discarded')),
    after_report_id uuid,
    examined bigint NOT NULL DEFAULT 0 CHECK(examined>=0),
    stored bigint NOT NULL DEFAULT 0 CHECK(stored>=0),
    lease_id uuid,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(scope,generation_id),
    FOREIGN KEY(scope,generation_id) REFERENCES {schema}.report_embedding_generation(scope,generation_id) ON DELETE CASCADE
);
CREATE TABLE {schema}.report_embedding_active (
    scope text PRIMARY KEY REFERENCES {schema}.project(scope) ON DELETE CASCADE,
    generation_id uuid NOT NULL,
    activated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(scope,generation_id) REFERENCES {schema}.report_embedding_generation(scope,generation_id)
);

CREATE TABLE {schema}.report_embedding (
    scope text NOT NULL,
    generation_id uuid NOT NULL,
    report_id uuid NOT NULL,
    embedding_key text GENERATED ALWAYS AS (generation_id::text||'/'||report_id::text) STORED,
    input_digest bytea NOT NULL CHECK(octet_length(input_digest)=32),
    source_digest bytea NOT NULL CHECK(octet_length(source_digest)=32),
    source_count integer NOT NULL CHECK(source_count BETWEEN 1 AND 4096),
    embedding vector NOT NULL CHECK(vector_norm(embedding)>0),
    dimensions integer GENERATED ALWAYS AS (vector_dims(embedding)) STORED,
    PRIMARY KEY(scope,generation_id,report_id),
    UNIQUE(scope,generation_id,embedding_key),
    FOREIGN KEY(scope,generation_id,dimensions) REFERENCES {schema}.report_embedding_generation(scope,generation_id,dimensions),
    FOREIGN KEY(scope,generation_id,report_id) REFERENCES {schema}.report_embedding_target(scope,generation_id,report_id) ON DELETE CASCADE
) PARTITION BY LIST(generation_id);

-- One row per contributing observation makes aggregate provenance inspectable and bounded. The
-- circular registration relationship is deferred so vector, sources and registry rows are atomic.
CREATE TABLE {schema}.report_embedding_source (
    scope text NOT NULL,
    generation_id uuid NOT NULL,
    report_id uuid NOT NULL,
    source_observation_id uuid NOT NULL,
    embedding_key text GENERATED ALWAYS AS (generation_id::text||'/'||report_id::text) STORED,
    registration_kind text GENERATED ALWAYS AS ('report_embedding'::text) STORED,
    PRIMARY KEY(scope,generation_id,report_id,source_observation_id),
    FOREIGN KEY(scope,generation_id,report_id) REFERENCES {schema}.report_embedding(scope,generation_id,report_id) ON DELETE CASCADE,
    FOREIGN KEY(scope,source_observation_id) REFERENCES {schema}.observation(scope,observation_id) ON DELETE CASCADE,
    FOREIGN KEY(source_observation_id,registration_kind,embedding_key)
        REFERENCES {schema}.projection_dependency(source_observation_id,projection_kind,projection_id)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX report_embedding_source_observation_idx ON {schema}.report_embedding_source(scope,source_observation_id);

INSERT INTO {schema}.projection_kind(kind,projection_table,id_column,survives_sharing,description)
VALUES('report_embedding','report_embedding','embedding_key',false,
       'A thematic vector over one community report. Every underlying observation owns it, so removing any contributor deletes both report and vector.');

ALTER TABLE {schema}.projection_dependency
    ADD COLUMN report_embedding_generation_ref uuid GENERATED ALWAYS AS
        (CASE WHEN projection_kind='report_embedding' THEN split_part(projection_id,'/',1)::uuid END) STORED,
    ADD COLUMN report_embedding_report_ref uuid GENERATED ALWAYS AS
        (CASE WHEN projection_kind='report_embedding' THEN split_part(projection_id,'/',2)::uuid END) STORED,
    DROP CONSTRAINT dependency_one_target_chk,
    ADD CONSTRAINT dependency_one_target_chk CHECK(num_nonnulls(entity_ref,fact_ref,chunk_ref,rejected_ref,report_ref,history_ref,artifact_ref,embedding_generation_ref,entity_embedding_generation_ref,report_embedding_generation_ref)=1),
    ADD CONSTRAINT dependency_report_embedding_source_fk
        FOREIGN KEY(scope,report_embedding_generation_ref,report_embedding_report_ref,source_observation_id)
        REFERENCES {schema}.report_embedding_source(scope,generation_id,report_id,source_observation_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX dependency_report_embedding_ref_idx ON {schema}.projection_dependency
    (scope,report_embedding_generation_ref,report_embedding_report_ref)
    WHERE report_embedding_generation_ref IS NOT NULL;

-- Any source loss invalidates an aggregate vector. When the parent vector initiated the cascade,
-- the second delete is a harmless no-op; when an observation initiated it, this removes the parent.
CREATE FUNCTION {schema}.invalidate_report_embedding_source() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    DELETE FROM {schema}.report_embedding
      WHERE scope=OLD.scope AND generation_id=OLD.generation_id AND report_id=OLD.report_id;
    RETURN OLD;
END $$;
CREATE TRIGGER report_embedding_source_removed AFTER DELETE ON {schema}.report_embedding_source
FOR EACH ROW EXECUTE FUNCTION {schema}.invalidate_report_embedding_source();

CREATE FUNCTION {schema}.mark_report_embedding_incomplete() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    UPDATE {schema}.report_embedding_build
       SET state='building',after_report_id=NULL,lease_id=NULL,updated_at=clock_timestamp()
     WHERE scope=OLD.scope AND generation_id=OLD.generation_id AND state='ready';
    RETURN OLD;
END $$;
CREATE TRIGGER report_embedding_removed AFTER DELETE ON {schema}.report_embedding
FOR EACH ROW EXECUTE FUNCTION {schema}.mark_report_embedding_incomplete();

-- Publication and report mutation use one per-report lock, so a provider result computed from stale
-- prose is refused or removed. Every report-set change also makes finite generations stale.
CREATE FUNCTION {schema}.lock_report_embedding_change() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(OLD.scope||':report-embedding:'||OLD.report_id::text,0));
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER report_embedding_change_lock BEFORE UPDATE OR DELETE ON {schema}.community_report
FOR EACH ROW EXECUTE FUNCTION {schema}.lock_report_embedding_change();

CREATE FUNCTION {schema}.stale_report_embedding_snapshots() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
DECLARE target_scope text; target_id uuid;
BEGIN
    IF TG_OP='DELETE' THEN
        target_scope := OLD.scope; target_id := OLD.report_id;
    ELSE
        target_scope := NEW.scope; target_id := NEW.report_id;
    END IF;
    PERFORM 1 FROM {schema}.project WHERE scope=target_scope FOR UPDATE;
    UPDATE {schema}.report_embedding_build SET state='stale',lease_id=NULL,updated_at=clock_timestamp()
     WHERE scope=target_scope AND state<>'discarded';
    IF TG_OP='UPDATE' THEN
        DELETE FROM {schema}.report_embedding WHERE scope=target_scope AND report_id=target_id;
    END IF;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER report_embedding_report_changed AFTER INSERT OR UPDATE OR DELETE ON {schema}.community_report
FOR EACH ROW EXECUTE FUNCTION {schema}.stale_report_embedding_snapshots();
REVOKE ALL ON FUNCTION {schema}.immutable_report_embedding_identity() FROM PUBLIC;
REVOKE ALL ON FUNCTION {schema}.invalidate_report_embedding_source() FROM PUBLIC;
REVOKE ALL ON FUNCTION {schema}.mark_report_embedding_incomplete() FROM PUBLIC;
REVOKE ALL ON FUNCTION {schema}.lock_report_embedding_change() FROM PUBLIC;
REVOKE ALL ON FUNCTION {schema}.stale_report_embedding_snapshots() FROM PUBLIC;

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
    'report_embedding.start','report_embedding.build','report_embedding.activate','report_embedding.cancel','report_embedding.prune','report_embedding.repair','report_embedding.search'));
