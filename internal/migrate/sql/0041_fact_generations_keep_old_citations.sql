-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- Rows exist only for committed cutovers. Prepared model output lives in bounded process memory;
-- a crash before commit leaves the old generation readable and a retry can prepare it again.
CREATE TABLE {schema}.fact_generation (
    scope text NOT NULL,
    generation_id uuid NOT NULL,
    source_observation_id uuid NOT NULL,
    operation_key uuid NOT NULL,
    extractor_version text NOT NULL CHECK(extractor_version ~ '^extract/v2:[a-f0-9]{64}$'),
    previous_extractor_version text,
    previous_generation_id uuid,
    applied_at timestamptz NOT NULL,
    messages_read integer NOT NULL CHECK(messages_read BETWEEN 1 AND 256),
    facts_asserted integer NOT NULL CHECK(facts_asserted BETWEEN 0 AND 100),
    facts_retired integer NOT NULL CHECK(facts_retired BETWEEN 0 AND 100),
    claims_rejected integer NOT NULL CHECK(claims_rejected BETWEEN 0 AND 100),
    claims_retracted integer NOT NULL CHECK(claims_retracted BETWEEN 0 AND 100),
    claims_refused_by_role integer NOT NULL CHECK(claims_refused_by_role BETWEEN 0 AND 100),
    principal_id uuid NOT NULL,
    PRIMARY KEY(scope,generation_id),
    UNIQUE(scope,source_observation_id,operation_key),
    UNIQUE(scope,source_observation_id,generation_id),
    FOREIGN KEY(scope,source_observation_id) REFERENCES {schema}.observation(scope,observation_id) ON DELETE CASCADE
);

-- Associations survive derived fact loss because the recoverable ID is backed by its receipt.
-- Causal erasure of a receipt removes its association as well as its recorded state.
ALTER TABLE {schema}.fact_receipt ADD CONSTRAINT receipt_generation_source_uniq UNIQUE(scope,fact_id,source_observation_id);
CREATE TABLE {schema}.fact_generation_record (
    scope text NOT NULL,
    source_observation_id uuid NOT NULL,
    generation_id uuid NOT NULL,
    fact_id uuid NOT NULL,
    disposition text NOT NULL CHECK(disposition IN ('admitted','retired')),
    PRIMARY KEY(scope,generation_id,disposition,fact_id),
    FOREIGN KEY(scope,source_observation_id,generation_id) REFERENCES {schema}.fact_generation(scope,source_observation_id,generation_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(scope,fact_id,source_observation_id) REFERENCES {schema}.fact_receipt(scope,fact_id,source_observation_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
);
CREATE UNIQUE INDEX generation_record_fact_idx ON {schema}.fact_generation_record(scope,fact_id,disposition);
CREATE INDEX generation_source_time_idx ON {schema}.fact_generation(scope,source_observation_id,applied_at DESC);

ALTER TABLE {schema}.source_extraction ADD COLUMN generation_id uuid;
ALTER TABLE {schema}.source_extraction ADD CONSTRAINT source_extraction_generation_fk
    FOREIGN KEY(scope,source_observation_id,generation_id) REFERENCES {schema}.fact_generation(scope,source_observation_id,generation_id)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK(operation IN (
    'observe','recall','erase','freshness','export','authenticate',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke',
    'formation.unpark','formation.recover','formation.rebuild','citation.resolve','record.list','record.history','record.retract','record.correct','record.assert',
    'artifact.put','artifact.get','artifact.list','artifact.delete','artifact.configure',
    'subject.register','subject.get','subject.list','subject.update'));
