-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- The original generation boundary and model stay immutable. Worker progress can extend a finite
-- target, but only a completed page sequence establishes coverage through that target.
ALTER TABLE {schema}.embedding_build
 ADD COLUMN through_offset bigint NOT NULL DEFAULT -1 CHECK(through_offset>=-1),
 ADD COLUMN covered_through_offset bigint NOT NULL DEFAULT -1 CHECK(covered_through_offset>=-1),
 ADD CONSTRAINT embedding_coverage_within_target CHECK(covered_through_offset<=through_offset);
UPDATE {schema}.embedding_build b SET through_offset=g.through_offset,
 covered_through_offset=CASE WHEN b.state='ready' THEN g.through_offset ELSE -1 END
 FROM {schema}.embedding_generation g WHERE g.scope=b.scope AND g.generation_id=b.generation_id;
