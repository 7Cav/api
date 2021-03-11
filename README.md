# Cav API

A web API using GPRC/protobufs for main communication, but also supplying a more standard HTTP/JSON over grpc-gateway for legacy usage

## Clients

### Authentication

The API is guarded via an OAuth2 bearer token, which you can get via the [7Cav auth service](https://auth.7cav.us/auth/realms/7Cav/account/)

> If you see an empty field for the API Key, go to the 'sessions' tab, and click 'Log out all sessions'. Then, sign in again as usual.

### HTTP/JSON

We still maintain a simple HTTP API which routes to the underlying gRPC API. The more detailed endpoints are only accessible via the gRPC clients, so only use the HTTP API if you really need to and understand the trade-offs.

You can view the automatically generated documentation via [api.7cav.us](https://api.7cav.us)

> If you want to 'try out' the API, ensure you use your bearer token by clicking the 'Authorize' button at the top of the page 

Wrap the requests in whichever flavour of language/HTTP client you wish:

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

    res, err := client.Get("milpacs/profile/id/1")
    if err != nil {
        panic(err)
    }
    fmt.Println(res)
}
```

### gRPC

You probably came here for guidance on using the gRPC clients in your code. It depends on which language you're using, and if they are [supported by gRPC](https://grpc.io/docs/languages/).

An example client written in golang can be found in `cmd/example.go` and can be run via `go run main.go example <id>` to get milpac info for a specific user.

```go
package main

import (
	"context"
    "crypto/tls"
    "fmt"
    "github.com/7cav/api/proto"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials"
    "google.golang.org/grpc/credentials/oauth"
)

func main() {
    ctx := context.Background()
    token := "<token>"
    
    rpcCreds := oauth.NewOauthAccess(&oauth2.Token{AccessToken: token})

    // use TLS config to auto detect SSL/TLS cert from Api Host
    config := &tls.Config{
        InsecureSkipVerify: false,
    }

    opts := []grpc.DialOption{
        grpc.WithTransportCredentials(credentials.NewTLS(config)),
        grpc.WithPerRPCCredentials(rpcCreds),
        grpc.WithBlock(),
    }

    conn, _ := grpc.Dial("api.7cav.us:443", opts...)
    client := proto.NewMilpacServiceClient(conn)
    res, err := client.Profile(context.Background(), &proto.ProfileRequest{UserId: 1})
    if err != nil {
        panic(err)
    }
    fmt.Println(res)
}
```

Otherwise, follow the gRPC tutorials on using 'Client Side Code' and use the `proto/milpacs.proto` schema in your own code-bases.

## Running

In production, we use a customized version of the docker-compose.yml you can see here. The nginx container in front is for handling SSL/TLS termination before we reach the gRPC server.That way internally we don't need to use TLS encryption(needed on client side due to sending bearer tokens)

The following should get you up and running. However you'll need a copy of the 7cav xenforo database imported into the mysql container!

```shell
make certs
docker-compose up -d
```

## Development

So long as you have golang installed, you should be fine to run the following make commands to get setup with the required dependencies:

```shell
make install
```

This will also install [Cobra](https://github.com/spf13/cobra), which is used for creating the boilerplate for each custom command on the generated binary.

When making changes to the proto file, be sure to run the relevant make file to regenerate the exported server interfaces:

```shell
make generate
```
