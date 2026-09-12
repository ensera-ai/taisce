-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- ── A seal waits for what it covers ──────────────────────────────────────────────────────────
--
-- A seal covered every entry up to the highest id it could see. An entry draws its id when its
-- INSERT runs and becomes visible when its transaction commits, so an entry that drew a lower id and
-- committed after the seal landed inside a sealed range without being in its digest — and the next
-- verification reported tampering that never happened. Two sealers could not overlap; a
-- sealer and a writer could.
--
-- ── WHAT CLOSES IT ────────────────────────────────────────────────────────────────────────────
--
-- Sealing is one function, and it takes SHARE ROW EXCLUSIVE on the ledger first. That mode conflicts
-- with the ROW EXCLUSIVE an INSERT takes before it draws its id, and with itself. So the seal starts
-- only when every writer that drew an id has finished, a writer that arrives meanwhile draws its id
-- after the seal, and two sealers queue. Every id the seal can see then belongs to a finished
-- transaction, and the range it covers is final.
--
-- ── WHAT IT COSTS, AND THE BOUND ──────────────────────────────────────────────────────────────
--
-- Appends wait while a seal holds the lock: that is a pause on the read path, which records every
-- recall. The pause is the time to digest the entries being sealed, so a seal covers at most
-- 100,000 of them and a longer backlog is sealed over several calls. The bound is a count, not a
-- measurement: how long 100,000 entries take to digest has not been measured on hardware that can
-- say so.
--
-- ── WHO MAY SEAL ──────────────────────────────────────────────────────────────────────────────
--
-- The function is SECURITY DEFINER with a pinned search_path, so the memory role seals without being
-- able to write a seal itself: it loses INSERT on audit_seal (memorywrites.go), and with it the
-- ability to write an arbitrary digest into the chain. The function is owned by whoever runs the
-- migrations — on the shipped compose file, the superuser — which is why every name in it is
-- qualified and nothing is resolved through the caller's search path.
--
-- ── TRUNCATE ──────────────────────────────────────────────────────────────────────────────────
--
-- Row triggers do not fire on TRUNCATE, so 0013's promise that the ledger is append-only against
-- every identity was wider than its triggers. A statement trigger now refuses TRUNCATE on both
-- tables. An owner can still disable triggers; that is the operator limit the seals and the head
-- digest written outside the database exist for, not something a trigger can close.
CREATE FUNCTION {schema}.audit_seal_now()
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

CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON {schema}.audit_entry
    FOR EACH STATEMENT EXECUTE FUNCTION {schema}.audit_is_append_only();
CREATE TRIGGER audit_seal_no_truncate BEFORE TRUNCATE ON {schema}.audit_seal
    FOR EACH STATEMENT EXECUTE FUNCTION {schema}.audit_is_append_only();
