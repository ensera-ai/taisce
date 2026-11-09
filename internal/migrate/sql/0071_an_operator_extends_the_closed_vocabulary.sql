-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- ── Extending the vocabulary is an operator's operation, so the ledger has to be able to name it ──
--
--  The relation vocabulary is the one surface that fits a deployment to its own domain: a deployment
--  whose conversations are about clinical visits or shipments meets relations the seeded set cannot
--  name, and every one of them is refused as unmapped. Until now the rows were reachable only by
--  connecting to the database as an identity the serving processes deliberately do not hold, so the
--  adjustment that makes this product fit a domain was priced at one release per deployment.
--
--  Two operations now reach it — adding a relation, and listing what the instance admits — and both
--  are operator operations on the management surface rather than memory operations. The ledger's
--  check is what decides whether an operation may be served at all: a route whose name the check
--  refuses would have its row rejected, the rejection is logged and swallowed by design, and the
--  operation would succeed with no account of it. So the name arrives here, where the ledger learns
--  it, before any handler can be registered under it.
--
--  ── WHY LISTING IS RECORDED, AND NOT ONLY ADDING ─────────────────────────────────────────────
--
--  Reading the vocabulary is a read of policy, not of memory, and it is recorded for the same reason
--  every other operator read is: the question an operator has to be able to answer afterwards is
--  "who looked at what this instance admits, and when" — which is how a change nobody announced is
--  noticed. The read is given its own name rather than sharing the write's, because an addition and
--  an inspection would otherwise be indistinguishable in the account of what happened.
--
--  ── WHAT THIS DOES NOT ADD ───────────────────────────────────────────────────────────────────
--
--  No grant, and no name for removing or renaming a relation.
--
--  There is no grant to add: the management surface holds the administrative connection, because
--  creating a project is DDL, and that identity owns this namespace. A GRANT to an owner changes
--  nothing, and the boundary that does the work here is the one already in place — the memory
--  identity holds SELECT on this table and no write at all, which is what keeps a statement shaped
--  by hostile input away from the policy governing the next statement.
--
--  There is no removal or rename, by design rather than by omission. `fact.predicate` references
--  this table with RESTRICT, so retiring a relation facts still use fails loudly instead of
--  deleting memory; and `fact_cardinality_matches_vocabulary` cascades an update, so a rename would
--  rewrite the relation on every fact that carries it and leave one relation with two spellings in
--  its history — which is the split the closed set exists to prevent. An operator who wants a
--  relation gone wants the facts gone, and that is an erasure, which counts what it removed.

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
    'audit.activity','vocabulary.add','vocabulary.list'));
