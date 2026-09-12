-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- This is authoritative governance metadata, not inferred memory. Only the random subject ID is
-- propagated onto source attribution. An external reference has one current mapping per project.
CREATE TABLE {schema}.data_subject (
    scope text NOT NULL REFERENCES {schema}.project(scope) ON DELETE CASCADE,
    subject_id text COLLATE "C" NOT NULL CHECK(subject_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
    external_reference text COLLATE "C" CHECK(external_reference IS NULL OR (octet_length(external_reference) BETWEEN 1 AND 1024 AND length(btrim(external_reference))>0)),
    label text NOT NULL CHECK(octet_length(label)<=256),
    version uuid NOT NULL,
    previous_version uuid,
    last_digest bytea NOT NULL CHECK(octet_length(last_digest)=32),
    updated_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    inactive_after timestamptz,
    PRIMARY KEY(scope,subject_id),
    CONSTRAINT subject_external_reference_unique UNIQUE(scope,external_reference)
);
CREATE INDEX subject_inactive_idx ON {schema}.data_subject(scope,inactive_after,subject_id)
WHERE inactive_after IS NOT NULL;

CREATE TABLE {schema}.subject_retry (
    scope text NOT NULL REFERENCES {schema}.project(scope) ON DELETE CASCADE,
    key_digest bytea NOT NULL CHECK(octet_length(key_digest)=32),
    subject_id text,
    request_digest bytea CHECK(octet_length(request_digest)=32),
    PRIMARY KEY(scope,key_digest),
    FOREIGN KEY(scope,subject_id) REFERENCES {schema}.data_subject(scope,subject_id),
    CHECK((subject_id IS NULL)=(request_digest IS NULL))
);
CREATE INDEX subject_retry_owner_idx ON {schema}.subject_retry(scope,subject_id) WHERE subject_id IS NOT NULL;

CREATE FUNCTION {schema}.clear_subject_retry_payload() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE {schema}.subject_retry SET subject_id=NULL,request_digest=NULL
    WHERE scope=OLD.scope AND subject_id=OLD.subject_id;
    RETURN OLD;
END
$$;
CREATE TRIGGER subject_retry_forgets_payload BEFORE DELETE ON {schema}.data_subject
FOR EACH ROW EXECUTE FUNCTION {schema}.clear_subject_retry_payload();

-- Rotation changes only the current external reference and label. Reassigning identity or lifetime
-- would silently move existing attribution or extend retention on a cosmetic update.
CREATE FUNCTION {schema}.protect_subject_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.scope,NEW.subject_id,NEW.created_at,NEW.inactive_after)
       IS DISTINCT FROM (OLD.scope,OLD.subject_id,OLD.created_at,OLD.inactive_after) THEN
       RAISE EXCEPTION 'subject identity and lifetime are immutable' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER subject_identity_immutable BEFORE UPDATE ON {schema}.data_subject
FOR EACH ROW EXECUTE FUNCTION {schema}.protect_subject_identity();

-- Sources may still use application-managed opaque IDs. When an optional registry entry exists,
-- hold it while inserting/changing attribution so expiry cannot remove a mapping in the middle of
-- accepting a source. If deletion completed first, a later source is a new application-managed ID.
CREATE FUNCTION {schema}.lock_registered_subject() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.data_subject_id IS NOT NULL THEN
        PERFORM 1 FROM {schema}.data_subject WHERE scope=NEW.scope AND subject_id=NEW.data_subject_id FOR KEY SHARE;
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER observation_registered_subject BEFORE INSERT OR UPDATE OF scope,data_subject_id ON {schema}.observation
FOR EACH ROW EXECUTE FUNCTION {schema}.lock_registered_subject();

ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK(operation IN (
    'observe','recall','erase','freshness','export','authenticate',
    'project.create','project.suspend','project.resume','credential.issue','credential.revoke',
    'formation.unpark','formation.recover','citation.resolve','record.list','record.history','record.retract','record.correct','record.assert',
    'artifact.put','artifact.get','artifact.list','artifact.delete','artifact.configure',
    'subject.register','subject.get','subject.list','subject.update'));
