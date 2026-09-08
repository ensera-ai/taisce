-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0
-- Speaker references are a closed vocabulary; their identity is supplied by observation metadata.
CREATE TABLE {schema}.speaker_term (
    term text PRIMARY KEY CHECK (term = lower(btrim(term)) AND term <> '')
);
INSERT INTO {schema}.speaker_term (term) VALUES
    ('i'), ('me'), ('myself'), ('the user'), ('user'), ('أنا'), ('انا'), ('نفسي');

-- A name-only speaker may already have superseded somebody else's fact. Assigning it to one person
-- cannot recover that history. Refuse this upgrade atomically, before changing identity constraints;
-- the operator must rebuild affected projections from retained observations in a separate schema.
DO $body$
BEGIN
    IF EXISTS (SELECT 1 FROM {schema}.entity e JOIN {schema}.speaker_term t
               ON t.term = e.normalized_name) THEN
        RAISE EXCEPTION 'speaker identity upgrade requires a projection rebuild from retained observations; name-only speaker entities exist';
    END IF;
END
$body$;

ALTER TABLE {schema}.entity
    ADD COLUMN identity_kind text NOT NULL DEFAULT 'named',
    ADD COLUMN speaker_subject_id text,
    ADD CONSTRAINT entity_identity_kind_chk CHECK (
        (identity_kind = 'named' AND speaker_subject_id IS NULL) OR
        (identity_kind = 'speaker' AND speaker_subject_id IS NOT NULL AND speaker_subject_id <> ''
          AND canonical_name = 'speaker' AND normalized_name = 'speaker' AND entity_type = 'person'
          AND aliases = '{}'::text[] AND normalized_aliases = '{}'::text[])),
    DROP CONSTRAINT entity_identity_uniq;
CREATE UNIQUE INDEX entity_named_identity_uniq ON {schema}.entity(scope, normalized_name)
    WHERE identity_kind = 'named';
CREATE UNIQUE INDEX entity_speaker_identity_uniq ON {schema}.entity(scope, speaker_subject_id)
    WHERE identity_kind = 'speaker';
