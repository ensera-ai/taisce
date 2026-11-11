-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- A project says whether its people share one world, and a fact belongs to an account.
--
-- ── THE TWO THINGS A PROJECT CAN BE ──────────────────────────────────────────────────────────
--
-- A named entity is one node per project, so two people who each mention "Sarah" write about one
-- node. A single-valued relation — where somebody lives, what something's status is — holds one
-- current value, and a newer statement closes the older one. Whether that is right depends on
-- something the rows cannot say:
--
--   A team's workspace. "Atlas is in Dublin", then from a colleague, "Atlas moved to Oslo". One
--   world, and the later statement is news about it. It should close the earlier one, whoever
--   said either.
--
--   An application with end users. Alice says her sister Sarah lives in Paris; Bob says his sister
--   Sarah lives in Tokyo. Two worlds that happen to share a name. Bob's statement must not close
--   Alice's — and when it did, nothing was refused, nothing was erased, the citation on Bob's fact
--   was genuine, and Alice's memory answered as though she had never said it.
--
-- Both are correct, for different projects, and nothing in a statement says which it was made in.
-- So the project says.
--
-- ── DECIDED HERE: THE PROJECT STATES IT, AND THE FACT CARRIES THE ANSWER ─────────────────────
--
-- `project.statements` is `shared` or `separate`. Every fact carries the `account` it was filed
-- under: the empty string for the project's one shared account, the data subject for a person's
-- own. The constraint that allows one current value compares the account, so in both kinds of
-- project supersession is still a constraint rather than a rule the write path has to remember.
--
-- The account is stamped by a trigger, from the project, when the fact is inserted. Not by the
-- statement that inserts it: four paths write facts — formation, an authored assertion, a rebuild
-- and recovery from receipts — and a column each of them had to compute the same way is a column
-- one of them would eventually compute differently.
--
-- ── WHY THE DEFAULT IS `separate` ────────────────────────────────────────────────────────────
--
-- A default decides which project is wrong when nobody chose. A shared-world project left on
-- `separate` holds a stale value beside the new one: both are there, both are dated and cited, and
-- the mistake is visible in the first read that shows two answers. A separate-worlds project left
-- on `shared` loses a true fact silently. The first is the one to be wrong about.
--
-- Projects that exist when this runs are a different matter. Their facts were formed under the
-- shared rule and are filed under the shared account, so they stay `shared`: changing the rule
-- underneath facts already written would leave rows filed under one account and governed by
-- another. A scope written to with no project row is `separate`, the same as a new project.
--
-- ── WHY IT CANNOT CHANGE ONCE THERE ARE FACTS ────────────────────────────────────────────────
--
-- The account is written on the fact. A project that switched after forming facts would hold some
-- filed under the shared account and some under people's own, and a person's new statement would
-- no longer close what they said before the switch. So the setting can be changed while the project
-- holds no facts, and is refused afterwards — here, by a trigger, so that a hand-written UPDATE
-- during an incident is refused as surely as the command is.
--
-- The fact trigger reads the project row FOR SHARE, which makes that refusal airtight: a switch
-- waits for an insert in flight, and then finds its fact. Without the lock a fact could be stamped
-- under the old setting while the new one committed beside it.
--
-- ── WHAT THIS DOES NOT DO ────────────────────────────────────────────────────────────────────
--
-- It does not separate the node. In a `separate` project "Sarah" is still one entity for everybody
-- who names her, so a read across the whole project returns each person's statements about her,
-- each with its own source. It separates who may end a statement, not who may see one.
--
-- It does not reopen what was already closed. In a project that should have been `separate`, a fact
-- ended by another person's statement stays ended; the page on temporal history gives the query
-- that counts them. Forming the content again in a `separate` project is what restores them.
--
-- A data subject is a string the application supplies. Nothing here verifies that it names who it
-- says it does.
--
-- ── WHAT IT COSTS TO RUN ─────────────────────────────────────────────────────────────────────
--
-- Replacing the exclusion constraint rebuilds its index, and the fact table is locked against
-- writes and reads while it does. Every existing fact is filed under the shared account in a shared
-- project, so the rows that satisfied the old constraint satisfy this one and the build cannot fail
-- on existing data.

