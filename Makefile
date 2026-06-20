# The proto/buf codegen toolchain was retired in Phase 4 (#135). The API is a
# plain net/http JSON service with hand-written handlers and a hand-owned
# OpenAPI 3.1 spec — there is no generate/install step. `make lint` is `go vet`.

test:
	go test ./...

lint:
	go vet ./...

# Dockerized MariaDB integration harness (testdb/). Tests opt into the
# real database via TESTDB_ADDR; without it they skip.
# The compose project is unique per checkout and the host port ephemeral
# (discovered via `docker compose port`), so parallel worktrees can run
# harnesses side by side.
TESTDB_PROJECT ?= testdb-$(notdir $(CURDIR))
TESTDB_COMPOSE := docker compose -p $(TESTDB_PROJECT) -f testdb/compose.yaml

testdb-up:
	$(TESTDB_COMPOSE) up -d --wait

testdb-down:
	$(TESTDB_COMPOSE) down -v

# Fail loudly if port discovery comes back empty: an empty TESTDB_ADDR
# would silently skip the whole integration suite and exit 0.
test-integration: testdb-up
	@set -eu; addr=$$($(TESTDB_COMPOSE) port mariadb 3306); \
	test -n "$$addr" || { echo "harness port discovery failed" >&2; exit 1; }; \
	TESTDB_ADDR=$$addr go test ./...
