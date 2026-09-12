-- One message asserting the same relation twice produced two facts.
--
-- Nothing collapsed them. The fact table carries no uniqueness beyond its primary key, deliberately:
-- a superseded fact and the fact replacing it share a subject, a predicate and an object, and differ
-- only in their valid range, so a unique key over the triple would make supersession impossible. The
-- duplicate this refuses is a narrower thing — the same relation, from the same message, asserted
-- twice in one extraction — and it is refused where it is produced rather than prevented in a
-- constraint that would also forbid the legitimate case.
--
-- What belongs here is the record of the refusal. The reason set is closed by a check constraint so
-- that a reason no reader knows about cannot be written, and a third reason is a third thing the
-- record means: not "we could not represent this" and not "we could not cite this", but "this is
-- already recorded, and recording it again would spend a row in a bundle on a repeat".

ALTER TABLE {schema}.rejected_claim
    DROP CONSTRAINT rejected_claim_reason_chk;

ALTER TABLE {schema}.rejected_claim
    ADD CONSTRAINT rejected_claim_reason_chk
    CHECK (reason = ANY (ARRAY['unmapped_relation', 'unlocatable_quote', 'duplicate_claim']));
