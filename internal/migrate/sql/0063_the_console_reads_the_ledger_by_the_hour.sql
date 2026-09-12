-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- ── The console reads the ledger by the hour ─────────────────────────────────────────────────
--
-- The operator's console counts what the ledger holds for a window — how many operations, of which
-- kinds, allowed or refused, per project — and draws it. That is a read of the ledger, and a read an
-- operator makes is on the ledger like every other one, so the ledger learns one more name for it:
-- `audit.activity`. Counts only: the ledger holds no words, and a count of its rows holds fewer.
--
-- The window is served by `audit_time_idx` (0013), so a week of activity is an index range rather
-- than a scan of everything ever recorded.
ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK(operation IN (
    'observe','recall','erase','freshness','export','authenticate',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke',
    'formation.unpark','formation.recover','formation.rebuild','formation.rebuild.start','formation.rebuild.cancel',
    'citation.resolve','message.get','record.list','record.history','record.retract','record.correct','record.assert',
    'feedback.record','feedback.list','feedback.promote',
    'artifact.put','artifact.get','artifact.list','artifact.delete','artifact.configure',
    'subject.register','subject.get','subject.list','subject.update','entity.list','entity.get','entity.purge',
    'notification.register','notification.list','notification.disable','notification.deliveries',
    'embedding.start','embedding.build','embedding.activate','embedding.cancel','embedding.prune','embedding.repair','embedding.search',
    'entity_embedding.start','entity_embedding.build','entity_embedding.activate','entity_embedding.cancel','entity_embedding.prune','entity_embedding.repair','entity_embedding.search',
    'report_embedding.start','report_embedding.build','report_embedding.activate','report_embedding.cancel','report_embedding.prune','report_embedding.repair','report_embedding.search',
    'context.assemble',
    'project.list','credential.list','refusal.summary','erasure.list','formation.status','formation.parked','audit.seal','audit.verify',
    'audit.activity'));
