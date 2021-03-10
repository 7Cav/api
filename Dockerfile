# Build stage
FROM golang AS build-env

RUN mkdir /src
WORKDIR /src
COPY go.mod .
COPY go.sum .

# Get dependancies - will also be cached if we won't change mod/sum
RUN go mod download

# COPY the source code as the last step
COPY . .

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
RUN go build -a -installsuffix cgo -o /api

# Production stage
FROM scratch
COPY --from=build-env /api /

COPY out out
EXPOSE 10000
EXPOSE 11000
CMD ["/api", "serve"]
