-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- A claim that speaks in the first person from a message the principal did not speak is refused, and
-- that refusal is now a row like the others rather than only a counter. Measured on a news corpus
-- observed under a data subject, every claim from a tool-role message was refused by role, and the
-- ones about named third parties were refused with it; the rule was wider than its reason. It now
-- refuses only the speaker reference, and the store admits a non-user message's claims about named
-- third parties under that message's own role.
ALTER TABLE {schema}.rejected_claim
    DROP CONSTRAINT rejected_claim_reason_chk;
ALTER TABLE {schema}.rejected_claim
    ADD CONSTRAINT rejected_claim_reason_chk
    CHECK (reason = ANY (ARRAY['unmapped_relation', 'unlocatable_quote',
                               'duplicate_claim', 'not_asserted', 'unresolvable_subject',
                               'not_current', 'conflicting_value', 'not_spoken_by_principal']));
COMMENT ON CONSTRAINT rejected_claim_reason_chk ON {schema}.rejected_claim IS
    'Eight reasons a proposal did not become a fact, each meaning something different: the vocabulary '
    'cannot represent it, the message cannot cite it, this message already said it, the message did '
    'not assert it, it is about nothing the message establishes, the message puts it in the past, the '
    'message already gave the relation a different current value, or it speaks for the principal from '
    'a message the principal did not speak.';
