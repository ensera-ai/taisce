-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- ── A sealed range can leave the ledger, and nothing else can ────────────────────────────────────
--
-- The ledger refused every DELETE, from every identity, for good. That is the right rule for a
-- record and it makes a table that only grows: with traffic, not with content, and for as long as
-- the instance lives. Verification recomputed the chain from the first seal, so its cost grew with
-- the same table. An operator's only way to make room was to disable the triggers as the owner —
-- the one act the ledger exists to make visible, done invisibly.
--
-- ── WHAT CHANGES ────────────────────────────────────────────────────────────────────────────────
--
-- A range of entries that is sealed may be exported with its seals and then removed. What stays
-- behind is a CHECKPOINT: the seal the export ran through, the chain's digest at that seal, how
-- much left, and the digest of the file it left in. The chain continues from the checkpoint as it
-- did from the seal, so the entries that remain verify exactly as before, and the entries that
-- left verify from their file against the same digests.
--
-- ── WHAT THE DATABASE HOLDS TO, WHOEVER ASKS ────────────────────────────────────────────────────
--
-- A rule in a command is a convention. These are in the schema:
--
--   * An entry or a seal may be deleted only if a checkpoint covers it. Without a checkpoint the
--     ledger is append-only exactly as it was, and the refusal says the same words.
--   * A checkpoint may only name a seal that exists, with the digest that seal holds, and must run
--     further than the checkpoint before it. So nothing unsealed can be covered: an entry no seal
--     has reached has an identifier above every seal's.
--   * A checkpoint is never changed and never removed.
--   * The memory role writes none of this. It holds INSERT on the ledger and nothing on a
--     checkpoint (memorywrites.go), so the process that serves traffic cannot make room for itself.
--
-- ── WHAT IT DOES NOT HOLD TO ────────────────────────────────────────────────────────────────────
--
-- That the file exists, or is the file the checkpoint names. The database cannot see a file. The
-- command writes it and makes it durable before the checkpoint is written, and the checkpoint
-- carries the file's digest so a file can later be shown to be the one — but an operator who
-- discards the file has discarded those entries, and the ledger will say only that they left.
--
-- And, as before, an owner can disable a trigger. The head digest written to the process log at
-- every seal is what a rebuilt chain cannot agree with; an archive verifies against the same value.

CREATE TABLE {schema}.audit_checkpoint (
    checkpoint_id  bigserial   PRIMARY KEY,
    through_seal   bigint      NOT NULL,
    through_entry  bigint      NOT NULL,
    -- The chain's digest at through_seal. The next seal chains to this when no seal remains, and
    -- verification starts from it.
    digest         bytea       NOT NULL,
    entries        bigint      NOT NULL,
    seals          integer     NOT NULL,
    -- SHA-256 of the file the range was exported to.
    archive_digest bytea       NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_checkpoint_digests_chk CHECK (octet_length(digest) = 32 AND octet_length(archive_digest) = 32),
    CONSTRAINT audit_checkpoint_counts_chk CHECK (entries > 0 AND seals > 0)
);

CREATE FUNCTION {schema}.audit_checkpoint_names_a_seal() RETURNS trigger AS $$
DECLARE
    last_entry bigint;
    held bytea;
    before bigint;
BEGIN
    SELECT s.to_entry, s.digest INTO last_entry, held FROM {schema}.audit_seal s WHERE s.seal_id = NEW.through_seal;
    IF last_entry IS NULL OR last_entry <> NEW.through_entry OR held <> NEW.digest THEN
        RAISE EXCEPTION 'a checkpoint names a seal the ledger holds, by its last entry and its digest'
            USING HINT = 'nothing that is not sealed can be covered by a checkpoint';
    END IF;
    SELECT max(c.through_seal) INTO before FROM {schema}.audit_checkpoint c;
    IF before IS NOT NULL AND NEW.through_seal <= before THEN
        RAISE EXCEPTION 'a checkpoint runs further than the one before it';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_checkpoint_names_a_seal BEFORE INSERT ON {schema}.audit_checkpoint
    FOR EACH ROW EXECUTE FUNCTION {schema}.audit_checkpoint_names_a_seal();
CREATE TRIGGER audit_checkpoint_no_update BEFORE UPDATE ON {schema}.audit_checkpoint
    FOR EACH ROW EXECUTE FUNCTION {schema}.audit_is_append_only();
CREATE TRIGGER audit_checkpoint_no_delete BEFORE DELETE ON {schema}.audit_checkpoint
    FOR EACH ROW EXECUTE FUNCTION {schema}.audit_is_append_only();
CREATE TRIGGER audit_checkpoint_no_truncate BEFORE TRUNCATE ON {schema}.audit_checkpoint
    FOR EACH STATEMENT EXECUTE FUNCTION {schema}.audit_is_append_only();

