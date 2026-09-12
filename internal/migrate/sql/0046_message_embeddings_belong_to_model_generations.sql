-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- Unstamped vectors cannot establish a model space. Retained messages provide their rebuild input.
ALTER TABLE {schema}.chunk DROP COLUMN embedding;
ALTER TABLE {schema}.chunk ADD CONSTRAINT chunk_source_identity_uniq UNIQUE(scope,source_observation_id,chunk_id);

-- Model identity and activation are operator policy, separate from worker progress.
CREATE TABLE {schema}.embedding_generation (
    generation_id uuid PRIMARY KEY,
    scope text NOT NULL REFERENCES {schema}.project(scope) ON DELETE CASCADE,
    operation_key uuid NOT NULL,
    model_name text NOT NULL CHECK(octet_length(model_name) BETWEEN 1 AND 256),
    model_revision text NOT NULL CHECK(octet_length(model_revision) BETWEEN 1 AND 128),
    endpoint_hash text NOT NULL CHECK(endpoint_hash ~ '^[0-9a-f]{64}$'),
    identity_hash text NOT NULL CHECK(identity_hash ~ '^[0-9a-f]{64}$'),
    dimensions integer NOT NULL CHECK(dimensions BETWEEN 1 AND 4000),
    metric text NOT NULL DEFAULT 'cosine' CHECK(metric='cosine'),
    input_version text NOT NULL DEFAULT 'message-text/v1' CHECK(input_version='message-text/v1'),
    through_offset bigint NOT NULL CHECK(through_offset>=-1),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(scope,operation_key),
    UNIQUE(scope,generation_id),
    UNIQUE(scope,generation_id,dimensions)
);
CREATE FUNCTION {schema}.immutable_embedding_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'embedding generation identity is immutable'; END $$;
CREATE TRIGGER embedding_identity_immutable BEFORE UPDATE ON {schema}.embedding_generation
FOR EACH ROW EXECUTE FUNCTION {schema}.immutable_embedding_identity();

CREATE TABLE {schema}.embedding_build (
    scope text NOT NULL,
    generation_id uuid NOT NULL,
    state text NOT NULL DEFAULT 'building' CHECK(state IN ('building','ready','cancelled','discarded')),
    after_offset bigint NOT NULL DEFAULT -1 CHECK(after_offset>=-1),
    after_ordinal integer NOT NULL DEFAULT -1 CHECK(after_ordinal>=-1),
    examined bigint NOT NULL DEFAULT 0 CHECK(examined>=0),
    stored bigint NOT NULL DEFAULT 0 CHECK(stored>=0),
    lease_id uuid,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(scope,generation_id),
    FOREIGN KEY(scope,generation_id) REFERENCES {schema}.embedding_generation(scope,generation_id) ON DELETE CASCADE,
    CHECK((after_offset=-1)=(after_ordinal=-1))
);
CREATE TABLE {schema}.embedding_active (
    scope text PRIMARY KEY REFERENCES {schema}.project(scope) ON DELETE CASCADE,
    generation_id uuid NOT NULL,
    activated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(scope,generation_id) REFERENCES {schema}.embedding_generation(scope,generation_id)
);

CREATE TABLE {schema}.message_embedding (
    scope text NOT NULL,
    generation_id uuid NOT NULL,
    chunk_id uuid NOT NULL,
    source_observation_id uuid NOT NULL,
    embedding_key text NOT NULL,
    input_digest bytea NOT NULL CHECK(octet_length(input_digest)=32),
    embedding vector NOT NULL CHECK(vector_norm(embedding)>0),
    dimensions integer GENERATED ALWAYS AS (vector_dims(embedding)) STORED,
    registration_kind text GENERATED ALWAYS AS ('message_embedding'::text) STORED,
    PRIMARY KEY(scope,generation_id,chunk_id),
    UNIQUE(scope,generation_id,source_observation_id,chunk_id),
    CHECK(embedding_key=generation_id::text||'/'||chunk_id::text),
    FOREIGN KEY(scope,generation_id,dimensions) REFERENCES {schema}.embedding_generation(scope,generation_id,dimensions),
    FOREIGN KEY(scope,source_observation_id,chunk_id) REFERENCES {schema}.chunk(scope,source_observation_id,chunk_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(scope,source_observation_id) REFERENCES {schema}.observation(scope,observation_id) ON DELETE CASCADE,
    FOREIGN KEY(source_observation_id,registration_kind,embedding_key)
        REFERENCES {schema}.projection_dependency(source_observation_id,projection_kind,projection_id)
        DEFERRABLE INITIALLY DEFERRED
) PARTITION BY LIST(generation_id);
CREATE INDEX message_embedding_source_idx ON {schema}.message_embedding(scope,source_observation_id);

INSERT INTO {schema}.projection_kind(kind,projection_table,id_column,survives_sharing,description)
VALUES('message_embedding','message_embedding','embedding_key',false,'Model-identified vectors of retained messages, owned by the exact source in every generation');
ALTER TABLE {schema}.projection_dependency
    ADD COLUMN embedding_generation_ref uuid GENERATED ALWAYS AS (CASE WHEN projection_kind='message_embedding' THEN split_part(projection_id,'/',1)::uuid END) STORED,
    ADD COLUMN embedding_chunk_ref uuid GENERATED ALWAYS AS (CASE WHEN projection_kind='message_embedding' THEN split_part(projection_id,'/',2)::uuid END) STORED,
    DROP CONSTRAINT dependency_one_target_chk,
    ADD CONSTRAINT dependency_one_target_chk CHECK(num_nonnulls(entity_ref,fact_ref,chunk_ref,rejected_ref,report_ref,history_ref,artifact_ref,embedding_generation_ref)=1),
    ADD CONSTRAINT dependency_message_embedding_fk FOREIGN KEY(scope,embedding_generation_ref,source_observation_id,embedding_chunk_ref)
        REFERENCES {schema}.message_embedding(scope,generation_id,source_observation_id,chunk_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX dependency_embedding_ref_idx ON {schema}.projection_dependency(scope,embedding_generation_ref,embedding_chunk_ref)
    WHERE embedding_generation_ref IS NOT NULL;

ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK(operation IN (
    'observe','recall','erase','freshness','export','authenticate',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke',
    'formation.unpark','formation.recover','formation.rebuild','formation.rebuild.start','formation.rebuild.cancel',
    'citation.resolve','record.list','record.history','record.retract','record.correct','record.assert',
    'artifact.put','artifact.get','artifact.list','artifact.delete','artifact.configure',
    'subject.register','subject.get','subject.list','subject.update','entity.list','entity.get',
    'embedding.start','embedding.build','embedding.activate','embedding.cancel','embedding.prune','embedding.repair','embedding.search'));