ALTER TABLE {schema}.project
    ADD COLUMN statements text NOT NULL DEFAULT 'shared'
    CONSTRAINT project_statements_chk CHECK (statements IN ('shared', 'separate'));
-- The default above was for the rows that already exist. From here on, a project nobody decided
-- about is the kind whose mistake is visible.
ALTER TABLE {schema}.project ALTER COLUMN statements SET DEFAULT 'separate';

ALTER TABLE {schema}.fact ADD COLUMN account text NOT NULL DEFAULT '';

-- The one place the rule is written. The trigger below stamps a fact with it, and the statements
-- that look for "the value this account currently holds" ask it for the same answer.
CREATE FUNCTION {schema}.fact_account(fact_scope text, fact_data_subject text) RETURNS text
LANGUAGE sql STABLE SET search_path=pg_catalog,pg_temp AS $$
    SELECT CASE
        WHEN coalesce((SELECT p.statements FROM {schema}.project p WHERE p.scope = fact_scope),
                      'separate') = 'shared'
        THEN ''
        ELSE coalesce(fact_data_subject, '')
    END
$$;

CREATE FUNCTION {schema}.stamp_fact_account() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    -- Held to the end of the transaction, so the project cannot be switched between this fact
    -- being stamped and it being visible to the check that refuses the switch.
    PERFORM 1 FROM {schema}.project p WHERE p.scope = NEW.scope FOR SHARE;
    NEW.account := {schema}.fact_account(NEW.scope, NEW.data_subject_id);
    RETURN NEW;
END;
$$;

CREATE TRIGGER fact_account_is_stamped BEFORE INSERT ON {schema}.fact
    FOR EACH ROW EXECUTE FUNCTION {schema}.stamp_fact_account();
REVOKE ALL ON FUNCTION {schema}.stamp_fact_account() FROM PUBLIC;

-- An account does not change once written, and neither does the data subject it was derived from.
-- A fact moved to another account would escape the slot it was holding, and two current values
-- could then be made to coexist by an UPDATE where an INSERT would have been refused.
CREATE FUNCTION {schema}.refuse_fact_account_change() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    RAISE EXCEPTION 'a fact''s account is fixed when it is written'
        USING ERRCODE = '55000', CONSTRAINT = 'fact_account_is_fixed';
END;
$$;

CREATE TRIGGER fact_account_is_fixed BEFORE UPDATE OF account ON {schema}.fact
    FOR EACH ROW WHEN (OLD.account IS DISTINCT FROM NEW.account)
    EXECUTE FUNCTION {schema}.refuse_fact_account_change();
REVOKE ALL ON FUNCTION {schema}.refuse_fact_account_change() FROM PUBLIC;

CREATE FUNCTION {schema}.refuse_statements_change_with_facts() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM {schema}.fact f WHERE f.scope = NEW.scope) THEN
        RAISE EXCEPTION 'project % already holds facts filed under its % rule', NEW.scope, OLD.statements
            USING ERRCODE = '55000', CONSTRAINT = 'project_statements_fixed_once_facts_exist';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER project_statements_fixed_once_facts_exist BEFORE UPDATE OF statements ON {schema}.project
    FOR EACH ROW WHEN (OLD.statements IS DISTINCT FROM NEW.statements)
    EXECUTE FUNCTION {schema}.refuse_statements_change_with_facts();
REVOKE ALL ON FUNCTION {schema}.refuse_statements_change_with_facts() FROM PUBLIC;

ALTER TABLE {schema}.fact DROP CONSTRAINT fact_single_cardinality_excl;
ALTER TABLE {schema}.fact ADD CONSTRAINT fact_single_cardinality_excl
    EXCLUDE USING gist (
        scope             WITH =,
        subject_entity_id WITH =,
        predicate         WITH =,
        account           WITH =,
        valid             WITH &&,
        known             WITH &&
    )
    WHERE (cardinality = 'one' AND subject_entity_id IS NOT NULL);
