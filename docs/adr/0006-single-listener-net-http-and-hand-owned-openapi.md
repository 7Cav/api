# ADR 0006: Single-listener net/http service with a hand-owned OpenAPI 3.1 contract

> Supersedes ADR 0001 (split-process gRPC + HTTP gateway) and ADR 0002
> (intra-process plaintext gRPC dial). Both described the two-listener,
> proto-driven layout this ADR retires. The deletion landed in #135 (PR
> #207), the final phase of PRD #112 ("Goodbye gRPC").

## Context

ADR 0001 split the API into a gRPC server and an HTTP/JSON gateway, both
generated from `proto/milpacs.proto`, running as two listeners in one
process. ADR 0002 covered the in-process plaintext hop between them. The
design bought one thing: a single source of truth for the gRPC service,
the REST routes, and the OpenAPI document, all falling out of code
generation.

The cost showed up over time. Two known consumers (`7cav-cavbot2` and the
ADR tool) used the HTTP gateway. The gRPC port had no confirmed external
consumer, and the heads-up conversation before cutover (#117) confirmed
nobody generated client code from the served spec either; the spec was
not accurate enough to drive a generated client. So the gRPC surface and
the generated Swagger 2.0 document were carrying weight nothing leaned on.
The proto/buf toolchain, the generated `*.pb.go` and `*.pb.gw.go` files,
and the second listener were maintenance and review surface with no
matching demand.

Meanwhile the read-path latency work in earlier PRD #112 phases removed
the response cache (ADR 0003) and proved the service could be reasoned
about directly. The remaining question was whether we could rewrite onto
plain `net/http` without quietly changing the wire form clients already
depended on.

## Decision

Retire the split-process design. The API is one `net/http` JSON service
with hand-written handlers, behind the same TLS-terminating reverse proxy
as before. There is one public listener; the gRPC listener, the gateway
translation layer, the proto files, and the buf/protoc toolchain are gone.

The contract is no longer a generated artifact. It is a trio that is
checked into the repo and enforced in CI:

- the **types package** (`types/`): the hand-written wire types that
  replaced the generated proto messages;
- the **hand-owned OpenAPI 3.1 spec** (`openapi/openapi.yaml`): the
  reference document served at the docs URL, replacing the generated
  Swagger 2.0 file;
- the **golden corpus** (`contract/goldens/`): one recorded
  request/response per public route, captured from the old stack before
  it was deleted.

The golden corpus is what made the rewrite safe. The goldens were recorded
against the running proto-driven stack, then replayed against the new
`net/http` stack; a response that does not reproduce its golden fails a
test (`contract/spec_test.go`, `contract/golden_test.go`). The same
replay validates the OpenAPI spec in both directions: every operation must
have a witnessing golden, and every golden route must resolve to an
operation. Drift names the operation and the field, so the contract is
machine-checked rather than tribal.

The Swagger 2.0 document is fully retired, not frozen behind an alias.
#117 confirmed no consumer codegens from it and that it was never accurate
enough to use, so there was nothing to keep compatible. The OpenAPI 3.1
document replaces it at the same docs URL; the route paths and response
shapes clients actually call are unchanged.

## Consequences

- Adding an endpoint is no longer a single proto edit. It is a sequence of
  hand-written steps (wire types, handler, route registration, spec
  operation, goldens), written down as the add-an-endpoint checklist in
  `CONTEXT.md`. The spec and golden steps are CI-enforced, so the sequence
  is guarded rather than remembered.
- There is no `make generate` / `make install` / codegen step. `make lint`
  is `go vet`; the build is plain `go build ./...`.
- The wire form is now owned by hand. The conventions the generated stack
  produced (lowerCamelCase JSON, emit-everything, 64-bit ints as strings /
  32-bit as numbers, enum name strings with an `_UNSPECIFIED` zero value)
  are adopted as house style and enforced: by the tag lint in
  `types/wireconventions_test.go` at declaration time, and by the golden
  corpus and spec replay on every covered route.
- No second listener means no intra-process plaintext dial, so the concern
  ADR 0002 documented no longer exists. TLS still terminates at the
  reverse proxy.
- The contract trio is the safety net the generated stack used to provide
  for free. A future rewrite of the same surface replays the same goldens
  and validates the same spec; the corpus is the authoritative wire
  reference, independent of the code that serves it.
