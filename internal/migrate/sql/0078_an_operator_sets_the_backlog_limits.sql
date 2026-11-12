-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- ── Setting how many turns may wait is an operator's operation, so the ledger has to name it ──
--
-- The limits on the unformed backlog decide what a bulk ingest does: past them a write is refused
-- until formation makes room. They were changed by an UPDATE in a database session, as the
-- administrative role — an act with no operator's name on it and no entry anywhere.
--
-- They are now set on the management surface. The ledger's check is what decides whether an
-- operation may be served at all: a route whose name the check does not hold cannot record itself,
-- and an operation that cannot be recorded is refused rather than served unaccounted for. So the
-- name arrives here, where the ledger learns it, before any process serves the route.
--
-- The table that holds the limits is not touched. Its own constraints already say what a limit may
-- be, and they refuse a value set through the surface exactly as they refuse one set by hand.
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
    'audit.activity','vocabulary.add','vocabulary.list','backlog.limits'));
