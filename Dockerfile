# Build stage
FROM golang AS build-env

RUN mkdir /src
WORKDIR /src
COPY go.mod .
COPY go.sum .

# Get dependencies - will also be cached if we won't change mod/sum
RUN go mod download

# COPY the source code as the last step. Phase 4 (#135) retired the proto/buf
# codegen toolchain, so there is no `make install` / buf install / `make
# generate` step: the binary builds straight from committed Go.
COPY . .

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
RUN go mod tidy

# VERSION is supplied by the release workflow via --build-arg from
# the GitHub release tag (`github.ref_name`). Defaults to "dev" for
# local docker builds so `Starting 7Cav API version:` shows something
# meaningful even without the tag flow. It is also stamped onto the
# served OpenAPI spec's info.version at request time (rest.DocsHandler).
ARG VERSION=dev
RUN go build -a -ldflags="-s -w -X github.com/7cav/api/servers.version=${VERSION}" -installsuffix cgo -o /api

# Production stage
FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=build-env /api /

# Single public listener (#134/#135); the old gRPC port (:10000) is gone.
# The Prometheus metrics listener (:9090) is internal-only and not published.
EXPOSE 11000
CMD ["/api", "serve"]
