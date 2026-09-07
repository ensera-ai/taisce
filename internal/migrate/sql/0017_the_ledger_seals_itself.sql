-- ── Making a rewritten ledger detectable ──────────────────────────────────────────────────────
--
--  The ledger is append-only against every identity the application holds: triggers refuse UPDATE and
--  DELETE, asserted through a superuser connection. What that does not cover is somebody who can drop
--  the triggers — an operator with full database access, which rule 12 asks about directly and which
--  self-hosting makes the realistic case rather than the exotic one.
--
--  ── WHY SEALS AND NOT A HASH ON EVERY ROW ────────────────────────────────────────────────────
--
--  Chaining each entry to the one before it is the obvious shape and it puts a SERIALISATION POINT on
--  the read path: every recall writes an audit row, and each row would have to read the digest of the
--  row before it. Every recall in the instance would queue behind every other. That is a real cost
--  paid on every call to protect against a case that is rare, which is the trade rule 9 says to refuse.
--
--  So entries are written freely and SEALED in batches. A seal covers a contiguous range of entries,
--  carries a digest over them, and chains to the seal before it. Rewriting a sealed entry changes its
--  seal's digest, which changes every seal after it — so a rewrite has to redo the whole chain rather
--  than one row.
--
--  ── WHAT THIS PROVES, AND WHAT IT DOES NOT ───────────────────────────────────────────────────
--
--  It proves that entries sealed before a given digest was observed have not been altered since. It
--  does NOT prove anything about entries written after the last seal — that window is the sealing
--  interval, and it is a real hole rather than a rounding error.
--
--  And it does not stop somebody who can rewrite everything. An operator with full access can delete
--  the ledger, rebuild it and re-seal it from scratch, and nothing inside the database can tell.
--  What defeats that is a digest held somewhere the database cannot reach, which is why the head
--  digest is emitted to the process log: whatever collects those logs then holds values a rebuilt
--  chain cannot agree with.
--
--  That is weaker than a signed checkpoint a client verifies, and it is deliberate. That design
--  existed so a CUSTOMER could catch a VENDOR. Self-hosted there is no vendor — the operator holds
--  the disk — and what remains is an operator proving to their own auditor that they have not
--  rewritten their own records, which a chain plus an external log answers at a fraction of the cost.

CREATE TABLE IF NOT EXISTS {schema}.audit_seal (
    seal_id      bigserial   PRIMARY KEY,
    -- The contiguous range this seal covers. Contiguous by construction: a seal always starts where
    -- the last one ended, so a gap would mean entries nobody sealed.
    from_entry   bigint      NOT NULL,
    to_entry     bigint      NOT NULL,
    entry_count  integer     NOT NULL,

    -- The digest over the entries themselves, and the chain link. Separated so a verifier can say
    -- WHICH failed: the entries changed, or the chain was re-linked around them.
    entries_digest bytea     NOT NULL,
    previous_digest bytea    NOT NULL,
    digest       bytea       NOT NULL,

    sealed_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT audit_seal_range_chk CHECK (to_entry >= from_entry),
    CONSTRAINT audit_seal_count_chk CHECK (entry_count > 0)
);

CREATE INDEX IF NOT EXISTS audit_seal_range_idx ON {schema}.audit_seal (to_entry DESC);

-- Sealed rows are what the chain covers, so a seal itself must be as unrewritable as the entries.
CREATE TRIGGER audit_seal_no_update BEFORE UPDATE ON {schema}.audit_seal
    FOR EACH ROW EXECUTE FUNCTION {schema}.audit_is_append_only();
CREATE TRIGGER audit_seal_no_delete BEFORE DELETE ON {schema}.audit_seal
    FOR EACH ROW EXECUTE FUNCTION {schema}.audit_is_append_only();

-- The digest of a range of entries.
--
-- Deterministic and explicit about the field order: a digest computed over `to_jsonb(row)` would
-- change when a column is added, which would make every historical seal fail verification after an
-- unrelated migration. Naming the fields means a new column does not silently invalidate the past —
-- and adding one to the digest is then a deliberate act with a version behind it.
CREATE OR REPLACE FUNCTION {schema}.audit_entries_digest(lo bigint, hi bigint)
RETURNS bytea AS $$
    SELECT sha256(coalesce(string_agg(
        e.entry_id::text || '|' || e.operation || '|' || e.principal || '|' ||
        e.principal_kind || '|' || e.project || '|' || e.magnitude::text || '|' ||
        e.outcome || '|' || extract(epoch from e.occurred_at)::text,
        E'\n' ORDER BY e.entry_id), '')::bytea)
      FROM {schema}.audit_entry e
     WHERE e.entry_id BETWEEN lo AND hi;
$$ LANGUAGE sql STABLE;

COMMENT ON TABLE {schema}.audit_seal IS
    'Chained digests over ranges of audit entries. Rewriting a sealed entry changes its seal and '
    'every seal after it, so a rewrite must redo the whole chain rather than one row. It does not '
    'stop somebody who can rebuild everything — for that, the head digest is emitted to the process '
    'log, where whatever collects logs holds values a rebuilt chain cannot agree with.';
