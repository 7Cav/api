# Build stage
FROM golang AS build-env

RUN apt-get update && apt-get install -y jq curl

RUN mkdir /src
WORKDIR /src
COPY go.mod .
COPY go.sum .

# Get dependancies - will also be cached if we won't change mod/sum
RUN go mod download
COPY Makefile ./
RUN make install

# Install buf (protobuf generation tool)
RUN curl -sSL $(curl -s https://api.github.com/repos/bufbuild/buf/releases/latest | jq -r '.assets[] | select(.name | contains("buf-Linux-x86_64")) | .browser_download_url') -o /usr/local/bin/buf \
    && chmod +x /usr/local/bin/buf

# COPY the source code as the last step
COPY . .

# RUN make install
RUN make generate

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
RUN go mod tidy

# VERSION is supplied by the release workflow via --build-arg from
# the GitHub release tag (`github.ref_name`). Defaults to "dev" for
# local docker builds so `Starting 7Cav API version:` shows something
# meaningful even without the tag flow.
ARG VERSION=dev
RUN go build -a -ldflags="-s -w -X github.com/7cav/api/servers.version=${VERSION}" -installsuffix cgo -o /api

# Production stage
FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=build-env /api /

EXPOSE 10000
EXPOSE 11000
CMD ["/api", "serve"]
