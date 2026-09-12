-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- Source-owned spelling receipts may differ in case or whitespace, but migration 0043 guarantees
-- that they normalize to the named entity's one identity key. Refuse an impossible cache before
-- removing it; silently dropping a distinct normalized name could make retained memory unreachable.
DO $body$
BEGIN
    IF EXISTS (
        SELECT 1 FROM {schema}.entity e
        CROSS JOIN LATERAL unnest(e.normalized_aliases) alias(normalized_name)
        WHERE alias.normalized_name <> e.normalized_name
    ) THEN
        RAISE EXCEPTION 'normalized alias cache conflicts with canonical entity identity';
    END IF;
END
$body$;

DROP INDEX {schema}.entity_aliases_idx;
ALTER TABLE {schema}.entity
    DROP CONSTRAINT entity_identity_kind_chk,
    DROP COLUMN normalized_aliases,
    ADD CONSTRAINT entity_identity_kind_chk CHECK (
        (identity_kind = 'named' AND speaker_subject_id IS NULL) OR
        (identity_kind = 'speaker' AND speaker_subject_id IS NOT NULL AND speaker_subject_id <> ''
          AND canonical_name = 'speaker' AND normalized_name = 'speaker' AND entity_type = 'person'
          AND aliases = '{}'::text[]));
