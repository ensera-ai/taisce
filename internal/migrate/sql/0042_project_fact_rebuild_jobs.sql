-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- A checkpoint holds log positions and aggregate progress, not another copy of source content or
-- source identifiers. Erasure can remove a pending observation without leaving a personal link.
CREATE TABLE {schema}.fact_rebuild_job (
    scope text NOT NULL,
    job_id uuid NOT NULL,
    extractor_version text NOT NULL CHECK(extractor_version ~ '^extract/v2:[a-f0-9]{64}$'),
    through_offset bigint NOT NULL CHECK(through_offset>=-1),
    after_offset bigint NOT NULL DEFAULT -1 CHECK(after_offset>=-1),
    pending_offset bigint,
    sources_rebuilt bigint NOT NULL DEFAULT 0 CHECK(sources_rebuilt>=0),
    sources_skipped bigint NOT NULL DEFAULT 0 CHECK(sources_skipped>=0),
    status text NOT NULL DEFAULT 'active' CHECK(status IN ('active','completed','cancelled')),
    cancel_requested boolean NOT NULL DEFAULT false,
    lease_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(scope,job_id),
    CHECK(after_offset<=through_offset),
    CHECK(pending_offset IS NULL OR (pending_offset>after_offset AND pending_offset<=through_offset)),
    CHECK(status='active' OR pending_offset IS NULL)
);
CREATE UNIQUE INDEX one_active_fact_rebuild ON {schema}.fact_rebuild_job(scope) WHERE status='active';

ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK(operation IN (
    'observe','recall','erase','freshness','export','authenticate',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke',
    'formation.unpark','formation.recover','formation.rebuild','formation.rebuild.start','formation.rebuild.cancel',
    'citation.resolve','record.list','record.history','record.retract','record.correct','record.assert',
    'artifact.put','artifact.get','artifact.list','artifact.delete','artifact.configure',
    'subject.register','subject.get','subject.list','subject.update'));
