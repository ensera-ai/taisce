-- Runs once, at cluster bring-up, from /docker-entrypoint-initdb.d.
--
-- Extensions are registered HERE rather than by the migrator, for two reasons: CREATE EXTENSION needs
-- privileges the memory runtime is not granted, and a missing extension should fail while
-- the cluster is coming up rather than halfway through an instance migration.
--
-- Three, and each has a reader in this codebase. See the Dockerfile for what was removed and why.

-- The embedding index. `chunk.embedding` is vector(1024) and every project partition carries an HNSW
-- over it.
CREATE EXTENSION IF NOT EXISTS vector;

-- Scalar types alongside a range in ONE GiST index, and in an exclusion constraint.
--
-- Both are load-bearing. The traversal indexes lead with `scope` and an entity id and end with the
-- `valid` range; without this, PostgreSQL answers "data type text has no default operator class for
-- access method gist" and the index cannot be created. And G4's supersession constraint —
-- EXCLUDE (scope WITH =, subject WITH =, predicate WITH =, valid WITH &&) — is impossible without
-- it, which is what would push that invariant back out into an application lock.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Resident via shared_preload_libraries, but the view still needs the extension registered before it
-- can be queried. Without this the library collects and every attempt to READ it fails, which looks
-- exactly like the setting not working.
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
