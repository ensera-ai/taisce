-- Runs once, at cluster bring-up, from /docker-entrypoint-initdb.d.
--
-- Extensions are registered HERE rather than left to the Memra migrator, because CREATE EXTENSION needs
-- privileges the per-service migration roles are not granted — and because a missing extension should
-- fail while the cluster is coming up, loudly, rather than halfway through a migration.

-- Resident via shared_preload_libraries, but the view still needs the extension registered before
-- pg_stat_statements can be queried. Without this the library collects and every attempt to READ it
-- fails, which looks exactly like the setting not working.
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

-- The embedding index.
CREATE EXTENSION IF NOT EXISTS vector;

-- BM25 relevance ranking. Postgres ts_rank has no IDF, term-frequency saturation or length
-- normalisation; BM25 has all three. Tokenises through to_tsvector, so it inherits the per-language
-- stemming the baseline migration sets up.
CREATE EXTENSION IF NOT EXISTS pg_textsearch;

-- Field-level crypto. Load-bearing for SEC-MEM-015 (personal data reduced to a non-reversible digest
-- wherever it would otherwise appear in a key or identifier) — see docs/03-security.md.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- btree_gist lets scalar types (text, uuid) sit alongside a range in ONE GiST index, and in an
-- exclusion constraint.
--
-- Both are load-bearing rather than convenient. The traversal indexes lead with `scope` and an
-- entity id and end with the `valid` range; without this, PostgreSQL answers "data type text has no
-- default operator class for access method gist" and the index cannot be created at all. And the
-- supersession constraint of M3 — EXCLUDE (scope WITH =, subject WITH =, predicate WITH =,
-- valid WITH &&) — is impossible without it, which is what would push that invariant back out into
-- an application lock.
CREATE EXTENSION IF NOT EXISTS btree_gist;
