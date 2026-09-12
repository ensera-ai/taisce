-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- The UUID belongs to the authoritative message so loss of a chunk does not change its identity.
-- A future document segment contract is separate from this one-chunk-per-message representation.
ALTER TABLE {schema}.turn_message ADD COLUMN chunk_id uuid;
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM {schema}.chunk WHERE source_message_ordinal IS NOT NULL
        GROUP BY scope,source_observation_id,source_message_ordinal HAVING count(*)>1) THEN
        RAISE EXCEPTION 'message has ambiguous chunk identities';
    END IF;
END $$;
UPDATE {schema}.turn_message m SET chunk_id=c.chunk_id FROM {schema}.chunk c
    WHERE c.source_observation_id=m.observation_id AND c.source_message_ordinal=m.ordinal;
-- No original UUID can be inferred where the derived row was already lost before this boundary.
UPDATE {schema}.turn_message SET chunk_id=gen_random_uuid() WHERE chunk_id IS NULL;
ALTER TABLE {schema}.turn_message ALTER COLUMN chunk_id SET NOT NULL,
    ALTER COLUMN chunk_id SET DEFAULT gen_random_uuid(),
    ADD CONSTRAINT message_chunk_identity_unique UNIQUE(observation_id,ordinal,chunk_id);
ALTER TABLE {schema}.chunk ADD CONSTRAINT chunk_message_identity_fk
    FOREIGN KEY(source_observation_id,source_message_ordinal,chunk_id)
    REFERENCES {schema}.turn_message(observation_id,ordinal,chunk_id)
    ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;
