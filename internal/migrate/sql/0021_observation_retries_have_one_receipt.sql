-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
--
-- Retry identity is operation metadata, not another copy of an observation. A key is scoped to a
-- project and never reusable there. Deletion removes the content fingerprint and source link while
-- retaining a minimal key tombstone, so an old queued retry cannot resurrect erased words.
ALTER TABLE {schema}.observation
    ADD CONSTRAINT observation_scope_id_uniq UNIQUE (scope, observation_id);

CREATE TABLE {schema}.observation_retry (
    scope text NOT NULL,
    key_digest bytea NOT NULL CHECK (octet_length(key_digest) = 32),
    request_digest bytea CHECK (octet_length(request_digest) = 32),
    observation_id uuid,
    PRIMARY KEY (scope, key_digest),
    FOREIGN KEY (scope, observation_id)
        REFERENCES {schema}.observation (scope, observation_id),
    CHECK ((observation_id IS NULL) = (request_digest IS NULL))
);
CREATE INDEX observation_retry_source_idx ON {schema}.observation_retry (observation_id)
    WHERE observation_id IS NOT NULL;

CREATE FUNCTION {schema}.clear_observation_retry_payload() RETURNS trigger
LANGUAGE plpgsql AS $body$
BEGIN
    UPDATE {schema}.observation_retry
       SET observation_id = NULL, request_digest = NULL
     WHERE observation_id = OLD.observation_id AND scope = OLD.scope;
    RETURN OLD;
END
$body$;
CREATE TRIGGER observation_retry_forgets_payload
    BEFORE DELETE ON {schema}.observation FOR EACH ROW
    EXECUTE FUNCTION {schema}.clear_observation_retry_payload();
