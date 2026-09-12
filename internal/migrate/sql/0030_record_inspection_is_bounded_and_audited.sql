-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- Subject inventory walks deduplicated provenance in cursor order without scanning other subjects.
CREATE INDEX dependency_subject_fact_page_idx ON {schema}.projection_dependency(scope,data_subject_id,fact_ref)
    WHERE projection_kind='fact';
-- History pages seek backwards from their last interval rather than sorting all versions.
CREATE INDEX fact_history_page_idx ON {schema}.fact_history(scope,fact_id,(upper(known)) DESC,history_id DESC);

ALTER TABLE {schema}.audit_entry DROP CONSTRAINT audit_operation_chk;
ALTER TABLE {schema}.audit_entry ADD CONSTRAINT audit_operation_chk CHECK (operation IN (
    'observe','recall','erase','freshness','export','citation.resolve','record.list','record.history',
    'project.create','project.suspend','project.resume',
    'credential.issue','credential.revoke','authenticate','formation.unpark'
));
