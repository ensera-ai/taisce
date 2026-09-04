# Taisce.
#
# The substrate is a container because the schema depends on four extensions, and a test that skips
# when they are absent proves nothing about a system whose whole design is in its DDL.

SUBSTRATE_VERSION ?= 0.1.0
TEST_DSN ?= postgres://postgres:taisce@localhost:55432/taisce?sslmode=disable

.PHONY: substrate db-up db-down test test-inference build

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

# No skip-when-absent arm. TAISCE_TEST_DSN unset makes the database tests skip, and a green run that
# skipped everything is the failure mode this target exists to prevent.
test:
	TAISCE_TEST_DSN="$(TEST_DSN)" go test ./...

# Extraction against a real model. Separate because it costs money and because it sends its fixture
# text to whichever provider is configured — a deliberate act, so a deliberate command.
#
# The test is behind a build tag rather than an environment check, so under this target missing
# configuration FAILS rather than skips. A target whose whole purpose is to reach a live model, and
# which passes when it did not, is worse than no target.
#
#   TAISCE_INFERENCE_ENDPOINT   base URL of an OpenAI-compatible endpoint
#   TAISCE_INFERENCE_API_KEY    bearer token, if the endpoint wants one
#   TAISCE_INFERENCE_EXTRACTOR_MODEL  the model to ask
#   TAISCE_INFERENCE_ALLOWLIST  hosts permitted to receive message content, comma separated.
#                               Empty permits nothing: the default has to be closed, because an
#                               allowlist that opens when unset is one forgotten variable away from
#                               not being one.
test-inference:
	TAISCE_TEST_DSN="$(TEST_DSN)" go test -tags inference -count=1 -v ./internal/infra/inference/

build:
	go build ./...
