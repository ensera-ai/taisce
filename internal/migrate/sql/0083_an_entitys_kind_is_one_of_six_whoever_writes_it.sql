-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- ── An entity's kind is one of six, whoever writes it ─────────────────────────────────────────
--
-- A relation's object end has been one of six kinds since 0003, by a check on `predicate`. The
-- column that holds what an entity itself is — `entity.entity_type` — was text with a default and
-- nothing else. Extraction holds a model's kind to the six before it reaches storage, and that is a
-- bound in code: it holds for the write path that passes through it. Three statements write this
-- column, and only one of them is that path.
--
--   * the entity a claim names, written by extraction — bounded in code;
--   * a speaker, which is always a person;
--   * an entity put back from a retained receipt, which carries the kind the entity had when the
--     receipt was written. A receipt written before extraction was bounded holds whatever the model
--     said, and restoring from it writes that.
--
-- And beyond those, a statement run by somebody holding the administrative connection, and the
-- previous release, which writes a model's kind as it arrived.
--
-- A kind is returned by every recall as a category. A caller switches on it and nobody attaches a
-- quote to it, so text there is read with less suspicion than text anywhere else in an answer.
--
-- ── WHAT HOLDS IT ───────────────────────────────────────────────────────────────────────────────
--
-- Two things, because one of them alone is wrong either way.
--
-- A CHECK is the boundary: the column cannot hold a seventh kind. On its own it would also refuse
-- the write — and the previous release's write of a seventh kind is a whole turn, and a receipt's
-- is a whole restore. A release's schema is one the previous release can serve on, so a check that
-- fails its turns is not one this migration may add bare.
--
-- So a trigger in front of it gives a kind outside the six the kind an entity has when nobody typed
-- it: `thing`. That is the rule extraction already applies to a model's kind — the claim is sound,
-- the label is not, so the label is treated as not given — applied where every writer passes. It
-- runs before the check, so the check is never what an ordinary write meets.
--
-- The check is what is left when the trigger is not in the way: a restore that loads rows before
-- it creates triggers, a session that has switched them off, a table somebody disabled it on. Then
-- a seventh kind is refused, and that is the test that makes it refuse.
--
-- ── WHAT IT DOES TO ROWS ALREADY WRITTEN ────────────────────────────────────────────────────────
--
-- An entity whose kind is outside the six becomes a `thing`. What it said before is not kept: it
-- is a model's unbounded text, tied to no data subject, and a table that kept it would be a second
-- place such text lives that no erasure sweeps. To see what an upgrade will change, count before it:
--
--   SELECT entity_type, count(*) FROM <namespace>.entity
--    WHERE entity_type <> ALL (ARRAY['person','organisation','place','time','value','thing'])
--    GROUP BY 1 ORDER BY 2 DESC;
--
-- Retained receipts are not rewritten. A receipt is the record of what a fact was, and the trigger
-- is what meets its kind on the way back in.
--
-- ── WHY ONE TRIGGER IS SWITCHED OFF FOR ONE STATEMENT ───────────────────────────────────────────
--
-- Any update of an entity discards its embedding, under a lock held until the transaction ends.
-- A migration is one transaction, so rewriting N entities took N locks: measured on 20,000
-- entities with a kind outside the six, the rewrite failed with "out of shared memory" — the lock
-- table of a default server holds about 6,400. And the discard would be for nothing: an entity's
-- embedding is made from its name, its spellings and the words said about it, never its kind.
--
-- So that trigger is off for the rewrite and on again after it, inside this transaction. Nothing
-- else can write an entity in between — switching it off takes a lock that excludes writers — so
-- no update that should have discarded an embedding is missed.
--
-- ── WHAT IT COSTS ───────────────────────────────────────────────────────────────────────────────
--
-- On the write path: one comparison against six strings for each entity written. The trigger's
-- function runs only when the comparison fails, which no write of this release does.
--
-- On an upgrade: writes to `entity` wait from the rewrite until this commits, and reads wait while
-- the check is validated, which is one pass over the table. Both are the size of the entity table,
-- not of the facts.
--
-- ── WHAT IT DOES NOT DO ─────────────────────────────────────────────────────────────────────────
--
-- It does not refuse a seventh kind written by hand through the ordinary path: that write is
-- stored as a `thing`, silently, like any other. Nothing counts how often the trigger fires, so how
-- often a model writes a kind outside the six is still not measured. And it does not correct a kind
-- that is one of the six and wrong.
CREATE FUNCTION {schema}.give_the_kind_of_an_entity_nobody_typed() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    NEW.entity_type := 'thing';
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION {schema}.give_the_kind_of_an_entity_nobody_typed() FROM PUBLIC;

CREATE TRIGGER entity_kind_outside_the_six_is_not_given BEFORE INSERT OR UPDATE OF entity_type ON {schema}.entity
    FOR EACH ROW WHEN (NEW.entity_type <> ALL (ARRAY['person', 'organisation', 'place', 'time', 'value', 'thing']))
    EXECUTE FUNCTION {schema}.give_the_kind_of_an_entity_nobody_typed();

ALTER TABLE {schema}.entity DISABLE TRIGGER entity_embedding_entity_changed;
UPDATE {schema}.entity SET entity_type = 'thing'
 WHERE entity_type <> ALL (ARRAY['person', 'organisation', 'place', 'time', 'value', 'thing']);
ALTER TABLE {schema}.entity ENABLE TRIGGER entity_embedding_entity_changed;

-- Last, so that the lock that excludes readers is held for the validation and nothing before it.
ALTER TABLE {schema}.entity ADD CONSTRAINT entity_kind_chk
    CHECK (entity_type = ANY (ARRAY['person', 'organisation', 'place', 'time', 'value', 'thing']));

COMMENT ON CONSTRAINT entity_kind_chk ON {schema}.entity IS
    'The six kinds an entity can be, the same six predicate_object_kind_chk closes. The trigger entity_kind_outside_the_six_is_not_given stores any other as thing before this is evaluated, so this refuses only a write that did not pass the trigger.';
COMMENT ON TRIGGER entity_kind_outside_the_six_is_not_given ON {schema}.entity IS
    'A kind outside the six is stored as thing, not refused: the previous release and a retained receipt both write a kind as a model gave it, and refusing would fail a turn or a restore over a label.';
