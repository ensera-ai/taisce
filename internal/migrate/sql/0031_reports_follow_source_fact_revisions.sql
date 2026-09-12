-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- A retained observation can acquire different projections without being erased. Model input
-- carries the revision it read; persistence checks that revision under a conflicting row lock.
ALTER TABLE {schema}.observation
    ADD COLUMN fact_revision bigint NOT NULL DEFAULT 1 CHECK (fact_revision >= 1),
    ADD CONSTRAINT observation_fact_revision_uniq UNIQUE(scope,observation_id,fact_revision);

-- Reports without revision provenance cannot be validated. They are derived, regenerable content.
DELETE FROM {schema}.community_report;
ALTER TABLE {schema}.projection_dependency
    ADD COLUMN report_source_revision bigint,
    ADD CONSTRAINT dependency_report_revision_chk CHECK (
        (projection_kind='community_report' AND report_source_revision IS NOT NULL AND report_source_revision>0)
        OR (projection_kind<>'community_report' AND report_source_revision IS NULL)),
    ADD CONSTRAINT dependency_report_revision_fk FOREIGN KEY(scope,source_observation_id,report_source_revision)
        REFERENCES {schema}.observation(scope,observation_id,fact_revision) DEFERRABLE INITIALLY DEFERRED;

-- The function runs with the writer's existing DML privileges. It grants no new authority. Fact
-- changes invalidate every report registered to their sources, including substituted parents.
CREATE FUNCTION {schema}.invalidate_fact_reports() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
DECLARE
    source_ids uuid[];
    source_id uuid;
BEGIN
    IF TG_TABLE_SCHEMA <> '{schema}' THEN
        RAISE EXCEPTION 'invalid report invalidation trigger target';
    END IF;
    IF TG_TABLE_NAME='fact' AND TG_OP IN ('UPDATE','DELETE') THEN
        SELECT array_agg(DISTINCT source_observation_id) INTO source_ids
          FROM {schema}.fact_evidence WHERE scope=OLD.scope AND fact_id=OLD.fact_id;
    ELSIF TG_TABLE_NAME='fact_evidence' THEN
        IF TG_OP='INSERT' THEN
            source_ids := ARRAY[NEW.source_observation_id];
        ELSIF TG_OP='DELETE' THEN
            source_ids := ARRAY[OLD.source_observation_id];
        ELSE
            source_ids := ARRAY[OLD.source_observation_id,NEW.source_observation_id];
        END IF;
    ELSE
        RAISE EXCEPTION 'invalid report invalidation trigger target';
    END IF;
    -- Sorted observation locks precede report deletion. Report writers take the same locks before
    -- insertion, so either their snapshot is refused or this transaction deletes their report.
    FOR source_id IN SELECT DISTINCT unnest(source_ids) ORDER BY 1 LOOP
        UPDATE {schema}.observation SET fact_revision=fact_revision+1 WHERE observation_id=source_id;
    END LOOP;
    DELETE FROM {schema}.community_report r USING {schema}.projection_dependency d
     WHERE d.scope=r.scope AND d.report_ref=r.report_id
       AND d.projection_kind='community_report' AND d.source_observation_id=ANY(source_ids);
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER fact_report_revision BEFORE UPDATE OR DELETE ON {schema}.fact
    FOR EACH ROW EXECUTE FUNCTION {schema}.invalidate_fact_reports();
CREATE TRIGGER evidence_report_revision AFTER INSERT OR UPDATE OR DELETE ON {schema}.fact_evidence
    FOR EACH ROW EXECUTE FUNCTION {schema}.invalidate_fact_reports();
REVOKE ALL ON FUNCTION {schema}.invalidate_fact_reports() FROM PUBLIC;
