# MEMORY.md — tetherdb

Records resolved architectural decisions and current project state.
Read this at the start of every session before writing any code.

---

## ARCHITECTURAL DECISIONS

### 1. Source and target databases

**Decision:** Microsoft SQL Server is the source. PostgreSQL is the target. Unidirectional only. These are the only supported backends at launch.

**Why:** User-specified. Narrow scope allows deep feature coverage before expanding.

**Rules out:** MySQL, SQLite, Oracle, MongoDB in v1. Bidirectional sync. A pluggable connector interface must be designed so adding sources later does not require restructuring the core engine.

---

### 2. Implementation language

**Decision:** Go.

**Why:** Trivial cross-compilation to Windows and Linux. Single static binary — no runtime dependency on target machines. `kardianos/service` handles Windows Service + Linux systemd + macOS launchd in one library. Mature `go-mssqldb` (SQL Server) and `pgx` (Postgres) drivers. Faster iteration than Rust for a sync daemon with moderate throughput requirements.

**Rules out:** Rust, TypeScript, Python. CGO must remain off to preserve static binary output.

---

### 3. Distribution model

**Decision:** Two standalone daemons — `tetherdb-source` and `tetherdb-sink` — each a single static Go binary. Deployed as Windows Service, Linux systemd unit, or Docker container via `kardianos/service`. Each exposes a local HTTP management API on localhost (default :8080) for status, manual trigger, and log access.

**Why:** Databases are on separate private networks. Self-hosted daemons are the only viable model. Two binaries keeps dependency surfaces minimal — source never needs a Postgres driver; sink never needs a SQL Server driver.

**Rules out:** Hosted SaaS model. Single combined binary. Cloud relay.

---

### 4. Agent transport and sync architecture

**Decision:** Source agent dials sink agent over WebSocket/TLS on port 443 (configurable). Source reads SQL Server changes via CDC or Change Tracking, batches them, and sends to sink. Sink applies the batch to Postgres and sends an ACK. Source advances its change cursor only after ACK received.

**Why:** The two databases are on separate private networks with no shared VPN. SQL Server port 1433 is closed externally; Postgres network can open one inbound port. WebSocket over TLS on 443 is firewall-friendly, provides built-in message framing, and carries bidirectional ACK flow on a single persistent connection. ACK-before-advance guarantees no data loss on connection failure.

**Rules out:** Relay/broker architecture. Raw TCP. gRPC. Any model requiring inbound ports on the SQL Server network.

---

### 5. Error handling

**Decision:** Go idiomatic `(T, error)` returns throughout. No panics in library or daemon code. Custom error types wrap driver errors to add domain context. Every error is returned to the caller or logged with full context before retry or abort.

**Why:** Go's explicit error returns make every failure path visible at the call site. No hidden control flow.

**Rules out:** Panic-based error handling. Swallowing errors silently. Returning nil to signal failure.

---

## CURRENT PROJECT STATE

### Fully Working
- Context engineering system (CLAUDE.md, MEMORY.md, CONTEXT.md, DECISIONS.md, CHANGELOG.md, .llmignore, PRPs/, docs/)
- All five blocking architectural decisions resolved

### In Progress
- Nothing — ready to write the first PRP

### Not Started
- Everything else

---

## NEXT SESSION START POINT

All architectural decisions are resolved. The next step is to write the first PRP.

Likely candidates for the first feature:
1. **SQL Server connection + Change Tracking reader** — the source agent's core loop
2. **WebSocket transport layer** — the pipe between source and sink
3. **Postgres writer** — the sink agent's apply logic

Discuss with the user which to tackle first. Write a PRP before writing any code.
