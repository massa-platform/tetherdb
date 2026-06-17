# Technical Specification — tetherdb

**Status:** draft
**Date:** 2026-06-17
**Location:** docs/specs/tetherdb.md

---

## 1. Executive Summary

tetherdb is a self-hosted data mesh network for database synchronisation. It runs as a single binary (`tetherdb`) on any machine inside a private network. Each deployment is a node. Nodes connect to each other over WebSocket/TLS and form a directed sync graph configured entirely in TOML. The first supported sync path is Microsoft SQL Server → PostgreSQL. The architecture is database-agnostic by design — adding new database types requires only a new connector package.

---

## 2. Background

Database synchronisation across private networks is operationally painful. The databases may sit on separate networks with no shared VPN, with database ports (SQL Server :1433, Postgres :5432) closed to the internet. Existing tools either require cloud connectivity, open inbound firewall rules on the database host, or are tightly coupled to a specific database pair.

tetherdb is designed for this environment. Every node only makes outbound connections. Only the receiving node needs one inbound port open (:443 by default). No cloud component. No relay. No broker. The sync graph is defined in config and can express any topology: one-to-one, one-to-many, many-to-one, or chained.

---

## 3. Goals

1. Sync data from Microsoft SQL Server to PostgreSQL across separate private networks with no shared VPN.
2. Support one-to-many, many-to-one, and chained sync topologies through configuration alone — no code changes.
3. Guarantee no data loss on connection failure via ACK-gated cursor advance.
4. Deploy as a single static binary on Windows (Service), Linux (systemd), and Docker with no runtime dependencies.
5. Allow each downstream node to subscribe to a subset of the tables the upstream node publishes.
6. Resume interrupted syncs (initial full sync or change tracking) from the last confirmed position on restart.
7. Detect the best available SQL Server change mechanism (CDC or Change Tracking) automatically at startup.
8. Propagate configuration changes across the network over existing node connections — no separate control channel.

---

## 4. Non-Goals

- Bidirectional sync between two databases (v1 is unidirectional only per connection).
- Schema migration or DDL replication — column additions, renames, and table drops are out of scope.
- Conflict resolution — because sync is unidirectional, conflicts do not arise in normal operation.
- Built-in encryption key management — TLS certificates are provided by the operator.
- A hosted or cloud-managed version of tetherdb.
- Support for MySQL, Oracle, MongoDB, or any database other than SQL Server (source) and PostgreSQL (target) in v1.
- A graphical user interface.

---

## 5. Design / Architecture

### 5.1 The Node

Every tetherdb deployment is a node. A node is a single process running the `tetherdb` binary. A node can simultaneously:

- Read changes from a local database (acting as a **publisher**)
- Accept inbound WebSocket connections from upstream nodes (acting as a **subscriber/sink**)
- Dial outbound WebSocket connections to downstream nodes (acting as a **forwarder/source**)

There is no distinction between a "source binary" and a "sink binary". Role is determined by configuration.

### 5.2 The Sync Network

Nodes form a directed acyclic graph. Each edge is a WebSocket/TLS connection. Changes flow in one direction per edge. Configuration changes can flow in either direction over the same connection.

```
[SQL Server] ──► [Node A] ──────────────────────────► [Node C] ──► [Postgres]
                          \                           /
                           ──► [Node B] ────────────
                                        \
                                         ──────────► [Node D] ──► [Postgres]
```

### 5.3 Connector Interface

The sync engine never imports a database driver directly. All database access goes through two interfaces:

```go
// Reader is implemented by database source connectors.
// SQL Server is the first implementation.
type Reader interface {
    Probe(ctx context.Context) error
    InitialSync(ctx context.Context, table string, cursor InitialCursor) (RowStream, error)
    Changes(ctx context.Context, tables []string, from ChangeCursor) (ChangeStream, error)
    Close() error
}

// Writer is implemented by database sink connectors.
// PostgreSQL is the first implementation.
type Writer interface {
    Probe(ctx context.Context) error
    Apply(ctx context.Context, batch []Change) error
    Close() error
}
```

The universal change type that flows between connectors and across the network:

```go
type Op int

const (
    Insert Op = iota
    Update
    Delete
)

type Change struct {
    Schema string
    Table  string
    Op     Op
    PK     map[string]any
    Before map[string]any  // nil on Insert
    After  map[string]any  // nil on Delete
}
```

### 5.4 SQL Server Connector (Reader)

The SQL Server connector auto-detects the best available change mechanism at startup:

```
startup probe
  ├── CDC enabled on this database?
  │     yes → use CDC reader (full before/after values from transaction log)
  │     no  ↓
  ├── Change Tracking enabled on this database?
  │     yes → use CT reader (PK + current row values via join)
  │     no  ↓
  └── abort: no supported change mechanism — log clear error message
```

**Initial sync:** On first connection to a table (no cursor in SQLite), the connector reads the full table in batches and streams rows as `Insert` changes. Progress is checkpointed per batch so a restart resumes mid-table rather than from the beginning.

**Change tracking:** After initial sync completes, the connector switches to polling for changes (CT) or tailing the CDC log. Changes are batched and forwarded downstream.

**Authentication:** Supports both SQL Server login (username + password) and Windows Authentication.

**Reconnection:** On connection loss to SQL Server, the connector retries with exponential backoff. It does not advance the cursor while disconnected.

### 5.5 PostgreSQL Connector (Writer)

Receives batches of `Change` values and applies them to the target Postgres database:

- `Insert` → `INSERT ... ON CONFLICT DO UPDATE` (upsert, idempotent)
- `Update` → `INSERT ... ON CONFLICT DO UPDATE` (same as insert — handles redelivery)
- `Delete` → `DELETE WHERE pk = ?`

Applies each batch inside a single transaction. If the transaction fails, the entire batch is rejected and the upstream node is not ACKed — it will retry.

### 5.6 Transport — WebSocket over TLS

Node-to-node communication uses WebSocket over TLS. The connecting (outbound) node dials the receiving (inbound) node on port 443 (configurable). The connection is persistent. Messages flow in both directions on the same connection:

- **Outbound:** change batches (source → sink)
- **Inbound:** ACKs, config updates (sink → source)

**ACK protocol:**

```
source                        sink
  │                            │
  ├── ChangeBatch{id: 42} ────►│
  │                            ├── apply to database
  │◄─── Ack{id: 42} ──────────┤
  ├── advance cursor to 42     │
  │                            │
  ├── ChangeBatch{id: 43} ────►│
```

The source never advances its cursor until it receives an ACK for that batch. On reconnection, the source replays from the last ACKed cursor position. Duplicate delivery is possible; the sink's upsert/delete operations are idempotent.

### 5.7 State — SQLite

Each node maintains a local SQLite database for:

- Change cursors, keyed by `(connection_name, table)`
- Initial sync progress (last checkpointed PK), keyed by `(connection_name, table)`
- Published table registry (which tables this node exposes)

SQLite is accessed via `modernc.org/sqlite` (pure Go, CGO-free).

State presence determines sync mode — no cursor entry means full initial sync; cursor present means resume from that position.

### 5.8 Pub/Sub Table Model

Each node's connector declares which tables it **publishes** (makes available for downstream nodes). Each outbound connection declares which tables it **subscribes** to. At startup:

1. The connecting node sends its subscription list.
2. The receiving node validates that every subscribed table is in its published list.
3. If any subscribed table is not published, the connection is rejected with a clear error.
4. If validation passes, the sync pipeline starts.

Different downstream nodes can subscribe to different subsets of the same upstream node's published tables. Each subscription is an independent pipeline with its own cursor.

### 5.9 Fan-out and Fan-in

**Fan-out (one source, multiple sinks):**
The source node runs one pipeline goroutine per outbound connection. Each pipeline has its own cursor. If one downstream node is slow or disconnected, others continue unaffected.

**Fan-in (multiple sources, one sink):**
The sink node accepts multiple inbound connections. Each arrives on its own goroutine. Table name collisions across sources are namespaced by source node name: `{source_name}.{table}`.

### 5.10 Configuration

