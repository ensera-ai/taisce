# Taisce.
#
# The substrate is a container because the schema depends on four extensions, and a test that skips
# when they are absent proves nothing about a system whose whole design is in its DDL.

SUBSTRATE_VERSION ?= 0.1.0
TEST_DSN ?= postgres://postgres:taisce@localhost:55432/taisce?sslmode=disable
PERF_PROJECT ?= taisce-perf
PERF_PORT ?= 18080
PERF_POSTGRES_PORT ?= 55433
PERF_POSTGRES_PASSWORD ?= taisce-perf
PERF_TEST_DSN ?= postgres://postgres:$(PERF_POSTGRES_PASSWORD)@localhost:$(PERF_POSTGRES_PORT)/taisce?sslmode=disable
PERF_COMPOSE = TAISCE_PORT=$(PERF_PORT) TAISCE_PERF_POSTGRES_PORT=$(PERF_POSTGRES_PORT) \
	TAISCE_PERF_POSTGRES_PASSWORD=$(PERF_POSTGRES_PASSWORD) \
	docker compose --project-name $(PERF_PROJECT) -f compose.yaml -f compose.perf.yaml

.PHONY: substrate db-up db-down test licence signoff version test-inference golden build contract freeze-contract \
	perf-config perf-up perf-down perf-reset-stats perf-workload perf-stats

substrate:
	docker build -t taisce-postgres:$(SUBSTRATE_VERSION) deploy/postgres

db-up: substrate
	-docker rm -f taiscedb
	docker run -d --name taiscedb -e POSTGRES_PASSWORD=taisce -e POSTGRES_DB=taisce \
		-p 55432:5432 taisce-postgres:$(SUBSTRATE_VERSION)
	@until docker exec taiscedb psql -U postgres -d taisce -c 'SELECT 1' >/dev/null 2>&1; do sleep 1; done
	@echo "substrate ready on 55432"

db-down:
	-docker rm -f taiscedb

# An isolated, durable PostgreSQL qualification stack. `perf-down` preserves its volume so a warm
# run can follow a cold one; the destructive volume-removal command is documented rather than hidden
# behind an easy-to-mistype target.
perf-config:
	$(PERF_COMPOSE) config --quiet

perf-up: perf-config
	$(PERF_COMPOSE) up -d --build
	@until $(PERF_COMPOSE) exec -T postgres pg_isready -U postgres -d taisce >/dev/null 2>&1; do sleep 1; done
	@echo "qualification stack ready: api=$(PERF_PORT) postgres=$(PERF_POSTGRES_PORT)"

perf-down:
	$(PERF_COMPOSE) down

