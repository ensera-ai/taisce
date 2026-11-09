-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- The vocabulary's key stops moving under the facts that reference it.
--
-- `fact_cardinality_matches_vocabulary` (0010) referenced `predicate(predicate, cardinality)`
-- ON UPDATE CASCADE. The deletion side of the same reference refuses, and 0010 says why: retiring a
-- relation while facts still use it must fail loudly, because a cascade there would delete the
-- memory instead of refusing the change. That reasoning was applied to delete and not to update.
--
-- # What the cascade actually permitted, measured rather than assumed
--
-- Redeclaring a relation's cardinality. One statement —
-- `UPDATE predicate SET cardinality='one' WHERE predicate='works_at'` — rewrote the cardinality on
-- every fact that carried the relation, in the same statement, with no fact deleted and no count
-- changed. The copy on each fact is what supersession rests on, so this is not a label: it changes
-- whether a later fact closed an earlier one for facts that were formed years before the change, and
-- it changes it retroactively and invisibly. The ledger records no memory write, the receipts are
-- unaffected, and afterwards the rows look correct.
--
-- `many` to `one` can also collide with the single-current-value exclusion halfway through, which
-- would leave the statement refused for a reason that says nothing about what was attempted.
--
-- # What it did not permit, which is where this started
--
-- A rename. `UPDATE predicate SET predicate='employed_by' WHERE predicate='works_at'` was already
-- refused while facts carried the relation — by `fact_predicate_fk` (0003), whose ON UPDATE NO
-- ACTION check fires before the cascade from 0010 reaches the rows, because 0003 created its
-- constraint first. That was the premise this change set out from and it does not hold.
--
-- It is worth closing anyway, and for a reason stronger than the one that was looked for: the rename
-- was refused by an accident of the order two internal referential triggers happen to fire in, which
-- is the order the constraints were created in. Nothing states it, nothing tests it, and a later
-- migration that dropped and recreated `fact_predicate_fk` would reverse it without anybody
-- noticing. With this setting the refusal does not depend on which reference looks first.
--
-- # Why the substrate and not the application
--
-- The design already rejects a rename — D29 says a rename is a second spelling of one relation — and
-- the vocabulary operation offers neither a rename nor a redeclaration. But "we do not offer it" is
-- a convention, and a convention is broken by the statement written under pressure by whoever holds
-- the administrative connection, which is the only identity that can reach this table at all. A rule
-- in the substrate is one no statement gets around.
--
-- # What the cascade was serving
--
-- Nothing. No path in this system updates either referenced column: the vocabulary operation inserts
-- and does not update, no migration changes a name or a cardinality, and the memory identity has no
-- write on the table at all.
--
-- # What this does not cover
--
-- A relation no fact has ever carried. Nothing references it, so RESTRICT has nothing to refuse, and
-- renaming or redeclaring one loses no memory — which is why this is a limit rather than a gap. And a
-- relation that genuinely needs a different name: there is no supported answer to that and this does
-- not invent one. Adding the new relation, rebuilding the sources that used the old one, and erasing
-- what is left keeps every receipt honest; a rename would leave the history claiming the facts were
-- always spelled that way.
ALTER TABLE {schema}.fact
    DROP CONSTRAINT fact_cardinality_matches_vocabulary;
ALTER TABLE {schema}.fact
    ADD CONSTRAINT fact_cardinality_matches_vocabulary
        FOREIGN KEY (predicate, cardinality)
        REFERENCES {schema}.predicate (predicate, cardinality)
        ON UPDATE RESTRICT;

COMMENT ON CONSTRAINT fact_cardinality_matches_vocabulary ON {schema}.fact IS
    'The copy of cardinality on each fact can never disagree with the vocabulary, and the key it '
    'references cannot move while facts carry it: renaming a relation, or redeclaring its '
    'cardinality, is refused rather than cascaded. A cascade rewrote every fact that carried the '
    'relation without deleting one or changing a count, so nothing an operator looks at afterwards '
    'would show it — and cardinality is what supersession rests on, so it changed what the facts '
    'already stored had meant.';
