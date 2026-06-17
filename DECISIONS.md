# DECISIONS.md — tetherdb

Tracks architectural and design questions that are open, deferred, or resolved.

Rules:
- Every open decision blocks implementation of the code it affects.
- The AI must not implement anything that depends on an open decision.
- When a decision is resolved, move it to the RESOLVED section and record the outcome.
- Once resolved, copy the outcome to MEMORY.md as an architectural decision.

---

## OPEN — Requires human input before implementation

No open decisions — all architectural questions resolved or deferred.

---

## DEFERRED — Acknowledged, not yet needed

### DECISION-006 — Conflict resolution strategy

**Status:** deferred
**Raised:** 2026-06-17 — Session 1
**Revisit when:** First sync feature is being designed

**Question:** When two databases have diverged (e.g., both modified the same row), how does tetherdb resolve the conflict?

**Notes:** Unidirectional sync (SQL Server → Postgres only) makes this unlikely in normal operation — Postgres is never the source of truth. Revisit if bidirectional sync is ever added.

---

### DECISION-007 — Observability and telemetry

**Status:** deferred
**Raised:** 2026-06-17 — Session 1
**Revisit when:** First working sync implementation exists

**Question:** How does tetherdb expose sync metrics, logs, and traces?

**Notes:** Structured logging (Go's `log/slog`) is a baseline requirement from day one. Metrics (Prometheus? OpenTelemetry?) deferred until there is something worth observing.

---

## RESOLVED

### DECISION-001 — Implementation language and runtime

**Status:** resolved
**Raised:** 2026-06-17 — Session 1
**Resolved:** 2026-06-17 — Session 1

**Question:** What language and runtime will tetherdb be written in?

**Outcome:** Go.

**Rationale:** Cross-compilation is trivial (`GOOS=windows GOARCH=amd64 go build`). Single static binary, no runtime dependency — clean Docker and Windows/Linux deployment. `kardianos/service` abstracts Windows Service + Linux systemd + macOS launchd behind one interface. Mature SQL Server (`go-mssqldb`) and PostgreSQL (`pgx`) drivers. Faster iteration than Rust for a sync daemon where throughput requirements are moderate.

**Copied to MEMORY.md:** yes

---

### DECISION-002 — Database sync architecture and agent transport

**Status:** resolved
**Raised:** 2026-06-17 — Session 1
**Resolved:** 2026-06-17 — Session 1

**Question:** How does tetherdb synchronize databases and how do agents communicate?

**Outcome:** Two-agent model. Source agent runs in SQL Server's private network; sink agent runs in PostgreSQL's private network. Source dials sink over WebSocket/TLS on port 443 (configurable). Source reads changes from SQL Server via CDC or Change Tracking. Changes are batched and sent to sink. Sink applies the batch to Postgres and sends an ACK. Source advances its change cursor only after receiving the ACK — guarantees no data loss on connection failure.

**Rationale:** Databases are on separate private networks with no shared VPN. SQL Server port 1433 is closed to the internet; Postgres network can open one inbound port. WebSocket over TLS on port 443 is firewall-friendly, provides built-in message framing, and supports bidirectional ACK flow on a single persistent connection.

**Copied to MEMORY.md:** yes

---

### DECISION-003 — Supported database backends

**Status:** resolved
**Raised:** 2026-06-17 — Session 1
**Resolved:** 2026-06-17 — Session 1

**Question:** Which database systems will tetherdb support at launch?

**Outcome:** Microsoft SQL Server (source) → PostgreSQL (target). Unidirectional only. Architecture must use a pluggable connector interface so additional sources can be added later without restructuring the core engine.

**Rationale:** User-specified. Narrow initial scope allows deep feature coverage before expanding.

**Copied to MEMORY.md:** yes

---

### DECISION-004 — Distribution model

**Status:** resolved
**Raised:** 2026-06-17 — Session 1
**Resolved:** 2026-06-17 — Session 1

**Question:** How is tetherdb distributed and consumed?

**Outcome:** Two standalone daemons — `tetherdb-source` and `tetherdb-sink` — each a single static Go binary. Deployed as a Windows Service, Linux systemd unit, or Docker container via `kardianos/service`. Each agent exposes a local HTTP management API on localhost only (default port 8080) for status, manual trigger, and log access. No relay, no cloud component.

**Rationale:** Databases are on private networks; a self-hosted daemon is the only viable model. Two separate binaries keeps each agent's dependency surface minimal — the source binary never needs a Postgres driver; the sink binary never needs a SQL Server driver.

**Copied to MEMORY.md:** yes

---

### DECISION-005 — Error handling strategy

**Status:** resolved
**Raised:** 2026-06-17 — Session 1
**Resolved:** 2026-06-17 — Session 1

**Question:** What is the specific error handling pattern for this project?

**Outcome:** Go idiomatic `(T, error)` returns throughout. No panics in library or daemon code — panics are reserved for programmer errors (nil pointer on startup config, missing required field). Custom error types wrap stdlib and driver errors to provide domain context. Errors are never swallowed silently; every error is either returned to the caller or logged with full context before the operation is retried or aborted.

**Rationale:** DECISION-001 resolved to Go. Go's explicit error returns make every failure path visible at the call site. No exceptions, no hidden control flow.

**Copied to MEMORY.md:** yes
