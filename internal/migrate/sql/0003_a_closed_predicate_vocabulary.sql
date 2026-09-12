-- ── The predicate vocabulary is closed, and the database is what closes it ─────────────────────
--
--  `fact.predicate` has been `text` with no constraint since the first migration, with a note that
--  the ontology was still to be designed. This designs it.
--
--  ── WHY A CLOSED SET AT ALL ──────────────────────────────────────────────────────────────────
--
--  An extractor asked for a relation invents one per sentence. Given a thousand conversations it
--  produces `works_at`, `employed_by`, `is_employed_at` and `has_employer` — four edge types
--  describing one relation. Nothing is wrong with any of them individually, and that is the
--  problem: a traversal asking "where does this person work" matches one of the four and silently
--  misses three quarters of what was extracted. The recall loss is invisible, because every row
--  looks correct.
--
--  It also makes every later stage unbuildable. Supersession asks "is there a current fact with
--  this subject and predicate" — which only reconciles if the two assertions chose the same word.
--  A community summary that groups by relation groups by spelling. A cardinality rule cannot be
--  written for a vocabulary nobody can enumerate.
--
--  ── WHY A TABLE WITH A FOREIGN KEY, AND NOT A LIST IN THE APPLICATION ────────────────────────
--
--  A list in the application is a convention: every write path has to remember to check it, and the
--  path that forgets is usually the one written last. As a foreign key the closed set is an
--  invariant of the data — an unknown predicate cannot be stored by any path, present or future,
--  including a hand-written INSERT during an incident.
--
--  The reference is RESTRICT rather than CASCADE, deliberately: retiring a predicate while facts
--  still use it must fail loudly. A cascade would delete the memory instead of refusing the change.
--
--  The rows are seeded per tenant, because a tenant is a schema and every statement about a tenant
--  names it. That means the vocabulary is a copy per tenant rather than one shared table — which is
--  the cost of the hard boundary, and it is the same cost every other table here pays.
--
--  ── WHAT EACH COLUMN IS FOR, AND WHO READS IT ────────────────────────────────────────────────
--
--  `cardinality` is what supersession rests on. `one` means at most one fact with this subject and
--  this predicate may be currently valid: a new one closes the old. `many` means several coexist,
--  and only a repetition of the same object is a duplicate. That distinction cannot be inferred at
--  supersession time — by then all that is visible is two rows — so it is declared here, when the
--  predicate is defined and the question is answerable.
--
--  `object_kind` is what the extractor is told to look for and what an unresolvable end is judged
--  against: a `time` object that came back as a person's name is a bad extraction, not a fact.
--
--  `semantic_type` groups relations that answer the same kind of question. It is the axis a bundle
--  is organised along when a caller asks for what is known about someone, because a flat list of
--  thirty-nine relation types is not something a person reads.
--
--  ── HOW THE SET WAS CHOSEN, AND WHAT WAS LEFT OUT ────────────────────────────────────────────
--
--  Every entry earns its place by being a relation an agent has to be able to ASK FOR by name. The
--  test is not "could this appear in a conversation" — everything can — but "would a question be
--  answered wrongly if this collapsed into its nearest neighbour".
--
--  So there is `manages` and no `reports_to`: edges are directed and traversal follows both ends,
--  so an inverse is a second spelling of one relation and reintroduces exactly the split this file
--  exists to prevent.
--
--  There is `dislikes` and no `avoids`: affect and behaviour are genuinely different, but not
--  differently enough that any question distinguishes them — and a pair that close is how a
--  vocabulary starts drifting back open.
--
--  There is NO generic `has_value` or `has_attribute`. A wildcard relation makes the closed set
--  vacuous: anything at all can be phrased as a value of a compound object, so every claim the
--  ontology was meant to refuse arrives through it wearing an accepted name. Claims that need one
--  are the claims this vocabulary is deliberately not admitting.
--
--  There is no `mentioned` or `co_occurs_with`. An edge between two things that appeared near each
--  other has no direction and no meaning, so there is nothing to follow and a multi-hop question
--  gets no hops out of it.

CREATE TABLE {schema}.predicate (
    predicate     text NOT NULL PRIMARY KEY,
    semantic_type text NOT NULL,
    cardinality   text NOT NULL,
    object_kind   text NOT NULL,
    description   text NOT NULL,

    -- Both of these are closed sets over a closed set, so a CHECK rather than two more tables: a
    -- reference table exists to be joined against, and nothing joins against these.
    CONSTRAINT predicate_cardinality_chk
        CHECK (cardinality = ANY (ARRAY['one', 'many'])),
    CONSTRAINT predicate_object_kind_chk
        CHECK (object_kind = ANY (ARRAY['person', 'organisation', 'place', 'time', 'value', 'thing'])),
    CONSTRAINT predicate_semantic_type_chk
        CHECK (semantic_type = ANY (ARRAY['identity', 'preference', 'capability', 'intent',
                                          'social', 'possession', 'structure', 'authorship',
                                          'temporal', 'state', 'constraint'])),
    -- The name is part of the contract: it is what an extractor is told to emit and what a caller
    -- filters a traversal by. Lowercase with underscores, so there is one spelling of each.
    CONSTRAINT predicate_name_chk CHECK (predicate ~ '^[a-z][a-z_]*[a-z]$')
);

