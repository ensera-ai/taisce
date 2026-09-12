-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- A ninth reason a proposal did not become a fact: one of its ends carries a name this store cannot
-- keep. A name longer than the bound, or one spelling too many for a single entity, used to fail the
-- whole turn — so a turn that was mostly ordinary lost every claim in it, was retried, and was
-- eventually parked, for one end nobody could have shortened. It is an outcome with a reason, like
-- the other eight, and the rest of the turn forms.
ALTER TABLE {schema}.rejected_claim
    DROP CONSTRAINT rejected_claim_reason_chk;
ALTER TABLE {schema}.rejected_claim
    ADD CONSTRAINT rejected_claim_reason_chk
    CHECK (reason = ANY (ARRAY['unmapped_relation', 'unlocatable_quote',
                               'duplicate_claim', 'not_asserted', 'unresolvable_subject',
                               'not_current', 'conflicting_value', 'not_spoken_by_principal',
                               'entity_name_limit']));
COMMENT ON CONSTRAINT rejected_claim_reason_chk ON {schema}.rejected_claim IS
    'Nine reasons a proposal did not become a fact, each meaning something different: the vocabulary '
    'cannot represent it, the message cannot cite it, this message already said it, the message did '
    'not assert it, it is about nothing the message establishes, the message puts it in the past, the '
    'message already gave the relation a different current value, it speaks for the principal from '
    'a message the principal did not speak, or one of its ends carries a name the store cannot keep.';
