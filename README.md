<p align="center"><img src="https://github.com/7Cav/api/blob/develop/logo.png"></p>

# Cav API

An HTTP/JSON API that serves 7Cav community and roster data. It is a read
layer over the forum's MySQL database, written in Go on the standard
library `net/http` stack.

## Clients

### Authentication

The API is guarded by a `Bearer` token. Tokens are issued and managed in
the forum admin UI; each one carries a set of scopes that gate which parts
of the surface it can read. Send it on every request:

```
Authorization: Bearer <your token>
```

### HTTP/JSON

The interactive documentation lives at [api.7cav.us](https://api.7cav.us).

> To try the API from the docs page, click the `Authorize` button at the
> top and paste your bearer token.

Wrap the requests in whichever language or HTTP client you prefer.

#### NodeJS

```js
const axios = require('axios');
const token = "<your token>";

const client = axios.create({
  baseURL: 'https://api.7cav.us/api/v1',
  withCredentials: false,
  headers: {
    Accept: 'application/json',
    'Content-Type': 'application/json',
    Authorization: `Bearer ${token}`
  }
});

client.get("milpacs/profile/id/1")
    .then(res => {
        console.log(res.data)
    });
```

#### Go

```go
package main

import (
    "context"
    "fmt"
    "golang.org/x/oauth2"
)

func main() {
    ctx := context.Background()
    client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{
        AccessToken: "<your token>",
        TokenType:   "Bearer",
    }))

    res, err := client.Get("https://api.7cav.us/api/v1/milpacs/profile/id/1")
    if err != nil {
        panic(err)
    }
    fmt.Println(res)
}
```

## Architecture

The service runs as a single public listener that serves the whole `/api`
surface plus the documentation UI. There is no second port and no gRPC: an
earlier version split the process into a gRPC server and a generated
HTTP/JSON gateway, but that design was retired in [PRD #112][prd]
("Goodbye gRPC"). See [ADR 0006][adr6] for why.

The contract is checked into the repo, not generated:

- the **types package** (`types/`) holds the hand-written wire types;
- the **OpenAPI 3.1 spec** (`openapi/openapi.yaml`) is the reference
  document served at the docs URL;
- the **golden corpus** (`contract/goldens/`) records one
  request/response per public route.

`contract/spec_test.go` validates the spec against the corpus in both
directions, so the three stay in step and any drift fails CI naming the
operation and field. The domain glossary and the add-an-endpoint checklist
live in [`CONTEXT.md`](CONTEXT.md).

[prd]: https://github.com/7Cav/api/issues/112
[adr6]: docs/adr/0006-single-listener-net-http-and-hand-owned-openapi.md

## Running

In production the API runs behind an nginx that terminates TLS in front of
the single HTTP listener. The `docker-compose.yaml` in this repo is a
prod-shaped template; the live compose file is customized and kept out of
the repo.

You need a copy of the 7Cav XenForo database reachable from the container
(see the `DB_*` environment variables in `docker-compose.yaml`). Then:

```shell
docker compose up -d
```

## Development

With Go installed you can build and run the API directly. There is no code
generation or tooling install step:

```shell
go build ./...
go run main.go serve
```

`go run main.go serve` needs the database environment variables set (see
`docker-compose.yaml` for the full list).

### Tests

```shell
go test ./...          # unit + contract suite, no external services
make test-integration  # adds the dockerized MariaDB harness (testdb/)
```

`make lint` runs `go vet`. The contract suite (`contract/`) replays the
golden corpus against the live stack and validates the OpenAPI spec; run it
before pushing changes that touch the wire surface.

### Adding an endpoint

Wire types → handler → route registration → spec operation → goldens. The
spec and golden steps are CI-enforced. The full checklist is in
[`CONTEXT.md`](CONTEXT.md); the in-code long form is in the package docs of
`rest/rest.go` and `types/types.go`.
