# Taisce.
#
# The substrate is a container because the schema depends on four extensions, and a test that skips
# when they are absent proves nothing about a system whose whole design is in its DDL.

SUBSTRATE_VERSION ?= 0.1.0
TEST_DSN ?= postgres://postgres:taisce@localhost:55432/taisce?sslmode=disable

.PHONY: substrate db-up db-down test build

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

build:
	go build ./...
