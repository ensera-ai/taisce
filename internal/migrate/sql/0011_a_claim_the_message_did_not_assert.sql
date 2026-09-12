-- ── A fourth reason: the message did not assert it ────────────────────────────────────────────
--
--  Measured, against a live model: `I do not live in London.` produces `lives_in(I, London)` on some
--  attempts. `I think John works at Acme.` produces `works_at(John, Acme)` on most. Both pass every
--  check the system makes — the relation is in the vocabulary, the quote is verbatim, the span
--  reproduces it exactly — and both are facts asserting something the message did not say.
--
--  ── WHY THIS IS WORSE THAN THE REASONS ALREADY HERE ──────────────────────────────────────────
--
--  An unmapped relation is a claim we cannot represent. An unlocatable quote is a claim we cannot
--  cite. A duplicate is a claim we already have. All three are ABSENCES, and an absence is visible:
--  somebody asks a question and the answer is thin.
--
--  A claim the message did not assert is a PRESENCE. It is cited, current, indistinguishable from a
--  correct fact by every mechanism in the product, and it is what a customer is answered from. There
--  is nothing downstream that can tell it apart, which is why the refusal has to be here rather than
--  a filter somewhere later.
--
--  ── WHY REFUSING RATHER THAN STORING A POLARITY ──────────────────────────────────────────────
--
--  The alternative is a `polarity` column: store `I do not live in London` as a negative fact and let
--  every reader filter. That makes the memory able to answer "where does this person NOT live",
--  which is occasionally useful — and it makes every read path that forgets the predicate return the
--  negation as an assertion. It is the same denormalisation trap as the speaker, with the same
--  failure: silent, on the read path, in whatever query somebody writes next year.
--
--  Refusing is the smaller promise. A wrong fact is worse than an absent one, the claim is kept here
--  with its own reason so nothing is lost, and admitting negatives later is a decision this does not
--  block: the refused claims are exactly the corpus that decision would be made against.
--
--  ── WHY THE SET STAYS CLOSED ─────────────────────────────────────────────────────────────────
--
--  Four reasons now, and each still means a different thing: cannot represent, cannot cite, already
--  have, was not said. A fifth would need the same test — that no existing reason covers it and that
--  the count of it would drive a different response.

ALTER TABLE {schema}.rejected_claim
    DROP CONSTRAINT rejected_claim_reason_chk;

ALTER TABLE {schema}.rejected_claim
    ADD CONSTRAINT rejected_claim_reason_chk
    CHECK (reason = ANY (ARRAY['unmapped_relation', 'unlocatable_quote',
                               'duplicate_claim', 'not_asserted']));

COMMENT ON CONSTRAINT rejected_claim_reason_chk ON {schema}.rejected_claim IS
    'Four reasons a proposal did not become a fact, and each means something different: the '
    'vocabulary cannot represent it, the message cannot cite it, this message already said it, or '
    'the message did not assert it. The last is the only one whose alternative was writing a false '
    'fact rather than losing a true one.';
