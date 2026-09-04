# Memra's substrate: PostgreSQL + pgvector.
#
# ── WHY BUILD AN IMAGE AT ALL ────────────────────────────────────────────────────────────────────────
# `pgvector/pgvector:pg18` would cover the vector half on its own, so building our own has to justify
# itself. It does, for two reasons specific to this product:
#
#   1. SUPPLY CHAIN. Memra is sold on being able to say exactly where data lives and what touches it.
#      "We build our substrate image from the official base, pin every version, and can tell you what is
#      in it" is part of that claim. Inheriting a third-party image weakens it for no saving.
#   2. HEADROOM. Extensions this substrate needs are not in any stock image, and adding one is a
#      clone-and-make in the builder stage — cheap only because the builder stage exists. pg_textsearch
#      (BM25) already arrived this way; pgvectorscale (StreamingDiskANN) is the next candidate.
#
# ── WHY pg_textsearch AND NOT ParadeDB's pg_search ──────────────────────────────────────────────────
# Two reasons, and the second is the one that decided it.
#   LICENCE. pg_textsearch is under the PostgreSQL License — permissive, use anywhere for anything.
#   pg_search is AGPL-3.0 plus a commercial licence, which is a live problem for software we run as a
#   service: the network clause reaches a hosted deployment, which is the only kind we have.
#   TOKENISATION. pg_textsearch tokenises through Postgres's own to_tsvector and takes a `text_config`,
#   so it inherits the per-language stemming the baseline migration sets up — including the Arabic
#   snowball stemmer.
#   pg_search tokenises with Tantivy, which would discard that work and answer to a different tokeniser.
# ────────────────────────────────────────────────────────────────────────────────────────────────────

ARG PG_MAJOR=18
ARG PGVECTOR_REF=v0.8.6
ARG PGTEXTSEARCH_REF=main

# ── WHY ALPINE ───────────────────────────────────────────────────────────────────────────────────────
# Measured, not preferred. The Debian (trixie) base carried 14 CRITICAL CVEs, and every one of them was
# inherited rather than ours: perl, libperl, perl-modules, libxml2 and glibc — packages a database image
# ships because the distribution does, not because PostgreSQL needs them at runtime. Rebuilding on the
# newest patch tag did not help, because 18.6-trixie WAS the newest.
#
# Alpine drops that surface rather than patching it: no perl, musl instead of glibc, and a base an order
# of magnitude smaller. The cost is real and worth naming — musl is not glibc, so an extension that
# assumes glibc behaviour will not build here, which is a constraint on what this substrate can adopt
# later. It is the right trade for a component whose whole job is to hold customer data.
# ── MOVING AN EXISTING CLUSTER ONTO THIS IMAGE IS NOT A DROP-IN ──────────────────────────────────────
# Both bases report the same locale — libc provider, en_US.utf8 — but the libc differs, and glibc and
# musl do not sort en_US.utf8 identically. PostgreSQL will happily start on a PGDATA created by the
# other one; what breaks is quieter than a startup failure. Every btree index on a text column was built
# in the old collation's order, so lookups can miss rows that are present, and a unique index can admit
# a duplicate it should have rejected. Nothing errors.
#
# So an existing cluster needs a dump and restore, or at minimum a REINDEX of every text index — it is
# not a matter of pulling the new tag. A fresh volume, which is what the local stack does, has no such
# problem.
ARG PG_BASE=postgres:18.6-alpine3.24

# ---------------------------------------------------------------------------------------------------
# Builder — compiles the extension against the server headers of the EXACT base the final stage uses.
# ---------------------------------------------------------------------------------------------------
FROM ${PG_BASE} AS build
ARG PG_BASE
ARG PG_MAJOR
ARG PGVECTOR_REF
ARG PGTEXTSEARCH_REF

# DESTDIR stages the install tree so the final stage takes precisely what `make install` produced — a
# handful of .so/.control/.sql files — and nothing else. Purging the toolchain in a single RUN would also
# keep it out of the final layer, but it cannot prove the layer is clean; copying an explicit tree can,
# and it makes what ships reviewable in one COPY.
RUN set -eux; \
    # Builder stage only — none of this reaches the final image, which takes just the staged tree below.
    # ca-certificates is not in the base and git needs it to clone over https; without it the failure is
    # an opaque "Problem with the SSL CA cert" rather than a missing-package error.
    apk add --no-cache --virtual .build-deps build-base git ca-certificates; \
    PG_CONFIG="/usr/local/bin/pg_config"; \
    \
    git clone --depth 1 --branch "${PGVECTOR_REF}" https://github.com/pgvector/pgvector.git /tmp/pgvector; \
    # with_llvm=no skips emitting JIT bitcode, which is what keeps this base-agnostic: building bitcode
    # requires a clang whose version matches the one PostgreSQL itself was compiled with, so pinning it
    # would tie this image to a specific Alpine release and break on every base bump. The extensions run
    # identically without it — only JIT inlining of their functions is lost.
    make -C /tmp/pgvector OPTFLAGS="" with_llvm=no PG_CONFIG="${PG_CONFIG}"; \
    make -C /tmp/pgvector with_llvm=no PG_CONFIG="${PG_CONFIG}" DESTDIR=/staging install; \
    \
    git clone --depth 1 --branch "${PGTEXTSEARCH_REF}" https://github.com/timescale/pg_textsearch.git /tmp/pg_textsearch; \
    make -C /tmp/pg_textsearch with_llvm=no PG_CONFIG="${PG_CONFIG}"; \
    make -C /tmp/pg_textsearch with_llvm=no PG_CONFIG="${PG_CONFIG}" DESTDIR=/staging install; \
    \
    rm -rf /tmp/pgvector /tmp/pg_textsearch

# ---------------------------------------------------------------------------------------------------
# Final — the stock image plus pgvector. No compiler, no headers, no apt state.
# ---------------------------------------------------------------------------------------------------
FROM ${PG_BASE}
ARG PG_BASE
ARG PG_MAJOR
ARG PGVECTOR_REF
ARG PGTEXTSEARCH_REF

COPY --from=build /staging/ /

# pg_textsearch must be resident before the server accepts a CREATE EXTENSION, so it goes in
# shared_preload_libraries. Appending to the conf SAMPLE rather than the live conf is what makes it apply:
# initdb copies the sample into PGDATA the first time the cluster is created, so a fresh volume gets the
# setting without anyone remembering to pass -c. pg_stat_statements rides along for the same reason.
RUN set -eux; \
    echo "shared_preload_libraries = 'pg_textsearch,pg_stat_statements'" \
        >> /usr/local/share/postgresql/postgresql.conf.sample

# Extension bootstrap ships IN the image. The official entrypoint runs everything in this directory once,
# against POSTGRES_DB, the first time a cluster is created — so `docker run` produces a database Memra can
# migrate against with no external configuration. That matters twice over: it is what makes one-command
# evaluation real, and it is what lets the integration suite use the same bootstrap production uses instead
# of hand-rolling its own (which is how the schema and the tests drift apart).
#
# CREATE EXTENSION lives here rather than in a migration because it needs privileges the per-service
# migration roles are not granted, and because a missing extension should fail while the cluster is coming
# up rather than halfway through a migration.
COPY initdb/ /docker-entrypoint-initdb.d/

LABEL org.opencontainers.image.title="memra-postgres" \
      org.opencontainers.image.description="PostgreSQL ${PG_MAJOR} + pgvector ${PGVECTOR_REF} + pg_textsearch (BM25) — Memra's substrate" \
      org.opencontainers.image.source="https://github.com/memra/memra" \
      org.opencontainers.image.licenses="PostgreSQL"
