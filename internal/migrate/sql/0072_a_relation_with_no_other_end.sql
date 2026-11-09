-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- A claim whose object end is blank was stored rather than refused. The name resolved to no entity,
-- the write kept the fact with object_entity_id NULL, and the traversal that answers a recall skips
-- an edge with no other end — so the fact existed, had evidence, was counted by an erasure and
-- exported, and could never be read by any question, for the life of the instance.
--
-- That is the worst of the three states a claim can be in. A refusal is visible: it has a reason and
-- a count, and a thin answer sends somebody to look. A stored fact is readable. This was neither —
-- nothing said it was lost, and nothing said it was there.
--
-- The vocabulary already settles which it should be. Every predicate carries an object_kind, NOT NULL
-- and checked against six kinds (0003), and D29 kept the vocabulary closed when an operator was given
-- a way to extend it. A relation with no other end is not a unary fact this system holds; it is an
-- extraction that failed, and a failure belongs in the refusals where it can be counted.
--
-- The subject end had the same hole. It is nullable too, and extraction only refused subjects naming
-- something the message did not establish — a pronoun — not a subject that was blank. Both ends are
-- now refused before the quote is located, the object under its own reason and the subject under the
-- one it already had.
--
-- Every reason is restated here, which is how this constraint has always been extended and is a trap
-- this migration fell into on its first attempt: written from 0052's list, it silently dropped
-- not_spoken_by_principal (0053) and entity_name_limit (0066). Nothing failed at migration time. The
-- suite failed, with turns left unformed, because formation could no longer record those refusals and
-- a refusal it cannot record is an error that parks a turn. TestEveryRefusalReasonTheCodeCanProduceIsOneTheDatabaseAccepts
-- now holds the two lists against each other, so the next person extending this cannot lose one.
ALTER TABLE {schema}.rejected_claim
    DROP CONSTRAINT rejected_claim_reason_chk;
ALTER TABLE {schema}.rejected_claim
    ADD CONSTRAINT rejected_claim_reason_chk
    CHECK (reason = ANY (ARRAY['unmapped_relation', 'unlocatable_quote',
                               'duplicate_claim', 'not_asserted', 'unresolvable_subject',
                               'not_current', 'conflicting_value', 'not_spoken_by_principal',
                               'entity_name_limit', 'unresolvable_object']));
COMMENT ON CONSTRAINT rejected_claim_reason_chk ON {schema}.rejected_claim IS
    'Ten reasons a proposal did not become a fact, each meaning something different: the vocabulary '
    'cannot represent it, the message cannot cite it, this message already said it, the message did '
    'not assert it, it is about nothing the message establishes, the message puts it in the past, the '
    'message already gave the relation a different current value, the message was not the principal '
    'speaking, the name cannot be kept, or it names no other end. Three of them had a false fact as '
    'the alternative; the last had an unreadable one, which is worse, because a false fact can at '
    'least be contradicted.';
