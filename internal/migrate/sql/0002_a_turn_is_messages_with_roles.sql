-- ── A turn is messages with roles, and the groups among them are atomic ───────────────────────
--
-- `observation.payload` could hold the messages as jsonb and the write path would work. This is
-- rows instead, for two reasons that are both about what READS them later.
--
-- ── THE SPEAKER HAS TO BE QUERYABLE, NOT PARSEABLE ────────────────────────────────────────────
--
-- The role policy — an assistant's claim never becomes a user's fact — is enforced per message. A
-- role buried in a jsonb array is something every reader has to extract and every writer has to
-- shape identically, which is a convention rather than a constraint. As a column it is `NOT NULL`
-- with a CHECK, and a message with no speaker cannot be stored at all.
--
-- ── COMPACTION RUNS ON THE SERVER, SO THE SERVER HAS TO SEE THE GROUPS ────────────────────────
--
-- An assistant message carrying tool calls and the tool results answering them are ONE unit. A
-- compaction that summarises half of one produces a message list the model rejects outright — not a
-- degraded summary, an API error. So a compaction strategy has to know where the seams are.
--
-- `group_ordinal` is that, and deliberately nothing more: members of an inseparable unit share a
-- value, and the number ascends with the turn. It does not record WHAT the tool call was, because
-- nothing here needs to interpret it — only to avoid cutting through it. Storing the call itself
-- would be storing a second copy of the payload for no reader.
--
-- Retrofitting this later would mean every turn written before the change has no group structure,
-- and compaction would have to guess for exactly the oldest history — the part it compacts first.

CREATE TABLE {schema}.turn_message (
    observation_id uuid        NOT NULL REFERENCES {schema}.observation (observation_id) ON DELETE CASCADE,
    -- Position in the turn as the caller sent it. The caller's order is authoritative: it is the
    -- order the model saw, and a memory that reorders a conversation has changed what happened.
    ordinal        integer     NOT NULL,
    role           text        NOT NULL,
    content        text        NOT NULL,
    -- Members of an atomic unit share this. Ascends with the turn, so ordering by it is ordering by
    -- the conversation.
    group_ordinal  integer     NOT NULL,
    occurred_at    timestamptz NOT NULL,

    PRIMARY KEY (observation_id, ordinal),
    CONSTRAINT turn_message_role_chk
        CHECK (role = ANY (ARRAY['user', 'assistant', 'system', 'tool'])),
    CONSTRAINT turn_message_ordinal_chk       CHECK (ordinal >= 0),
    CONSTRAINT turn_message_group_ordinal_chk CHECK (group_ordinal >= 0)
);

-- The read every consumer issues: one turn, in order. `group_ordinal` and `role` ride along so a
-- compaction pass can group and filter without a heap fetch per message.
CREATE INDEX turn_message_turn_idx
    ON {schema}.turn_message (observation_id, ordinal)
    INCLUDE (group_ordinal, role);

-- ── A chunk names the message it came from ────────────────────────────────────────────────────
--
-- Evidence is a byte span (`fact_evidence.byte_start` / `byte_end`), and a span is meaningless
-- without the string it indexes into. The chunk already carries `source_observation_id`, but an
-- observation is a whole turn — so a span resolved against it would be a span into a concatenation
-- that exists nowhere, and `resolve_citation` would return the wrong words while looking correct.
--
-- Nullable, because a chunk can come from a document rather than a turn, and that path has no
-- messages. The citation resolver requires it for turn-sourced chunks and says so.
ALTER TABLE {schema}.chunk ADD COLUMN IF NOT EXISTS source_message_ordinal integer;

COMMENT ON COLUMN {schema}.chunk.source_message_ordinal IS
    'Ordinal of the turn_message this chunk was cut from. NULL for chunks with no message — a '
    'document, say. Evidence byte spans index into THAT message, never into the turn.';

-- ── Two columns whose readers this migration removed, or never created ────────────────────────
--
-- `observation.payload` held the turn as a jsonb blob. Its content is now rows in `turn_message`,
-- with the role as a constrained column instead of a convention. Keeping both would be a second copy
-- of the same messages that can disagree with the first, and the disagreement would surface as a
-- citation resolving against text nobody can see.
--
-- `observation.actor` has never had a writer or a reader. It is the obvious place to record who made
-- the call, and that is exactly the reasoning to refuse: a column installed because it will probably
-- be wanted is indistinguishable from one installed because it was wanted, right up until somebody
-- writes a query against it and finds it empty for the whole history. When the API layer needs to
-- record a caller it adds the column together with the query that reads it.
--
-- Dropping is cheap here and expensive later: no deployment has run this schema, so nothing is lost.
ALTER TABLE {schema}.observation DROP COLUMN IF EXISTS payload;
ALTER TABLE {schema}.observation DROP COLUMN IF EXISTS actor;
