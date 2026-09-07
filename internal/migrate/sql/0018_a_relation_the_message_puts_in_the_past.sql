-- ── A sixth reason: the message puts the relation in the past ─────────────────────────────────
--
--  `I used to live in Amman.` asserts the relation. It is positive, it is not hedged, it is not
--  reported, and the polarity refusal added in 0011 lets it straight through. The quote is verbatim,
--  the span reproduces it, the relation is in the vocabulary — and what is stored is a fact claiming
--  the person lives in Amman now.
--
--  ── WHY THIS IS THE WORSE OF THE TWO PRESENCES ───────────────────────────────────────────────
--
--  A claim the message did not assert is a presence rather than an absence, which is what made it
--  worse than the three reasons before it. This one is worse again: a denial that slips through is a
--  fact about something the person went out of their way to say was untrue, and it tends to read
--  oddly to whoever sees it. A past tense that slips through reads perfectly. It is the answer to
--  "where does this person live", it is cited, it is current, and the only thing wrong with it is a
--  date nobody recorded.
--
--  ── WHY REFUSING RATHER THAN STORING AN INTERVAL ─────────────────────────────────────────────
--
--  The obvious alternative is to store it with a closed validity: the relation ended at the message.
--  That half is knowable. The other half is not — the sentence says nothing about when it began, and
--  `valid` cannot be left unbounded below, because every read is a containment test. An unbounded
--  start answers "did they live there in 1990" with yes, on evidence nobody has, and a fabricated
--  start is a date this system invented and would then cite.
--
--  So the claim is kept here with its words and its own reason. Nothing false is stored, the rate is
--  countable, and the sentences are the corpus a later decision would be made against.
--
--  ── WHAT THIS DELIBERATELY DOES NOT DO ───────────────────────────────────────────────────────
--
--  Using the past tense to CLOSE a fact already held. If memory holds an open `lives_in(user,
--  Amman)`, this sentence is testimony that it ended and the honest response is to close the interval
--  rather than to refuse. That needs an evidence model for an ending — the receipt comes from a
--  different message than the one that opened the fact — and a read that survives a second evidence
--  row, which the current inner join does not. It is held separately rather than assumed here.

ALTER TABLE {schema}.rejected_claim
    DROP CONSTRAINT rejected_claim_reason_chk;

ALTER TABLE {schema}.rejected_claim
    ADD CONSTRAINT rejected_claim_reason_chk
    CHECK (reason = ANY (ARRAY['unmapped_relation', 'unlocatable_quote',
                               'duplicate_claim', 'not_asserted', 'unresolvable_subject',
                               'not_current']));

COMMENT ON CONSTRAINT rejected_claim_reason_chk ON {schema}.rejected_claim IS
    'Six reasons a proposal did not become a fact, each meaning something different: the vocabulary '
    'cannot represent it, the message cannot cite it, this message already said it, the message did '
    'not assert it, it is about nothing the message establishes, or the message puts it in the past. '
    'The last two are the ones whose alternative was a false fact rather than a missing one.';
