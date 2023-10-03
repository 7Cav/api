# Build stage
FROM golang:1.18-alpine As build-env

RUN mkdir /src
WORKDIR /src
COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .

RUN go mod download google.golang.org/grpc/cmd/protoc-gen-go-grpc
RUN go get github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.11.3

RUN apk add make
RUN make install

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
RUN go build -a -installsuffix cgo -o /api

# # Production stage
FROM golang:1.18-alpine
COPY --from=build-env /api /

EXPOSE 10000
EXPOSE 11000
CMD ["/api", "serve"]
