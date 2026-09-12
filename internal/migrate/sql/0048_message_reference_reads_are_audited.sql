-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- A UUID-only reader requires an unambiguous authoritative identity and a direct lookup index.
ALTER TABLE {schema}.turn_message ADD CONSTRAINT message_chunk_lookup_unique UNIQUE(chunk_id);

-- Message references are read under the same content-free credential audit policy as citations.
ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK(operation IN (
    'observe','recall','erase','freshness','export','authenticate',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke',
    'formation.unpark','formation.recover','formation.rebuild','formation.rebuild.start','formation.rebuild.cancel',
    'citation.resolve','message.get','record.list','record.history','record.retract','record.correct','record.assert',
    'artifact.put','artifact.get','artifact.list','artifact.delete','artifact.configure',
    'subject.register','subject.get','subject.list','subject.update','entity.list','entity.get',
    'embedding.start','embedding.build','embedding.activate','embedding.cancel','embedding.prune','embedding.repair','embedding.search'));
