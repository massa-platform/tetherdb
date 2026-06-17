# MEMORY.md — tetherdb

Records resolved architectural decisions and current project state.
Read this at the start of every session before writing any code.

---

## ARCHITECTURAL DECISIONS

### 1. Network model — nodes, not agents

**Decision:** tetherdb is a **data mesh network**. The unit of deployment is a **node** — a single binary that can simultaneously act as a source (reading from a database, forwarding changes outbound), a sink (receiving changes inbound, writing to a database), or both. Nodes connect to each other over WebSocket/TLS. The topology is a directed graph configured entirely through TOML — no code changes needed to add new routes.

**Why:** A fixed two-binary source/sink model cannot support many-to-one, one-to-many, or chained sync topologies. The node model makes all of these fall out from configuration. The abstraction cost is low — the core interfaces (Connector, pipeline, cursor) are the same; the node just runs multiple pipelines simultaneously.

**Rules out:** `tetherdb-source` and `tetherdb-sink` as separate binaries. Any hardcoded assumption about direction. Any architecture that requires code changes to add a new sync route.

---

### 2. Connector interface — pluggable, database-agnostic

**Decision:** The node never talks to a database directly. All database access goes through a `Connector` interface. A `Connector` can be a **reader** (produces changes), a **writer** (consumes changes), or both. SQL Server and PostgreSQL are the first two implementations. The sync engine, transport, and state management are written entirely against the interface.

```go
type Connector interface {
    Probe(ctx context.Context) error
    Close() error
}

type Reader interface {
    Connector
    InitialSync(ctx context.Context, table string, cursor InitialCursor) (RowStream, error)
    Changes(ctx context.Context, tables []string, from ChangeCursor) (ChangeStream, error)
}

type Writer interface {
    Connector
    Apply(ctx context.Context, batch []Change) error
}
```

The universal change type:

```go
type Change struct {
    Schema string
    Table  string
    Op     Op               // Insert, Update, Delete
    PK     map[string]any
    Before map[string]any   // nil on Insert
    After  map[string]any   // nil on Delete
}
```

**Why:** Decouples the sync engine from any specific database. Adding MySQL, Oracle, or MongoDB as a source or target requires only a new Connector implementation — no changes to the engine, transport, or state management.

**Rules out:** Importing database drivers anywhere except inside their respective connector packages. Any sync engine code that references SQL Server or Postgres types directly.

---

### 3. Source publishes tables; sinks subscribe

**Decision:** Each node's database connector declares which tables it publishes (makes available for sync). Each outbound connection declares which tables it subscribes to from that source. Subscription must be a subset of what the source publishes — the node aborts startup if a sink subscribes to an unpublished table.

**Why:** Gives the source operator control over what is exposed. Gives each sink independent control over what it receives. Different sinks can receive different subsets of the same source's tables.

**Rules out:** Implicit "sync all tables" behaviour. Sinks receiving tables they did not explicitly subscribe to.

---

### 4. Per-connection, per-table cursors in SQLite

**Decision:** State (change cursors, initial sync progress) is stored in a local SQLite database on each node. The cursor key is `(connection_name, table)`. Each outbound connection advances its own cursor independently — if one connection is down, others continue unaffected. State presence determines sync mode: no cursor → full initial sync; cursor present → resume from that position.

**Why:** Simple, local, no external dependency. Independent cursors mean a slow or failing sink never blocks a healthy one. State-as-cursor eliminates the need for explicit "initial sync" vs "change tracking" mode flags.

**Rules out:** Global shared cursor. External state stores (Redis, etcd). Any model where one slow sink blocks another.

---

### 5. Transport — WebSocket over TLS, port 443

**Decision:** Node-to-node communication uses WebSocket over TLS on port 443 (configurable). The connecting node (outbound) dials the receiving node (inbound). The receiving node listens on one port. ACK flows back on the same connection. The connecting node advances its cursor only after receiving an ACK.

**Why:** WebSocket provides built-in message framing and bidirectional flow on a single persistent connection. Port 443 passes through corporate firewalls without special rules. ACK-before-advance is the data-loss prevention invariant.

**Rules out:** Raw TCP. gRPC. Any relay or broker in the middle. Cursor advance before ACK.

---

### 6. Implementation language

**Decision:** Go. CGO_ENABLED=0. Single static binary.

**Why:** Trivial cross-compilation to Windows, Linux, ARM. `kardianos/service` handles Windows Service + Linux systemd + macOS launchd. Mature SQL Server (`go-mssqldb`) and Postgres (`pgx`) drivers. No runtime dependency on target machines.

**Rules out:** Rust, TypeScript, Python. CGO. Dynamic linking.

---

### 7. Configuration

**Decision:** TOML file. No hardcoded values anywhere. All connection parameters (hosts, ports, credentials, table lists) come from the config file or environment variables.

**Why:** User-specified. TOML is human-readable, widely supported, unambiguous.

**Rules out:** YAML. JSON. Hardcoded hosts, ports, or credentials.

---

### 8. Error handling

**Decision:** Go idiomatic `(T, error)` returns throughout. No panics in library or node code. Custom error types wrap driver errors to add domain context. Every error is returned to the caller or logged with full context before retry or abort. Automatic retry with exponential backoff on transient failures (connection loss, timeouts).

**Why:** Go's explicit error returns make every failure path visible at the call site.

**Rules out:** Panic-based error handling. Swallowing errors silently. Returning nil to signal failure.

---

## CURRENT PROJECT STATE

### Fully Working
- Context engineering system (CLAUDE.md, MEMORY.md, CONTEXT.md, DECISIONS.md, CHANGELOG.md, .llmignore, PRPs/, docs/)
- All architectural decisions resolved

### In Progress
- Nothing — ready to write the first PRP

### Not Started
- Everything else

---

## NEXT SESSION START POINT

All architectural decisions are resolved. The next step is to write the PRP for the first feature.

The agreed build order:
1. **SQL Server Connector (Reader)** — implements the Reader interface; auto-detects CDC vs Change Tracking; handles initial sync and change streaming
2. **WebSocket transport layer** — node-to-node connection, ACK protocol
3. **PostgreSQL Connector (Writer)** — implements the Writer interface; applies change batches
4. **Node engine** — wires connectors and transport together, manages pipelines and cursors

Write the PRP for item 1 before writing any code.