perf-reset-stats:
	$(PERF_COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d taisce \
		-c "SELECT pg_stat_reset_shared('io')" \
		-c "SELECT pg_stat_reset_shared('wal')" \
		-c "SELECT pg_stat_reset_shared('checkpointer')" \
		-c "SELECT pg_stat_reset()" \
		-c "SELECT pg_stat_statements_reset()"

# This is a repeatable database regression workload, not a production capacity claim. Replace it
# with the agreed HTTP workload and corpus before assigning throughput or latency targets.
perf-workload:
	/usr/bin/time -p env TAISCE_TEST_DSN="$(PERF_TEST_DSN)" go test -count=1 -timeout 15m \
		./cmd/taisce ./internal/api ./internal/credential ./internal/formation \
		./internal/infra/pg ./internal/migrate ./internal/recall

perf-stats:
	$(PERF_COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d taisce \
		-f - < scripts/postgres-profile.sql

# No skip-when-absent arm. TAISCE_TEST_DSN unset makes the database tests skip, and a green run that
# skipped everything is the failure mode this target exists to prevent.
#
# Coverage is computed here rather than in a separate target, because a gate nobody runs is not a
# gate. -coverpkg=./... because the default counts only what a package's own tests reach, which
# understates a codebase exercised end to end and would let the number fall while looking steady.
COVER_PROFILE ?= coverage.out

# -timeout 30m: the store package alone takes nine minutes here and longer on a CI runner, and Go's
# default of ten minutes per package would fail a slow-but-passing suite as if it had hung.
# -count=1: a cached test result is keyed on the binary, its flags and the files and variables the
# test read, and the database's state is none of those. Without it a runner restoring the build
# cache from an earlier job on the same tree reports packages as passed that never opened a
# connection (#166), and locally a schema change nothing in the Go inputs records is invisible.
test: licence
	TAISCE_TEST_DSN="$(TEST_DSN)" go test -count=1 -timeout 30m -coverpkg=./... -coverprofile=$(COVER_PROFILE) ./...
	./scripts/coverage-gate.sh $(COVER_PROFILE)

# Every published file carries a copyright header. Checked rather than documented, because a licence
# stated once at the root is one file move from a package published without one — and publishing is
# irreversible.
licence:
	./scripts/licence-gate.sh

# The public contract, rendered from the surface that serves it.
#
# Generated rather than written, because a hand-maintained description of a shape diverges from the
# shape the first time somebody adds a field without knowing the document exists. Committed rather
# than built on demand, because a change to a contract other people's code depends on has to appear
# in a diff a reviewer sees. TestTheContractDocumentDescribesTheSurfaceThatServes fails when the two
# disagree, so running this is a deliberate step and never an automatic one.
contract:
	go run scripts/contract.go

# Freezing is a decision, not a regeneration: it writes the snapshot every later build of the
# version is held to (D126). Run it when a version is declared frozen, and read the diff.
freeze-contract:
	go run scripts/contract.go -freeze internal/api/testdata/contract-v1.json

# Who authored the commits in a change. It cannot decide who has signed the contributor agreement —
# that record lives with the signing service — and it makes the question answerable.
signoff:
	./scripts/signoff-gate.sh

# Extraction against a real model: the citability check, and the regression corpus.
#
# Separate because it costs money and because it sends its fixture text to whichever provider is
# configured — a deliberate act, so a deliberate command.
#
# The corpus is what says a change to the prompt wording did not silently cost a relation. That has
# happened once already and nothing noticed, so the prompt's golden test fails with an instruction to
# run this target: rule 14 is that a prompt file does not change without its corpus running.
#
# The test is behind a build tag rather than an environment check, so under this target missing
# configuration FAILS rather than skips. A target whose whole purpose is to reach a live model, and
# which passes when it did not, is worse than no target.
#
#   make test-inference                        against a model on this machine
#   make test-inference INFERENCE_PROFILE=openrouter   against a hosted one
#
# A profile sets the endpoint, the model and the allowlist. The key is yours to export:
#
#   TAISCE_INFERENCE_API_KEY    bearer token, if the endpoint wants one
#
# The allowlist is the mechanism, not a formality. Empty permits nothing, because an allowlist that
# opens when unset is one forgotten variable away from not being one — and every host that receives
# somebody's words is listed, including a second endpoint that only embeds.
# Which model this runs against. `local`, `openrouter` or `gpu` — see deploy/inference/.
#
# A profile carries an endpoint, a model and an allowlist. It never carries a key: a credential in a
# committed file is a credential in the repository, so keys come from the environment and a profile
# that needs one says so in its own comments.
#
# The profile is loaded first and the surrounding environment wins, so a single variable can be
# overridden for one run without editing anything.
INFERENCE_PROFILE ?= local

test-inference:
	@test -f deploy/inference/$(INFERENCE_PROFILE).env || \
		{ echo "no such profile: deploy/inference/$(INFERENCE_PROFILE).env"; exit 1; }
	@echo "inference profile: $(INFERENCE_PROFILE)"
	set -a; . ./deploy/inference/$(INFERENCE_PROFILE).env; set +a; \
	TAISCE_TEST_DSN="$(TEST_DSN)" go test -tags inference -count=1 -v -timeout 3h ./internal/infra/inference/ && \
	TAISCE_TEST_DSN="$(TEST_DSN)" go test -tags inference -count=1 -v -timeout 5m ./internal/infra/pg/ -run '^TestConfiguredEmbeddingProvider' && \
	TAISCE_TEST_DSN="$(TEST_DSN)" go test -tags inference -count=1 -v -timeout 5m ./internal/api/ -run '^TestConfiguredPassageProvider'

# Rewrite the prompt golden from the prompt file.
#
# Run this AFTER test-inference, never instead of it. The golden is what makes a prompt edit visible;
# the corpus is what says the edit was an improvement rather than a trade nobody priced. Regenerating
# without measuring turns a failing test into a passing one and changes nothing about the behaviour it
# was warning about.
golden:
	go test ./internal/infra/inference/ -run TestTheComposedPrompt -update-golden -count=1

# What this commit is. x.y.z on the working history, and the x.y.0 it would be released as.
version:
	@echo "working:  $$(./scripts/version.sh current)"
	@echo "release:  $$(./scripts/version.sh next-release)"

build:
	go build -ldflags "-X main.version=$$(./scripts/version.sh current)" ./...
