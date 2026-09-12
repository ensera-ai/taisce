-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- What a retention sweep deleted went to a log line and nowhere else. An erasure leaves a receipt
-- with a counted residual, because "we deleted it" is a claim somebody has to be able to check
-- later; expiry deletes the same kinds of thing on a schedule nobody watches, and left nothing to
-- check at all. The receipt is written in the same transaction as the deletes, so a sweep
-- cannot delete without recording what it deleted.
CREATE TABLE {schema}.retention_sweep (
    sweep_id     uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    scope        text        NOT NULL,
    swept_at     timestamptz NOT NULL DEFAULT now(),
    -- How many expired turns this sweep took, and what went per projection kind: the same shape an
    -- erasure receipt reports, so the two read the same way.
    observations integer     NOT NULL CHECK (observations >= 0),
    deleted      jsonb       NOT NULL
);

-- The question an operator asks is "what has expiry done to this project lately", so the index
-- leads with the project and orders by when.
CREATE INDEX retention_sweep_scope_idx ON {schema}.retention_sweep (scope, swept_at DESC);

COMMENT ON TABLE {schema}.retention_sweep IS
    'What each retention sweep deleted, written in the transaction that deleted it. Counts only: '
    'no identifiers of what expired, because the point of expiry is that it is gone.';

-- Two operations the ledger can now name: the sweep, recorded by the deployment itself, and an
-- operator setting a project''s retention.
ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK(operation IN (
    'observe','recall','erase','freshness','export','authenticate',
    'project.create','project.suspend','project.resume','project.retention','credential.issue','credential.revoke',
    'formation.unpark','formation.recover','formation.rebuild','formation.rebuild.start','formation.rebuild.cancel',
    'citation.resolve','message.get','record.list','record.history','record.retract','record.correct','record.assert',
    'feedback.record','feedback.list','feedback.promote',
    'artifact.put','artifact.get','artifact.list','artifact.delete','artifact.configure',
    'subject.register','subject.get','subject.list','subject.update','entity.list','entity.get','entity.purge',
    'notification.register','notification.list','notification.disable','notification.deliveries',
    'embedding.start','embedding.build','embedding.activate','embedding.cancel','embedding.prune','embedding.repair','embedding.search',
    'entity_embedding.start','entity_embedding.build','entity_embedding.activate','entity_embedding.cancel','entity_embedding.prune','entity_embedding.repair','entity_embedding.search',
    'report_embedding.start','report_embedding.build','report_embedding.activate','report_embedding.cancel','report_embedding.prune','report_embedding.repair','report_embedding.search',
    'context.assemble','retention.sweep',
    'project.list','credential.list','refusal.summary','erasure.list','formation.status','formation.parked','audit.seal','audit.verify',
    'audit.activity'));
