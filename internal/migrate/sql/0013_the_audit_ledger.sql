-- ── What happened, and who did it ─────────────────────────────────────────────────────────────
--
--  Every operation that touches memory or changes who can reach it, attributed to the principal that
--  performed it. An erasure receipt says a deletion happened; this says who asked, and — more
--  importantly — that nothing else happened which nobody can account for.
--
--  ── WHY IT HOLDS NOTHING THAT COULD EVER BE ASKED ABOUT ──────────────────────────────────────
--
--  There is a conflict here that has to be resolved rather than managed. An audit trail an erasure
--  can delete is not an audit trail: the record of a deletion would be the first thing a deletion
--  removes. An audit trail an erasure cannot touch is personal data surviving an erasure, which is
--  the failure the erasure exists to prevent.
--
--  Both are avoided by recording nothing that could ever be the subject of an erasure request. No
--  data subject reference. No question text. No quote. No message content. What is recorded is that
--  a principal performed an operation on a project at a time, and how much it touched.
--
--  That makes append-only UNCONDITIONAL rather than a conflict to weigh case by case, and it is why
--  this table has no delete path, no erasure registration and no retention.
--
--  ── THE LOSS, WRITTEN DOWN RATHER THAN DISCOVERED ────────────────────────────────────────────
--
--  A person cannot be told which of their memories was read. They can be told that recalls happened
--  against a project, by which credential, and when. That is a real reduction in what the product
--  can answer, it is the price of the trade above, and it belongs beside the mechanism rather than
--  in a footnote somebody finds later.
--
--  ── WHY A COUNT AND NOT A RESULT ─────────────────────────────────────────────────────────────
--
--  `magnitude` is how much the operation touched — facts returned, rows deleted, messages appended.
--  A number rather than a description, because a description is a summary of content and content is
--  what this table does not hold. It is enough to answer "did something unusual happen" without
--  holding anything a person could ask to have removed.

CREATE TABLE IF NOT EXISTS {schema}.audit_entry (
    entry_id    bigserial   PRIMARY KEY,

    -- What was done. Closed, because an operation nobody has heard of cannot be counted, and the
    -- point of this table is that the set of things that happen is knowable.
    operation   text        NOT NULL,

    -- Who did it. A credential id or an operator session id — an identifier that resolves in the
    -- registry, never a name or an address, because those are personal data and this table holds
    -- none.
    principal   text        NOT NULL,
    -- What kind of principal, so a credential and a person are never confused for one another when
    -- somebody reads this back.
    principal_kind text     NOT NULL,

    -- Which project. Not nullable for memory operations and empty for instance-level ones, which is
    -- why it is a text column with a check rather than a foreign key: an audit row must be writable
    -- even for an operation on a project that no longer exists.
    project     text        NOT NULL DEFAULT '',

    -- How much it touched. Never what.
    magnitude   integer     NOT NULL DEFAULT 0,

    -- Whether it was allowed. A refused operation is the more interesting row: a repeated refusal is
    -- what an attack looks like from here.
    outcome     text        NOT NULL,

    occurred_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT audit_operation_chk CHECK (operation IN (
        'observe', 'recall', 'erase', 'freshness', 'export',
        'project.create', 'project.suspend', 'project.resume',
        'credential.issue', 'credential.revoke',
        'authenticate'
    )),
    CONSTRAINT audit_principal_kind_chk CHECK (principal_kind IN ('credential', 'operator', 'system')),
    CONSTRAINT audit_outcome_chk CHECK (outcome IN ('allowed', 'refused')),
    CONSTRAINT audit_magnitude_chk CHECK (magnitude >= 0),
    CONSTRAINT audit_principal_chk CHECK (length(principal) > 0)
);

-- The questions this table exists to answer: what happened recently, what has one principal been
-- doing, and what has been refused.
CREATE INDEX IF NOT EXISTS audit_time_idx ON {schema}.audit_entry (occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_principal_idx ON {schema}.audit_entry (principal, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_refused_idx ON {schema}.audit_entry (occurred_at DESC)
    WHERE outcome = 'refused';

-- Append-only, enforced rather than intended.
--
-- A ledger the application is trusted not to rewrite is a ledger whose integrity is a code review.
-- The data role has INSERT and SELECT and nothing else; these triggers close the same door against
-- anything holding a wider connection, including a migration written in a hurry.
CREATE OR REPLACE FUNCTION {schema}.audit_is_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'the audit ledger is append-only: % is not permitted', TG_OP
        USING HINT = 'a record that can be changed is not a record';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_no_update BEFORE UPDATE ON {schema}.audit_entry
    FOR EACH ROW EXECUTE FUNCTION {schema}.audit_is_append_only();
CREATE TRIGGER audit_no_delete BEFORE DELETE ON {schema}.audit_entry
    FOR EACH ROW EXECUTE FUNCTION {schema}.audit_is_append_only();

COMMENT ON TABLE {schema}.audit_entry IS
    'What happened and who did it. Holds no data subject, no question, no quote and no content — so '
    'append-only is unconditional rather than a conflict with erasure. The cost is that a person can '
    'be told a recall happened and not which of their memories it read.';
