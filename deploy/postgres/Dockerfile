# Taisce's substrate: PostgreSQL 18 + pgvector + btree_gist.
#
# ── WHAT IS HERE, AND WHY EACH ONE EARNS ITS PLACE ──────────────────────────────────────────────────
# An extension in this image is a permanent supply-chain and crash-domain cost: it runs inside the
# backend process, so a fault in it takes the cluster, and the cluster is the product. So the test is
# not "might we want this" but "what reads it today".
#
#   vector (pgvector)   REQUIRED. `chunk.embedding` is vector(1024), and every project partition
#                       carries an HNSW index over it. Nothing retrieves without this.
#   btree_gist          REQUIRED. The traversal indexes lead with `scope` and an entity id and end
#                       with a `tstzrange`; without btree_gist PostgreSQL refuses the index outright
#                       ("data type text has no default operator class for access method gist"), and
#                       G4's supersession EXCLUDE constraint is impossible. Found by running the
#                       migration, not by reading it.
#   pg_stat_statements  Operational. The only way to answer "which query got slower" after a deploy
#                       without adding a per-query instrumentation path to the server.
#
# ── WHAT WAS REMOVED AFTER REVIEW ───────────────────────────────────────────────────────────────────
# Both were present in an earlier draft of this file and neither had a reader here.
#
#   pg_textsearch (BM25)  No lexical index exists in this schema and none is designed: retrieval
#                         anchors on entities and reads passages as evidence, and the three
#                         embedded surfaces are vector, not lexical. It is also the most
#                         expensive thing this image could carry — a from-source compile in the
#                         builder stage and a `shared_preload_libraries` entry, for nothing. If
#                         hybrid retrieval is ever wanted it comes back WITH the query that reads it.
#   pgcrypto              `gen_random_uuid()` has been core since PostgreSQL 13, which is the only
#                         thing this schema used it for.
#
# The rule those two illustrate is G13's, applied to infrastructure: something installed with no
# reader is a cost with no mechanism behind it.
#
# ── WHY ALPINE, AND THE CONSTRAINT IT IMPOSES ───────────────────────────────────────────────────────
# A smaller base is a smaller attack surface, and the difference is in packages the database does not
# need at runtime but a distribution ships anyway. The cost is real and worth naming: musl is not
# glibc, so an extension assuming glibc behaviour will not build here — a constraint on what this
# substrate can adopt later, not a theoretical one.
#
# ── MOVING AN EXISTING CLUSTER ONTO THIS IMAGE IS NOT A DROP-IN ─────────────────────────────────────
# Both bases report the same locale, but glibc and musl do not sort en_US.utf8 identically, and every
# btree index on a text column was built in the old collation's order. Lookups can miss rows that are
# present and a unique index can admit a duplicate. Nothing errors. An existing cluster needs a dump
# and restore, or at minimum a REINDEX of every text index. A fresh volume has no such problem.

ARG PG_MAJOR=18
ARG PGVECTOR_REF=v0.8.6
ARG PG_BASE=postgres:18.6-alpine3.24

# ---------------------------------------------------------------------------------------------------
# Builder — compiles against the server headers of the EXACT base the final stage uses.
# ---------------------------------------------------------------------------------------------------
FROM ${PG_BASE} AS build
ARG PGVECTOR_REF

# DESTDIR stages the install tree so the final stage takes precisely what `make install` produced and
# nothing else. Purging the toolchain in one RUN would also keep it out of the final layer, but it
# cannot PROVE the layer is clean; copying an explicit tree can, and makes what ships reviewable in
# one COPY.
RUN set -eux; \
    # ca-certificates is not in the base and git needs it to clone over https; without it the failure
    # is an opaque SSL error rather than a missing package.
    apk add --no-cache --virtual .build-deps build-base git ca-certificates; \
    PG_CONFIG="/usr/local/bin/pg_config"; \
    git clone --depth 1 --branch "${PGVECTOR_REF}" https://github.com/pgvector/pgvector.git /tmp/pgvector; \
    # with_llvm=no skips JIT bitcode, which is what keeps this base-agnostic: building bitcode needs a
    # clang matching the one PostgreSQL was compiled with, so pinning it would tie this image to one
    # Alpine release and break on every base bump. Only JIT inlining of the extension's own functions
    # is lost.
    make -C /tmp/pgvector OPTFLAGS="" with_llvm=no PG_CONFIG="${PG_CONFIG}"; \
    make -C /tmp/pgvector with_llvm=no PG_CONFIG="${PG_CONFIG}" DESTDIR=/staging install; \
    rm -rf /tmp/pgvector

# ---------------------------------------------------------------------------------------------------
# Final — the stock image plus pgvector. No compiler, no headers, no package state.
# btree_gist and pg_stat_statements ship with PostgreSQL; nothing is compiled for them.
# ---------------------------------------------------------------------------------------------------
FROM ${PG_BASE}
ARG PG_MAJOR
ARG PGVECTOR_REF

COPY --from=build /staging/ /

# Appending to the conf SAMPLE rather than the live conf is what makes it apply: initdb copies the
# sample into PGDATA the first time a cluster is created, so a fresh volume gets the setting without
# anyone remembering to pass -c.
RUN set -eux; \
    echo "shared_preload_libraries = 'pg_stat_statements'" \
        >> /usr/local/share/postgresql/postgresql.conf.sample

# Extension bootstrap ships IN the image. The official entrypoint runs this directory once, against
# POSTGRES_DB, the first time a cluster is created — so `docker run` produces a database the migrator
# can run against with no external configuration. That matters twice: it makes one-command evaluation
# real, and it lets the integration suite use the same bootstrap production uses instead of
# hand-rolling its own, which is how a schema and its tests drift apart.
COPY initdb/ /docker-entrypoint-initdb.d/

LABEL org.opencontainers.image.title="taisce-postgres" \
      org.opencontainers.image.description="PostgreSQL ${PG_MAJOR} + pgvector ${PGVECTOR_REF} + btree_gist — Taisce's substrate" \
      org.opencontainers.image.source="https://github.com/ensera-ai/taisce" \
      org.opencontainers.image.licenses="PostgreSQL"