All configuration is in TOML. No hardcoded values anywhere. Example:

```toml
[node]
name    = "erp-node"
data_dir = "/var/lib/tetherdb"   # SQLite state location

[listen]
address = "0.0.0.0:443"
tls_cert = "/etc/tetherdb/cert.pem"
tls_key  = "/etc/tetherdb/key.pem"

[management]
address = "127.0.0.1:8080"      # local HTTP management API

[connector]
driver   = "sqlserver"
host     = "sqlserver.internal"
port     = 1433
database = "erp"
auth     = "sqlserver"          # or "windows"
username = "${TETHERDB_SQL_USER}"
password = "${TETHERDB_SQL_PASS}"

[connector.publish]
tables = ["orders", "customers", "products"]

[[connections]]
name    = "primary-sink"
address = "sink-node.internal:443"
subscribe = ["orders", "customers", "products"]

[[connections]]
name    = "analytics-sink"
address = "analytics-node.internal:443"
subscribe = ["orders"]
```

Credentials are never stored in the TOML file directly — environment variable interpolation (`${VAR}`) is supported for all sensitive fields.

### 5.11 Config Propagation

Since WebSocket connections are bidirectional, config changes can be relayed to connected nodes over existing connections without a separate control channel. An operator sends a config change to any reachable node via its local HTTP management API; that node forwards the change to its connected peers; each receiving node persists the change and hot-reloads affected pipelines.

### 5.12 Management API

Each node exposes a local HTTP API on `127.0.0.1:8080` (configurable, never `0.0.0.0` by default):

| Method | Path | Description |
|--------|------|-------------|
| GET | `/status` | Node health, active connections, pipeline state |
| GET | `/connections` | List of inbound and outbound connections |
| POST | `/sync/{connection}/{table}/trigger` | Force immediate sync cycle |
| GET | `/log` | Recent log entries |
| PUT | `/config` | Push config change (also relays to connected nodes) |

### 5.13 Service Management

`kardianos/service` wraps the node process for platform-specific service management:

- **Windows:** Windows Service
- **Linux:** systemd unit
- **macOS:** launchd plist
- **Docker:** binary runs as PID 1, signal handling via `kardianos/service`

---

## 6. Implementation Plan

Steps in dependency order. Each step is one PRP.

1. **SQL Server connector** — `Reader` interface + SQL Server implementation (CT + CDC auto-detect, initial sync, change polling, reconnect with backoff)
2. **PostgreSQL connector** — `Writer` interface + Postgres implementation (upsert/delete batch apply, transaction-per-batch)
3. **WebSocket transport** — node-to-node connection, ACK protocol, reconnect with backoff
4. **State layer** — SQLite schema, cursor read/write, initial sync checkpoint
5. **Pipeline engine** — wires Reader → transport → Writer; fan-out goroutines; cursor advance on ACK
6. **Pub/sub validation** — publish declaration, subscription validation at connection time, table namespacing for fan-in
7. **Config loader** — TOML parsing, env var interpolation, hot-reload
8. **Management API** — HTTP server, status/log/trigger/config endpoints
9. **Service wrapper** — `kardianos/service` integration, signal handling
10. **Config propagation** — relay config changes over existing WebSocket connections

---

## 7. Open Questions

- **DECISION-006** (deferred): Conflict resolution strategy — not needed for v1 unidirectional sync; revisit if bidirectional is ever added.
- **DECISION-007** (deferred): Observability — structured logging (`log/slog`) from day one; metrics and tracing deferred until first working sync exists.
- **DECISION-008** (deferred): Config propagation ACK semantics — reliable delivery vs fire-and-forget; revisit during step 10 of implementation plan.

---

## 8. References

- `MEMORY.md` — all resolved architectural decisions with rationale
- `DECISIONS.md` — open and deferred questions
- `docs/source/constraints/private-network-access.md` — network topology constraint
- `kardianos/service` — cross-platform service management
- `modernc.org/sqlite` — pure Go SQLite driver
- `gorilla/websocket` — WebSocket library
- `go-mssqldb` — SQL Server driver
- `pgx` — PostgreSQL driver
