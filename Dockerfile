FROM golang:1.25 AS builder
WORKDIR /src
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags "-s -w -X main.version=${VERSION}" \
    -o tetherdb ./cmd/tetherdb

FROM scratch
COPY --from=builder /src/tetherdb /tetherdb
# ca-certificates is required for Postgres TLS verification (sslmode=require).
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENTRYPOINT ["/tetherdb"]
CMD ["--config", "/etc/tetherdb/tetherdb.toml", "run"]
