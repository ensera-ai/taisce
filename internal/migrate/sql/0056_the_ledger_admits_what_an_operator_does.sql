-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- The management surface is a set of operations an operator performs on the instance, and
-- every one of them is on the ledger with the operator credential as its principal. The check that
-- names what the ledger admits learns them here, where they arrive, so no management route can be
-- served unrecorded.

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
    'report_embedding.start','report_embedding.build','report_embedding.activate','report_embedding.cancel','report_embedding.prune','report_embedding.repair','report_embedding.search',
    'context.assemble',
    'project.list','credential.list','refusal.summary','erasure.list','formation.status','formation.parked','audit.seal','audit.verify'));
