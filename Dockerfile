# Build stage
#
FROM golang:1.25-alpine AS builder

ARG APP_VERSION="0.1.0" 

WORKDIR /app

COPY go.mod go.sum main.go ./

RUN apk add --no-cache git && \
    go mod download && \
    CGO_ENABLED=0 GOOS=linux \
        go build -ldflags "-s -w -X main.version=${APP_VERSION}" -a -installsuffix cgo -o simple-docker-exporter main.go

# Final stage
#
FROM alpine:latest

# add certificates for TLS-connections to remote Docker's hosts
RUN apk --no-cache add ca-certificates

WORKDIR /
COPY --from=builder /app/simple-docker-exporter /

EXPOSE 9487

ENTRYPOINT ["/simple-docker-exporter"]
