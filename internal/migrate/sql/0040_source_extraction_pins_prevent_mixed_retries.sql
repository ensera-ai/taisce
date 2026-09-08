-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- An attempt pins its configuration before inference, including turns producing no claims.
-- This source-owned control record is not a recoverable projection or a completion marker.
CREATE TABLE {schema}.source_extraction (
    scope text NOT NULL,
    source_observation_id uuid NOT NULL,
    extractor_version text NOT NULL CHECK (
        extractor_version IN ('extract/v1','curated/v1') OR extractor_version ~ '^extract/v2:[a-f0-9]{64}$'
    ),
    pinned_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(scope,source_observation_id),
    FOREIGN KEY(scope,source_observation_id) REFERENCES {schema}.observation(scope,observation_id)
        ON DELETE CASCADE
);

CREATE FUNCTION {schema}.extraction_pin_is_immutable() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'source extraction identity is immutable' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER source_extraction_immutable BEFORE UPDATE ON {schema}.source_extraction
    FOR EACH ROW EXECUTE FUNCTION {schema}.extraction_pin_is_immutable();
REVOKE ALL ON FUNCTION {schema}.extraction_pin_is_immutable() FROM PUBLIC;
