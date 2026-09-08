-- Copyright 2026 The Taisce Authors
-- SPDX-License-Identifier: Apache-2.0

-- These fields had no readers and enforced no access rule. A caller's credential selects one
-- project; differing content access belongs in distinct projects. Keeping an inert label in
-- exports would invite applications to mistake stored metadata for an enforced security boundary.
ALTER TABLE {schema}.observation DROP COLUMN classification;
ALTER TABLE {schema}.projection_dependency DROP COLUMN classification;
