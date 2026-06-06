generate:
	buf generate

test:
	go test ./...

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

test-integration: testdb-up
	TESTDB_ADDR=$$($(TESTDB_COMPOSE) port mariadb 3306) go test ./...

lint:
	buf lint
	buf breaking --against 'https://github.com/7cav/api.git#branch=develop'

certs:
	rm -rf out/
	certstrap init --common-name "ExampleCA" --passphrase ""
	certstrap request-cert --common-name localhost --ip 0.0.0.0,127.0.0.1 --passphrase ""
	certstrap sign localhost --CA "ExampleCA"

install:
	go install \
		google.golang.org/protobuf/cmd/protoc-gen-go \
		google.golang.org/grpc/cmd/protoc-gen-go-grpc \
		github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway \
		github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2
	go get \
		github.com/bufbuild/buf/cmd/buf \
		github.com/square/certstrap \
		github.com/spf13/cobra

evans:
	evans \
	--tls -cert out/localhost.crt --certkey out/localhost.key --cacert out/ExampleCA.crt \
	--path /home/jarvis/.cache/buf/mod/grpc-ecosystem/grpc-gateway/240eb01580e34380ae1d138426e0174f/ \
	--path /home/jarvis/.cache/buf/mod/beta/googleapis/1dc4674e3cb949b388204fa2dc321be7 \
	--path . proto/milpacs.proto \
	-p 10000
