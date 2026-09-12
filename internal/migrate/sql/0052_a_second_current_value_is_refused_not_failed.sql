-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- A message that gives a single-cardinality relation two current values in one breath cannot have
-- both stored: the exclusion constraint refuses the second, and it is right to. Until now the store
-- returned that as a raw error, formation treated it as a model failure, and the whole turn was
-- retried and parked with nothing stored. Measured on a news corpus, about five articles in a hundred
-- parked this way. The second claim is now a refusal with its own reason, so the turn completes and
-- the count can be read.
ALTER TABLE {schema}.rejected_claim
    DROP CONSTRAINT rejected_claim_reason_chk;
ALTER TABLE {schema}.rejected_claim
    ADD CONSTRAINT rejected_claim_reason_chk
    CHECK (reason = ANY (ARRAY['unmapped_relation', 'unlocatable_quote',
                               'duplicate_claim', 'not_asserted', 'unresolvable_subject',
                               'not_current', 'conflicting_value']));
COMMENT ON CONSTRAINT rejected_claim_reason_chk ON {schema}.rejected_claim IS
    'Seven reasons a proposal did not become a fact, each meaning something different: the vocabulary '
    'cannot represent it, the message cannot cite it, this message already said it, the message did '
    'not assert it, it is about nothing the message establishes, the message puts it in the past, or '
    'the message already gave the relation a different current value. The last three are the ones '
    'whose alternative was a false fact rather than a missing one.';
