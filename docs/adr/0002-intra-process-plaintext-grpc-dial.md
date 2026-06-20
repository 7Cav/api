# ADR 0002: Intra-process plaintext gRPC dial; TLS terminates at the reverse proxy

> **Status: Superseded** by ADR 0006 (single-listener net/http service with
> a hand-owned OpenAPI 3.1 contract), PRD #112 Phase 4; landed in #135 (PR
> #207). With the second listener gone there is no intra-process hop to
> secure, so the plaintext-dial decision below no longer applies. TLS still
> terminates at the reverse proxy.

## Context

The binary runs two listeners (see ADR 0001): the gRPC server on one port
and the HTTP/JSON gateway on another. The gateway dials the gRPC server
to translate each REST call into a gRPC call. Public traffic to the
gateway arrives over HTTPS; we need to decide whether the in-process
gateway → gRPC hop should also be TLS.

## Decision

The gateway dials the local gRPC server with plaintext credentials. TLS
is terminated by a reverse proxy in front of the gateway; the API binary
itself does not handle TLS.

## Consequences

- No certificate management inside the binary; deployment topology owns it.
- The two listeners are co-located in one process by design — the
  plaintext hop never traverses an untrusted network.
- If end-to-end TLS were ever required (e.g., running the gRPC server in
  a separate process or on a separate host), both the gateway dial and
  the gRPC server setup would need to grow TLS credentials.