INSERT INTO {schema}.predicate (predicate, semantic_type, cardinality, object_kind, description) VALUES
    -- ── identity: what someone or something is, and where it sits ─────────────────────────────
    ('has_name',       'identity',   'one',  'value',        'The name the subject goes by.'),
    ('has_role',       'identity',   'one',  'value',        'The position or title the subject currently holds.'),
    ('works_at',       'identity',   'many', 'organisation', 'An organisation the subject works for.'),
    ('member_of',      'identity',   'many', 'organisation', 'A group, team or body the subject belongs to.'),
    ('lives_in',       'identity',   'one',  'place',        'Where the subject currently lives.'),
    ('born_in',        'identity',   'one',  'place',        'Where the subject was born.'),
    ('speaks',         'identity',   'many', 'value',        'A language the subject uses.'),
    ('has_contact',    'identity',   'many', 'value',        'A way to reach the subject.'),
    ('has_timezone',   'identity',   'one',  'value',        'The timezone the subject operates in.'),

    -- ── preference: what the subject wants more or less of ────────────────────────────────────
    ('prefers',        'preference', 'many', 'thing',        'Something the subject wants, chooses, or asks for.'),
    ('dislikes',       'preference', 'many', 'thing',        'Something the subject does not want or reacts against.'),
    ('interested_in',  'preference', 'many', 'thing',        'A subject the subject pays attention to without claiming competence.'),

    -- ── capability: what the subject can do ───────────────────────────────────────────────────
    ('knows_about',    'capability', 'many', 'thing',        'A subject the subject is competent in.'),
    ('uses',           'capability', 'many', 'thing',        'A tool, product or method the subject works with.'),
    ('learning',       'capability', 'many', 'thing',        'Something the subject is acquiring competence in.'),

    -- ── intent: what the subject is going to do, or is stuck on ───────────────────────────────
    ('intends_to',     'intent',     'many', 'thing',        'Something the subject means to do, without having committed.'),
    ('committed_to',   'intent',     'many', 'thing',        'Something the subject has undertaken to do.'),
    ('responsible_for','intent',     'many', 'thing',        'Something the subject is accountable for.'),
    ('blocked_by',     'intent',     'many', 'thing',        'What is preventing the subject from proceeding.'),

    -- ── social: who the subject is connected to ───────────────────────────────────────────────
    ('knows',          'social',     'many', 'person',       'A person the subject is acquainted with.'),
    ('works_with',     'social',     'many', 'person',       'A person the subject collaborates with.'),
    ('manages',        'social',     'many', 'person',       'A person the subject has authority over.'),
    ('related_to',     'social',     'many', 'person',       'A person the subject has a family or personal tie to.'),

    -- ── possession: what the subject has ──────────────────────────────────────────────────────
    ('owns',           'possession', 'many', 'thing',        'Something the subject holds title to.'),
    ('has_access_to',  'possession', 'many', 'thing',        'Something the subject can reach or use without owning.'),

    -- ── structure: how things stand to each other ─────────────────────────────────────────────
    ('part_of',        'structure',  'many', 'thing',        'A whole the subject is a component of.'),
    ('instance_of',    'structure',  'one',  'thing',        'The kind the subject is an example of.'),
    ('located_in',     'structure',  'one',  'place',        'Where the subject currently is.'),
    ('depends_on',     'structure',  'many', 'thing',        'Something the subject requires in order to function.'),
    -- Asserted, never acted on. Resolution merges by normalised name and by nothing else, because
    -- over-merging puts two people's facts on one node and cannot be undone by a later read. This
    -- records that somebody said two things are the same; it does not make them one node.
    ('same_as',        'structure',  'many', 'thing',        'Something asserted to be the same as the subject. Recorded, not applied by resolution.'),

    -- ── authorship: what the subject made or took part in ─────────────────────────────────────
    ('created',        'authorship', 'many', 'thing',        'Something the subject brought into existence.'),
    ('contributed_to', 'authorship', 'many', 'thing',        'Something the subject worked on without being its author.'),
    ('participated_in','authorship', 'many', 'thing',        'An event or activity the subject took part in.'),

    -- ── temporal: when. The object is a time, and the subject is what happens ─────────────────
    ('occurred_on',    'temporal',   'one',  'time',         'When the subject happened.'),
    -- Distinct from occurred_on because it has not happened, and distinct from due_on because a
    -- time something takes place at and a time it must be finished by are different questions with
    -- different answers for the same subject.
    ('scheduled_for',  'temporal',   'one',  'time',         'When the subject is due to take place.'),
    ('due_on',         'temporal',   'one',  'time',         'When the subject must be completed by.'),

    -- ── state: the condition the subject is in ────────────────────────────────────────────────
    ('has_status',     'state',      'one',  'value',        'The condition the subject is currently in.'),

    -- ── constraint: what must or must not hold ────────────────────────────────────────────────
    ('requires',       'constraint', 'many', 'thing',        'Something that must hold for the subject.'),
    ('prohibits',      'constraint', 'many', 'thing',        'Something that must not hold for the subject.');

-- The constraint that makes the vocabulary closed rather than documented.
--
-- Added after the seed, so it validates against a populated table: the migration fails here if any
-- fact already carries a predicate this file did not admit, rather than accepting the schema and
-- leaving the disagreement to be found by a query later.
ALTER TABLE {schema}.fact
    ADD CONSTRAINT fact_predicate_fk
    FOREIGN KEY (predicate) REFERENCES {schema}.predicate (predicate);

COMMENT ON TABLE {schema}.predicate IS
    'The closed relation vocabulary. fact.predicate references it, so an unknown relation cannot be '
    'stored by any path. A claim outside this set is recorded as unmapped and never admitted as an '
    'edge — the message it came from is already stored, so what is refused is the typed edge, not '
    'the memory.';