-- The same refusal, in the same words, unless a checkpoint covers the row. UPDATE and TRUNCATE
-- keep the unconditional function: an archived row leaves whole, it is never altered, and a range
-- leaves by its identifiers and never as "everything".
CREATE FUNCTION {schema}.audit_leaves_only_when_archived() RETURNS trigger AS $$
DECLARE
    covered bigint;
BEGIN
    IF TG_TABLE_NAME = 'audit_entry' THEN
        SELECT max(c.through_entry) INTO covered FROM {schema}.audit_checkpoint c;
        IF covered IS NOT NULL AND OLD.entry_id <= covered THEN
            RETURN OLD;
        END IF;
    ELSIF TG_TABLE_NAME = 'audit_seal' THEN
        SELECT max(c.through_seal) INTO covered FROM {schema}.audit_checkpoint c;
        IF covered IS NOT NULL AND OLD.seal_id <= covered THEN
            RETURN OLD;
        END IF;
    END IF;
    RAISE EXCEPTION 'the audit ledger is append-only: % is not permitted', TG_OP
        USING HINT = 'a record that can be changed is not a record';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER audit_no_delete ON {schema}.audit_entry;
CREATE TRIGGER audit_no_delete BEFORE DELETE ON {schema}.audit_entry
    FOR EACH ROW EXECUTE FUNCTION {schema}.audit_leaves_only_when_archived();
DROP TRIGGER audit_seal_no_delete ON {schema}.audit_seal;
CREATE TRIGGER audit_seal_no_delete BEFORE DELETE ON {schema}.audit_seal
    FOR EACH ROW EXECUTE FUNCTION {schema}.audit_leaves_only_when_archived();

-- Sealing, as it was (0064), with one more place to chain from. When every seal has left, the next
-- one follows the newest checkpoint. Chaining to zero there would start a second chain that
-- verifies on its own and says nothing of what came before it.
CREATE OR REPLACE FUNCTION {schema}.audit_seal_now()
RETURNS TABLE (from_entry bigint, to_entry bigint, entry_count integer, digest bytea)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
    lo bigint;
    hi bigint;
    n integer;
    previous bytea;
    entries bytea;
    chained bytea;
BEGIN
    LOCK TABLE {schema}.audit_entry IN SHARE ROW EXCLUSIVE MODE;

    SELECT s.to_entry + 1, s.digest INTO lo, previous
      FROM {schema}.audit_seal s ORDER BY s.seal_id DESC LIMIT 1;
    IF NOT FOUND THEN
        SELECT c.through_entry + 1, c.digest INTO lo, previous
          FROM {schema}.audit_checkpoint c ORDER BY c.checkpoint_id DESC LIMIT 1;
    END IF;
    IF NOT FOUND THEN
        -- The first seal chains to a fixed zero rather than to nothing, so verification has one rule.
        lo := 0;
        previous := decode(repeat('00', 32), 'hex');
    END IF;

    SELECT e.entry_id INTO hi FROM {schema}.audit_entry e
     WHERE e.entry_id >= lo ORDER BY e.entry_id OFFSET 99999 LIMIT 1;
    IF hi IS NULL THEN
        SELECT max(e.entry_id) INTO hi FROM {schema}.audit_entry e WHERE e.entry_id >= lo;
    END IF;
    IF hi IS NULL THEN
        RETURN;
    END IF;
    SELECT count(*) INTO n FROM {schema}.audit_entry e WHERE e.entry_id BETWEEN lo AND hi;

    entries := {schema}.audit_entries_digest(lo, hi);
    chained := sha256(previous || entries);
    INSERT INTO {schema}.audit_seal (from_entry, to_entry, entry_count, entries_digest, previous_digest, digest)
    VALUES (lo, hi, n, entries, previous, chained);
    RETURN QUERY SELECT lo, hi, n, chained;
END;
$$;

-- One line of the ledger exactly as a seal digested it, so that a file of these can be checked
-- against a seal by anything that can compute SHA-256, with no database and no knowledge of how
-- PostgreSQL writes a number. audit_entries_digest (0017) joins these same lines.
CREATE FUNCTION {schema}.audit_entry_line(e {schema}.audit_entry) RETURNS text AS $$
    SELECT e.entry_id::text || '|' || e.operation || '|' || e.principal || '|' ||
           e.principal_kind || '|' || e.project || '|' || e.magnitude::text || '|' ||
           e.outcome || '|' || extract(epoch from e.occurred_at)::text;
$$ LANGUAGE sql STABLE;

-- Archiving is an operator's act, so the ledger has to be able to name it.
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
    'audit.activity','vocabulary.add','vocabulary.list','backlog.limits','audit.archive'));
