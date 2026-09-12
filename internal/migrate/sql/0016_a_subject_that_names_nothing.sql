-- ── A claim that is citable and about nothing ─────────────────────────────────────────────────
--
--  Measured against a live model. The message `Thanks, that worked perfectly.` produced:
--
--      that has_status worked perfectly — "that worked perfectly." [8:30)
--
--  Every rule held. The relation is in the vocabulary, the quote is verbatim, the span reproduces it
--  exactly. It is a perfectly citable claim about nothing: the subject is a pronoun whose referent is
--  outside the message, and every check in the path is about FORM.
--
--  ── WHY IT GETS WORSE RATHER THAN STAYING FLAT ───────────────────────────────────────────────
--
--  `that` normalises to an entity, so a corpus of ordinary chat builds one heavily-connected node
--  named `that` which every traversal passes through. It manufactures the hub skew #7 exists to watch
--  for, out of nothing.
--
--  ── WHY A TABLE AND NOT A LIST IN THE CODE ───────────────────────────────────────────────────
--
--  The obvious fix is a list of pronouns in Go, and the objection to it is this codebase's own: a list
--  of words in a repository that says lists of words are conventions. The predicate vocabulary faced
--  the same choice and the answer was a table with a foreign key, so that the closed set is enforced
--  where it is stored rather than remembered where it is used.
--
--  Same answer here. Pronouns and demonstratives are a genuinely CLOSED CLASS — unlike stopwords,
--  which are a frequency judgement — so they can be enumerated, and enumerating them in a table means
--  a second language is rows rather than a code change.
--
--  ── WHAT THIS DOES NOT CATCH, SAID PLAINLY ───────────────────────────────────────────────────
--
--  Only the subject end, and only terms in this table. `the thing has_status good` has a subject that
--  names nothing and is not a pronoun; it survives. This removes the measured defect and the family it
--  belongs to, and it is not a general test of whether a claim is worth keeping — that would need a
--  measurement of precision against recall, which is #11's and #16's work.

CREATE TABLE IF NOT EXISTS {schema}.unresolvable_term (
    -- Normalised the way entity names are normalised, so the comparison is the one the resolver would
    -- have made. A term stored in any other form is one the check would miss.
    term text NOT NULL,
    lang text NOT NULL DEFAULT 'en',
    -- Why it is here, so a later reader can judge whether it should be. A table of bare words is one
    -- nobody can safely edit.
    reason text NOT NULL,

    PRIMARY KEY (lang, term),
    CONSTRAINT unresolvable_term_chk CHECK (term = lower(btrim(term)) AND length(term) > 0)
);

-- English pronouns and demonstratives. A closed class: this is the whole of it for subject position,
-- not a sample.
INSERT INTO {schema}.unresolvable_term (term, lang, reason) VALUES
    ('i', 'en', 'personal pronoun; the speaker is known from the message role, not from the subject'),
    ('me', 'en', 'personal pronoun'),
    ('my', 'en', 'possessive determiner'),
    ('mine', 'en', 'possessive pronoun'),
    ('you', 'en', 'personal pronoun'),
    ('your', 'en', 'possessive determiner'),
    ('yours', 'en', 'possessive pronoun'),
    ('he', 'en', 'personal pronoun'),
    ('him', 'en', 'personal pronoun'),
    ('his', 'en', 'possessive'),
    ('she', 'en', 'personal pronoun'),
    ('her', 'en', 'personal pronoun'),
    ('hers', 'en', 'possessive pronoun'),
    ('it', 'en', 'personal pronoun'),
    ('its', 'en', 'possessive determiner'),
    ('we', 'en', 'personal pronoun'),
    ('us', 'en', 'personal pronoun'),
    ('our', 'en', 'possessive determiner'),
    ('ours', 'en', 'possessive pronoun'),
    ('they', 'en', 'personal pronoun'),
    ('them', 'en', 'personal pronoun'),
    ('their', 'en', 'possessive determiner'),
    ('theirs', 'en', 'possessive pronoun'),
    ('this', 'en', 'demonstrative; the referent is outside the message'),
    ('that', 'en', 'demonstrative; the measured case'),
    ('these', 'en', 'demonstrative'),
    ('those', 'en', 'demonstrative'),
    ('here', 'en', 'deictic; resolves to a place the message does not name'),
    ('there', 'en', 'deictic'),
    ('someone', 'en', 'indefinite pronoun'),
    ('somebody', 'en', 'indefinite pronoun'),
    ('something', 'en', 'indefinite pronoun'),
    ('anyone', 'en', 'indefinite pronoun'),
    ('anybody', 'en', 'indefinite pronoun'),
    ('anything', 'en', 'indefinite pronoun'),
    ('everyone', 'en', 'indefinite pronoun'),
    ('everybody', 'en', 'indefinite pronoun'),
    ('everything', 'en', 'indefinite pronoun'),
    ('nobody', 'en', 'indefinite pronoun'),
    ('nothing', 'en', 'indefinite pronoun'),
    ('who', 'en', 'interrogative; a question is not an assertion about a subject called "who"'),
    ('what', 'en', 'interrogative'),
    ('which', 'en', 'interrogative or relative')
ON CONFLICT (lang, term) DO NOTHING;

-- A fifth reason: the subject names nothing that could be an entity.
ALTER TABLE {schema}.rejected_claim
    DROP CONSTRAINT rejected_claim_reason_chk;

ALTER TABLE {schema}.rejected_claim
    ADD CONSTRAINT rejected_claim_reason_chk
    CHECK (reason = ANY (ARRAY['unmapped_relation', 'unlocatable_quote',
                               'duplicate_claim', 'not_asserted', 'unresolvable_subject']));

COMMENT ON TABLE {schema}.unresolvable_term IS
    'Terms that cannot be the subject of a fact because they name nothing the message establishes. A '
    'table rather than a list in code, for the reason the predicate vocabulary is a table: a closed '
    'set belongs where it is enforced. Pronouns and demonstratives are genuinely closed, unlike '
    'stopwords, which are a frequency judgement.';
