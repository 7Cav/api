# Build stage
FROM golang AS build-env

RUN mkdir /src
WORKDIR /src
COPY go.mod .
COPY go.sum .

# Get dependancies - will also be cached if we won't change mod/sum
RUN go mod download

# Install buf (protobuf generation tool)
RUN curl -sSL $(curl -s https://api.github.com/repos/bufbuild/buf/releases/latest | grep "browser_download_url.*buf-Linux-x86_64" | cut -d '"' -f 4) -o /usr/local/bin/buf \
    && chmod +x /usr/local/bin/buf

# COPY the source code as the last step
COPY . .

RUN make install
RUN make generate

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
RUN go mod tidy
RUN go build -a -installsuffix cgo -o /api

# Production stage
FROM scratch
COPY --from=build-env /api /

EXPOSE 10000
EXPOSE 11000
CMD ["/api", "serve"]
