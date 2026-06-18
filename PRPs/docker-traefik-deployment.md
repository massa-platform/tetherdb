# PRP: Docker + Traefik Deployment for Sink Node

**Status:** draft
**Spec ref:** deployment / infrastructure
**Branch:** TBD

---

## Goal

Package the tetherdb sink node and its PostgreSQL database as a Docker Compose stack.
Traefik acts as the TLS-terminating reverse proxy — it provisions a Let's Encrypt cert
for `tetherdb.dafifi.net` and forwards WebSocket connections to the tetherdb container
over plain HTTP internally. tetherdb never holds cert files in this deployment model.

---

## Behaviour

### Runtime topology

```
SQL Server (SRV01-MTA)
  └─ tetherdb source node (Windows Service)
       └─ dials wss://tetherdb.dafifi.net:443
            └─ Traefik (Docker, port 443)
                 └─ WebSocket proxy → tetherdb sink (Docker, internal port 8443)
                      └─ applies changes → Postgres (Docker, internal port 5432)
```

Traefik terminates TLS. The tetherdb sink container sees a plain `ws://` connection
on its internal listen address. Postgres is reachable only within the Docker network —
no port is exposed to the host.

### Config change: optional TLS files on `[listen]`

Currently `validateListen` requires `tls_cert` and `tls_key` to be non-empty paths
that exist on disk. This must be relaxed:

- If both `tls_cert` and `tls_key` are empty → no-TLS mode (Traefik terminates).
- If either is non-empty → both must be present and the files must exist (existing behaviour).

No other changes to the listener itself — the existing `gorilla/websocket` listener
already accepts plain connections when no TLS config is provided.

---

## Files to create or modify

```
docker-compose.yml                  — new: tetherdb + postgres + traefik services
traefik/traefik.yml                 — new: static Traefik config (entrypoints, ACME)
traefik/acme.json                   — new: empty placeholder (chmod 600, gitignored)
config/tetherdb-sink.toml.example   — new: example sink node TOML config
internal/config/config.go           — modify: make tls_cert/tls_key optional
internal/config/config_test.go      — modify: add tests for no-TLS listen mode
.gitignore                          — modify: add traefik/acme.json
```

---

## docker-compose.yml

Three services: `traefik`, `tetherdb`, `postgres`.

**traefik:**
- Image: `traefik:v3`
- Ports: `80:80`, `443:443`
- Mounts: `./traefik/traefik.yml:/traefik.yml:ro`, `./traefik/acme.json:/acme.json`,
  `/var/run/docker.sock:/var/run/docker.sock:ro`
- No labels — Traefik reads its own static config from `traefik.yml`.

**tetherdb:**
- Image: built from a minimal `Dockerfile` (scratch-based, copies binary)
- Exposes port `8443` internally (not published to host)
- Mounts: `./config/tetherdb-sink.toml:/etc/tetherdb/tetherdb.toml:ro`
- Labels: Traefik router for host `tetherdb.dafifi.net`, WebSocket middleware enabled,
  entrypoint `websecure`, TLS via Let's Encrypt resolver.
- Environment: `PG_USER`, `PG_PASS` (set in `.env`, never in compose file)

**postgres:**
- Image: `postgres:16`
- No published ports — internal only
- Environment: `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` (from `.env`)
- Volume: `pgdata:/var/lib/postgresql/data`

---

## traefik/traefik.yml

```yaml
entryPoints:
  web:
    address: ":80"
    http:
      redirections:
        entryPoint:
          to: websecure
          scheme: https
  websecure:
    address: ":443"

certificatesResolvers:
  letsencrypt:
    acme:
      email: kithinjibrian369@gmail.com
      storage: /acme.json
      httpChallenge:
        entryPoint: web

providers:
  docker:
    exposedByDefault: false
```

---

## Dockerfile

Minimal two-stage build:

```dockerfile
FROM golang:1.25 AS builder
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=$(git describe --tags --always)" -o tetherdb ./cmd/tetherdb

FROM scratch
COPY --from=builder /src/tetherdb /tetherdb
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENTRYPOINT ["/tetherdb"]
CMD ["--config", "/etc/tetherdb/tetherdb.toml", "run"]
```

The `ca-certificates` layer is required so the sink can verify Postgres TLS if
`sslmode=require`.

---

## config/tetherdb-sink.toml.example

```toml
[node]
name    = "sink-prod"
data_dir = "/var/lib/tetherdb"

[management]
address = "127.0.0.1:8080"

[listen]
address = "0.0.0.0:8443"
# tls_cert and tls_key are intentionally absent — Traefik terminates TLS.

[sink]
driver   = "postgres"
host     = "postgres"          # Docker service name
port     = 5432
database = "erp_mirror"
username = "${PG_USER}"
password = "${PG_PASS}"
sslmode  = "disable"           # Internal Docker network — no TLS needed to Postgres
```

---

## Config validator change

In `internal/config/config.go`, `validateListen`:

**Current behaviour:** both `tls_cert` and `tls_key` must be non-empty and exist.

**New behaviour:**
```
if tls_cert == "" && tls_key == "" → valid (no-TLS mode)
if tls_cert != "" || tls_key != "" → both must be non-empty and files must exist
```

---

## Tests to write (TDD order)

1. `TestValidate_ListenNoTLS` — listen with no tls_cert/tls_key → valid
2. `TestValidate_ListenPartialTLS` — tls_cert set but tls_key empty → error
3. `TestValidate_ListenPartialTLSReverse` — tls_key set but tls_cert empty → error
4. `TestValidate_ListenBothTLS` — both set and files exist → valid (existing behaviour preserved)

---

## Open decisions logged

- **DECISION-011 (open):** Should `data_dir` in the sink TOML be a Docker volume mount
  (e.g. `/var/lib/tetherdb`) or can it be ephemeral in phase 1? Cursor state is not
  yet implemented so the value is unused — but the path must exist for config validation.
  Resolve before first production deployment.

---

## Out of scope for this PRP

- CI/CD pipeline for building and pushing the Docker image
- Postgres schema setup / migration
- Management API exposure via Traefik
- Multi-node or relay topology
- ACK protocol / pipeline engine wiring (separate PRPs)
