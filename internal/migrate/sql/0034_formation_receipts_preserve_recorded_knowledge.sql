-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- A model outcome and its knowledge time cannot be inferred exactly from the words alone.
-- These source-owned receipts retain admitted state; losing them is authoritative-data loss.
-- They deliberately have no foreign key to the recoverable fact or entity tables.
CREATE TABLE {schema}.fact_receipt (
    scope text NOT NULL,
    fact_id uuid NOT NULL,
    source_observation_id uuid NOT NULL,
    source_ordinal integer NOT NULL,
    state jsonb NOT NULL CHECK(jsonb_typeof(state)='object'),
    evidence jsonb NOT NULL CHECK(jsonb_typeof(evidence)='object'),
    subject_identity jsonb,
    object_identity jsonb,
    pipeline_version text NOT NULL,
    supersession_source uuid,
    superseded_by uuid,
    PRIMARY KEY(scope,fact_id),
    CHECK(state->>'scope'=scope AND (state->>'fact_id')::uuid=fact_id),
    FOREIGN KEY(scope,source_observation_id) REFERENCES {schema}.observation(scope,observation_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(source_observation_id,source_ordinal) REFERENCES {schema}.turn_message(observation_id,ordinal)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(scope,supersession_source) REFERENCES {schema}.observation(scope,observation_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX fact_receipt_source_idx ON {schema}.fact_receipt(scope,source_observation_id,fact_id);
CREATE INDEX fact_receipt_successor_idx ON {schema}.fact_receipt(scope,superseded_by) WHERE superseded_by IS NOT NULL;
CREATE INDEX fact_receipt_cause_idx ON {schema}.fact_receipt(scope,supersession_source) WHERE supersession_source IS NOT NULL;

CREATE TABLE {schema}.fact_receipt_history (
    scope text NOT NULL,
    history_id uuid NOT NULL,
    fact_id uuid NOT NULL,
    valid tstzrange NOT NULL CHECK(NOT isempty(valid)),
    known tstzrange NOT NULL CHECK(NOT isempty(known) AND NOT upper_inf(known)),
    source_observation_id uuid NOT NULL,
    PRIMARY KEY(scope,history_id),
    FOREIGN KEY(scope,fact_id) REFERENCES {schema}.fact_receipt(scope,fact_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(scope,source_observation_id) REFERENCES {schema}.observation(scope,observation_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX receipt_history_fact_idx ON {schema}.fact_receipt_history(scope,fact_id);
CREATE INDEX receipt_history_source_idx ON {schema}.fact_receipt_history(scope,source_observation_id);

CREATE FUNCTION {schema}.retain_fact_transition() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    -- Deleting a successor projection clears its live FK. Its receipt still names the successor
    -- for recovery; deleting the causal observation instead cascades the entire receipt away.
    UPDATE {schema}.fact_receipt r SET
        state=CASE WHEN NEW.superseded_by IS NULL AND r.superseded_by IS NOT NULL
                   THEN to_jsonb(NEW)||jsonb_build_object('superseded_by',r.superseded_by)
                   ELSE to_jsonb(NEW) END,
        superseded_by=coalesce(NEW.superseded_by,r.superseded_by),
        supersession_source=NEW.supersession_source
    WHERE r.scope=NEW.scope AND r.fact_id=NEW.fact_id;
    RETURN NEW;
END;
$$;
CREATE TRIGGER fact_transition_receipt AFTER UPDATE ON {schema}.fact
    FOR EACH ROW EXECUTE FUNCTION {schema}.retain_fact_transition();
REVOKE ALL ON FUNCTION {schema}.retain_fact_transition() FROM PUBLIC;

CREATE FUNCTION {schema}.retain_fact_history() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    INSERT INTO {schema}.fact_receipt_history(scope,history_id,fact_id,valid,known,source_observation_id)
    SELECT NEW.scope,NEW.history_id,NEW.fact_id,NEW.valid,NEW.known,NEW.source_observation_id
    WHERE EXISTS(SELECT 1 FROM {schema}.fact_receipt r WHERE r.scope=NEW.scope AND r.fact_id=NEW.fact_id)
    ON CONFLICT(scope,history_id) DO NOTHING;
    RETURN NEW;
END;
$$;
CREATE TRIGGER fact_history_receipt AFTER INSERT ON {schema}.fact_history
    FOR EACH ROW EXECUTE FUNCTION {schema}.retain_fact_history();
REVOKE ALL ON FUNCTION {schema}.retain_fact_history() FROM PUBLIC;

ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK (operation IN (
    'observe','recall','erase','freshness','export','citation.resolve','record.list','record.history','record.retract','record.correct',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke','authenticate','formation.unpark','formation.recover'
));
